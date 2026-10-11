package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := New(srv.URL, "user", "pass", "test")
	c.SetPacing(time.Millisecond, 50*time.Millisecond)
	return c
}

func TestBasicAuthAndForm(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != "user" || p != "pass" {
			t.Errorf("basic auth = %q/%q/%v", u, p, ok)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
			t.Errorf("content type = %q", ct)
		}
		_ = r.ParseForm()
		if got := r.PostForm["authorized_key[]"]; len(got) != 2 {
			t.Errorf("authorized_key[] = %v", got)
		}
		if got := r.PostForm.Get("rules[input][0][name]"); got != "rule 1" {
			t.Errorf("rules[input][0][name] = %q", got)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"server":{"server_number":321,"server_name":"a"}}`))
	})
	form := url.Values{}
	form.Add("authorized_key[]", "aa:bb")
	form.Add("authorized_key[]", "cc:dd")
	form.Set("rules[input][0][name]", "rule 1")
	got, err := SendObject[struct {
		ServerNumber int64  `json:"server_number"`
		ServerName   string `json:"server_name"`
	}](context.Background(), c, http.MethodPost, "/server/321", "server", form)
	if err != nil || got.ServerNumber != 321 || got.ServerName != "a" {
		t.Fatalf("SendObject = %+v, %v", got, err)
	}
}

func TestGetListEmptyIs404(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("server_ip") != "1.2.3.4" {
			t.Errorf("query = %q", r.URL.RawQuery)
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"status":404,"code":"NOT_FOUND","message":"No IP addresses found"}}`))
	})
	list, err := GetList[map[string]any](context.Background(), c, "/ip", "ip", url.Values{"server_ip": {"1.2.3.4"}})
	if err != nil || list == nil || len(list) != 0 {
		t.Fatalf("GetList = %v, %v; want an empty, non-nil list", list, err)
	}
}

func TestUnwrapList(t *testing.T) {
	raw := json.RawMessage(`[{"key":{"name":"a"}},{"key":{"name":"b"}}]`)
	got, err := UnwrapList[struct{ Name string }](raw, "key")
	if err != nil || len(got) != 2 || got[1].Name != "b" {
		t.Fatalf("UnwrapList = %+v, %v", got, err)
	}
	bare, err := Unwrap[struct{ ID int64 }](json.RawMessage(`{"id":4321,"name":"x"}`), "vswitch")
	if err != nil || bare.ID != 4321 {
		t.Fatalf("Unwrap of a bare object = %+v, %v", bare, err)
	}
}

func TestErrorFormat(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/invalid":
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":{"status":400,"code":"INVALID_INPUT","message":"invalid input","missing":["name"],"invalid":["vlan"]}}`))
		case "/limit":
			w.WriteHeader(403)
			_, _ = w.Write([]byte(`{"error":{"status":403,"code":"RATE_LIMIT_EXCEEDED","max_request":200,"interval":3600,"message":"rate limit exceeded"}}`))
		case "/auth":
			w.WriteHeader(401)
		}
	})
	err := c.Get(context.Background(), "/invalid", nil, nil)
	if !HasCode(err, "INVALID_INPUT") || !strings.Contains(err.Error(), "missing: name") || !strings.Contains(err.Error(), "invalid: vlan") {
		t.Errorf("invalid input error = %v", err)
	}
	err = c.Get(context.Background(), "/limit", nil, nil)
	if !HasCode(err, "RATE_LIMIT_EXCEEDED") || !strings.Contains(err.Error(), "200 requests per 3600 seconds") {
		t.Errorf("rate limit error = %v", err)
	}
	err = c.Get(context.Background(), "/auth", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "ten minutes") {
		t.Errorf("auth error = %v", err)
	}
}

