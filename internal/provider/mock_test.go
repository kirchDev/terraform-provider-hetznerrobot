package provider

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kirchDev/terraform-provider-hetznerrobot/internal/client"
)

// mockRobot is an in-memory stand-in for the Robot webservice, faithful to the
// documented shapes:
//   - HTTP Basic auth, form-encoded bodies (DELETE included);
//   - entities wrapped in their name ({"server": {…}}), collections as
//     [{"server": {…}}], vSwitches bare;
//   - an empty collection is a 404 NOT_FOUND, not [];
//   - errors as {"error": {"status", "code", "message"}}.
//
// Each area registers its endpoints with route() and its fixtures with seed()
// from an init() in its own mock_*_test.go file, so the core stays small.
type mockRobot struct {
	t        *testing.T
	mu       sync.Mutex
	requests []string
	// tables holds the entities per kind, keyed by their id rendered as text.
	tables map[string]map[string]map[string]any
	nextID int64
}

// mockHandler answers one request. args are the pattern's capture groups.
type mockHandler func(m *mockRobot, r *http.Request, args []string, form url.Values) (int, any)

type mockRoute struct {
	method string
	re     *regexp.Regexp
	handle mockHandler
}

var (
	mockRoutes []mockRoute
	mockSeeds  []func(*mockRobot)
)

// route registers a handler for method and a path pattern (anchored; use
// ([^/]+) for a segment).
func route(method, pattern string, h mockHandler) {
	mockRoutes = append(mockRoutes, mockRoute{method: method, re: regexp.MustCompile("^" + pattern + "$"), handle: h})
}

// seed registers a fixture loader that runs for every new mock.
func seed(f func(*mockRobot)) { mockSeeds = append(mockSeeds, f) }

const (
	mockUser     = "robot-test"
	mockPassword = "robot-test-password"
)

func newMockRobot(t *testing.T) *mockRobot {
	t.Helper()
	m := &mockRobot{t: t, tables: map[string]map[string]map[string]any{}, nextID: 1000}
	for _, s := range mockSeeds {
		s(m)
	}
	srv := httptest.NewServer(m)
	t.Cleanup(srv.Close)
	t.Setenv("HETZNER_ROBOT_ENDPOINT", srv.URL)
	t.Setenv("HETZNER_ROBOT_USER", mockUser)
	t.Setenv("HETZNER_ROBOT_PASSWORD", mockPassword)

	prevPoll, prevConfigure := pollInterval, configureClient
	pollInterval = 10 * time.Millisecond
	configureClient = func(c *client.Client) { c.SetPacing(time.Millisecond, time.Millisecond) }
	t.Cleanup(func() { pollInterval, configureClient = prevPoll, prevConfigure })

	accEnv(t)
	return m
}

func (m *mockRobot) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := r.URL.Path
	m.requests = append(m.requests, r.Method+" "+p)

	if u, pw, ok := r.BasicAuth(); !ok || u != mockUser || pw != mockPassword {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	// ParseForm ignores the body of a DELETE; the webservice reads it (vSwitch
	// cancellation and server removal), so the mock parses it by hand.
	b, _ := io.ReadAll(r.Body)
	form, err := url.ParseQuery(string(b))
	if err != nil {
		status, body := mockErr(400, "INVALID_INPUT", "invalid input")
		writeJSON(w, status, body)
		return
	}
	if r.Method != http.MethodGet && len(b) > 0 && r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
		m.t.Errorf("mock: %s %s sent a body without the form content type", r.Method, p)
	}
	// Query parameters only count for reads: a write whose fields ended up in
	// the URL instead of the body must fail here as it would against Robot.
	if r.Method == http.MethodGet {
		for k, v := range r.URL.Query() {
			form[k] = v
		}
	}

	for _, rt := range mockRoutes {
		if rt.method != r.Method {
			continue
		}
		if args := rt.re.FindStringSubmatch(p); args != nil {
			status, body := rt.handle(m, r, args[1:], form)
			writeJSON(w, status, body)
			return
		}
	}
	m.t.Errorf("mock: no route for %s %s", r.Method, p)
	status, body := mockErr(404, "NOT_FOUND", "Not found")
	writeJSON(w, status, body)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