func TestRetryPolicy(t *testing.T) {
	var gets, posts, unauthorized atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/auth":
			unauthorized.Add(1)
			w.WriteHeader(401)
		case r.Method == http.MethodGet:
			if gets.Add(1) < 3 {
				w.WriteHeader(500)
				return
			}
			_, _ = w.Write([]byte(`{}`))
		default:
			posts.Add(1)
			w.WriteHeader(500)
		}
	})
	if err := c.Get(context.Background(), "/x", nil, nil); err != nil || gets.Load() != 3 {
		t.Errorf("GET: err=%v attempts=%d, want success after 3", err, gets.Load())
	}
	if err := c.Send(context.Background(), http.MethodPost, "/x", url.Values{}, nil); err == nil || posts.Load() != 1 {
		t.Errorf("POST: err=%v attempts=%d, want one attempt — a write that 500'd may have landed", err, posts.Load())
	}
	_ = c.Get(context.Background(), "/auth", nil, nil)
	if unauthorized.Load() != 1 {
		t.Errorf("401 was retried %d times — three failures block the IP", unauthorized.Load())
	}
}

func TestMaintenanceIsRetriedForWrites(t *testing.T) {
	var n atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) == 1 {
			w.WriteHeader(503)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	})
	if err := c.Send(context.Background(), http.MethodPost, "/rdns/1.2.3.4", url.Values{}, nil); err != nil || n.Load() != 2 {
		t.Fatalf("err=%v attempts=%d", err, n.Load())
	}
}

func TestStorageBoxWritesArePaced(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{}`)) })
	start := time.Now()
	for range 3 {
		if err := c.Send(context.Background(), http.MethodPost, "/storagebox/1", url.Values{}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if d := time.Since(start); d < 100*time.Millisecond {
		t.Errorf("three Storage Box writes took %v, want at least two intervals", d)
	}
}

func TestFlexTypes(t *testing.T) {
	var v struct {
		A FlexInt      `json:"a"`
		B FlexInt      `json:"b"`
		C FlexInt      `json:"c"`
		D FlexString   `json:"d"`
		E FlexString   `json:"e"`
		F StringOrList `json:"f"`
		G StringOrList `json:"g"`
		H StringOrList `json:"h"`
		I FlexFloat    `json:"i"`
	}
	in := `{"a":29,"b":"64","c":null,"d":321,"e":null,"f":["linux","vkvm"],"g":"linux","h":[64,32],"i":"49.5800"}`
	if err := json.Unmarshal([]byte(in), &v); err != nil {
		t.Fatal(err)
	}
	if v.A.Value != 29 || v.B.Value != 64 || v.C.Valid || v.D.Value != "321" || v.E.Valid {
		t.Errorf("scalars: %+v", v)
	}
	if !v.F.IsList || len(v.F.List) != 2 || v.G.IsList || v.G.Single != "linux" || v.H.List[0] != "64" {
		t.Errorf("string-or-list: %+v %+v %+v", v.F, v.G, v.H)
	}
	if v.I.Value != 49.58 {
		t.Errorf("float: %+v", v.I)
	}
}

func TestOrdersAreNeverRetried(t *testing.T) {
	var n atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.WriteHeader(503)
	})
	if err := c.Send(context.Background(), http.MethodPost, "/order/server/transaction", url.Values{}, nil); err == nil || n.Load() != 1 {
		t.Fatalf("err=%v attempts=%d, want one attempt — a 503 after the order landed would buy twice", err, n.Load())
	}
}

func TestGetListParentMissing(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		switch r.URL.Path {
		case "/storagebox":
			_, _ = w.Write([]byte(`{"error":{"status":404,"code":"STORAGEBOX_NOT_FOUND","message":"No Storage Boxes found"}}`))
		default:
			_, _ = w.Write([]byte(`{"error":{"status":404,"code":"STORAGEBOX_NOT_FOUND","message":"Storage Box with ID 1 not found"}}`))
		}
	})
	if list, err := GetList[map[string]any](context.Background(), c, "/storagebox", "storagebox", nil); err != nil || len(list) != 0 {
		t.Errorf("top-level empty collection: %v, %v", list, err)
	}
	if _, err := GetList[map[string]any](context.Background(), c, "/storagebox/1/snapshot", "snapshot", nil); !NotFound(err) {
		t.Errorf("missing parent should stay an error, got %v", err)
	}
}