// mockErr renders the documented error envelope.
func mockErr(status int, code, message string) (int, any) {
	return status, map[string]any{"error": map[string]any{"status": status, "code": code, "message": message}}
}

func mockInvalid(missing ...string) (int, any) {
	return 400, map[string]any{"error": map[string]any{
		"status": 400, "code": "INVALID_INPUT", "message": "invalid input", "missing": missing, "invalid": nil,
	}}
}

// wrap renders {"<key>": v}.
func wrap(key string, v any) map[string]any { return map[string]any{key: v} }

// wrapList renders [{"<key>": v}, …], or the documented 404 when empty.
func wrapList(key string, items []map[string]any, emptyMessage string) (int, any) {
	if len(items) == 0 {
		return mockErr(404, "NOT_FOUND", emptyMessage)
	}
	out := make([]map[string]any, len(items))
	for i, it := range items {
		out[i] = wrap(key, it)
	}
	return 200, out
}

// --- tables ---

func (m *mockRobot) table(kind string) map[string]map[string]any {
	t, ok := m.tables[kind]
	if !ok {
		t = map[string]map[string]any{}
		m.tables[kind] = t
	}
	return t
}

func (m *mockRobot) get(kind, id string) (map[string]any, bool) {
	e, ok := m.table(kind)[id]
	return e, ok
}

func (m *mockRobot) put(kind, id string, attrs map[string]any) { m.table(kind)[id] = attrs }

func (m *mockRobot) del(kind, id string) { delete(m.table(kind), id) }

// list returns the entities of kind sorted by id, optionally filtered.
func (m *mockRobot) list(kind string, keep func(map[string]any) bool) []map[string]any {
	ids := make([]string, 0, len(m.table(kind)))
	for id := range m.table(kind) {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := []map[string]any{}
	for _, id := range ids {
		e := m.table(kind)[id]
		if keep == nil || keep(e) {
			out = append(out, e)
		}
	}
	return out
}

func (m *mockRobot) newID() int64 {
	m.nextID++
	return m.nextID
}

// count reports how many recorded requests start with prefix ("POST /reset/").
func (m *mockRobot) count(prefix string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, r := range m.requests {
		if strings.HasPrefix(r, prefix) {
			n++
		}
	}
	return n
}

// copyMap returns a shallow copy, so a handler never hands out its table row.
func copyMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// --- form readers ---

func formBoolValue(form url.Values, key string) (bool, bool, error) {
	v, ok := form[key]
	if !ok {
		return false, false, nil
	}
	switch v[0] {
	case "true":
		return true, true, nil
	case "false":
		return false, true, nil
	}
	return false, true, fmt.Errorf("%s=%q is not true/false", key, v[0])
}

func formIntValue(form url.Values, key string) (int64, bool, error) {
	v, ok := form[key]
	if !ok {
		return 0, false, nil
	}
	n, err := strconv.ParseInt(v[0], 10, 64)
	return n, true, err
}

// applyForm copies the given form keys onto e, converting by the type the
// field already has in e (bool, int, string). It reports the first key whose
// value does not convert.
func applyForm(e map[string]any, form url.Values, keys ...string) (string, bool) {
	for _, k := range keys {
		if _, ok := form[k]; !ok {
			continue
		}
		switch e[k].(type) {
		case bool:
			b, _, err := formBoolValue(form, k)
			if err != nil {
				return k, false
			}
			e[k] = b
		case int, int64, float64:
			n, _, err := formIntValue(form, k)
			if err != nil {
				return k, false
			}
			e[k] = n
		default:
			e[k] = form.Get(k)
		}
	}
	return "", true
}
