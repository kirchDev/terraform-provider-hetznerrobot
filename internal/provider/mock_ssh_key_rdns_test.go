package provider

import (
	"crypto/md5"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

func init() {
	// --- SSH keys ---
	route(http.MethodGet, `/key`, func(m *mockRobot, _ *http.Request, _ []string, _ url.Values) (int, any) {
		return wrapList("key", m.list("key", nil), "No keys found")
	})
	route(http.MethodPost, `/key`, func(m *mockRobot, _ *http.Request, _ []string, form url.Values) (int, any) {
		name, data := form.Get("name"), form.Get("data")
		if name == "" || data == "" {
			return mockInvalid("name", "data")
		}
		fp := mockFingerprint(data)
		if _, ok := m.get("key", fp); ok {
			return mockErr(409, "KEY_ALREADY_EXISTS", "The supplied key already exists")
		}
		fields := strings.Fields(data)
		typ := map[string]string{"ssh-ed25519": "ED25519", "ssh-rsa": "RSA"}[fields[0]]
		k := map[string]any{
			"name": name, "fingerprint": fp, "type": typ, "size": int64(256),
			// Robot answers in OpenSSH format without the comment.
			"data":       strings.Join(fields[:min(2, len(fields))], " "),
			"created_at": "2026-10-11 12:00:00",
		}
		m.put("key", fp, k)
		return 201, wrap("key", copyMap(k))
	})
	route(http.MethodGet, `/key/([^/]+)`, func(m *mockRobot, _ *http.Request, a []string, _ url.Values) (int, any) {
		k, ok := m.get("key", a[0])
		if !ok {
			return mockErr(404, "NOT_FOUND", "Key not found")
		}
		return 200, wrap("key", copyMap(k))
	})
	route(http.MethodPost, `/key/([^/]+)`, func(m *mockRobot, _ *http.Request, a []string, form url.Values) (int, any) {
		k, ok := m.get("key", a[0])
		if !ok {
			return mockErr(404, "NOT_FOUND", "Key not found")
		}
		if form.Get("name") == "" {
			return mockInvalid("name")
		}
		k["name"] = form.Get("name")
		return 200, wrap("key", copyMap(k))
	})
	route(http.MethodDelete, `/key/([^/]+)`, func(m *mockRobot, _ *http.Request, a []string, _ url.Values) (int, any) {
		if _, ok := m.get("key", a[0]); !ok {
			return mockErr(404, "NOT_FOUND", "Key not found")
		}
		m.del("key", a[0])
		return 200, nil
	})

	// --- reverse DNS ---
	route(http.MethodGet, `/rdns`, func(m *mockRobot, _ *http.Request, _ []string, form url.Values) (int, any) {
		serverIP := form.Get("server_ip")
		return wrapList("rdns", m.list("rdns", func(e map[string]any) bool {
			return serverIP == "" || m.ipServer(e["ip"].(string)) == serverIP
		}), "No reverse DNS entries found")
	})
	route(http.MethodGet, `/rdns/([^/]+)`, func(m *mockRobot, _ *http.Request, a []string, _ url.Values) (int, any) {
		if !m.knownIP(a[0]) {
			return mockErr(404, "IP_NOT_FOUND", "The IP address "+a[0]+" was not found")
		}
		e, ok := m.get("rdns", a[0])
		if !ok {
			return mockErr(404, "RDNS_NOT_FOUND", "The IP address "+a[0]+" has no reverse DNS entry yet")
		}
		return 200, wrap("rdns", copyMap(e))
	})
	rdnsWrite := func(upsert bool) mockHandler {
		return func(m *mockRobot, _ *http.Request, a []string, form url.Values) (int, any) {
			if !m.knownIP(a[0]) {
				return mockErr(404, "IP_NOT_FOUND", "The IP address "+a[0]+" was not found")
			}
			if form.Get("ptr") == "" {
				return mockInvalid("ptr")
			}
			_, exists := m.get("rdns", a[0])
			if exists && !upsert {
				return mockErr(409, "RDNS_ALREADY_EXISTS", "There is already an existing reverse DNS entry")
			}
			e := map[string]any{"ip": a[0], "ptr": form.Get("ptr")}
			m.put("rdns", a[0], e)
			status := 201
			if exists {
				status = 200
			}
			return status, wrap("rdns", copyMap(e))
		}
	}
	route(http.MethodPost, `/rdns/([^/]+)`, rdnsWrite(true))
	route(http.MethodPut, `/rdns/([^/]+)`, rdnsWrite(false))
	route(http.MethodDelete, `/rdns/([^/]+)`, func(m *mockRobot, _ *http.Request, a []string, _ url.Values) (int, any) {
		if !m.knownIP(a[0]) {
			return mockErr(404, "IP_NOT_FOUND", "The IP address "+a[0]+" was not found")
		}
		m.del("rdns", a[0])
		return 200, nil
	})
}

func mockFingerprint(data string) string {
	sum := md5.Sum([]byte(data))
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02x", b)
	}
	return strings.Join(parts, ":")
}

// ipServer returns the main IP of the server an IP or subnet address belongs to.
func (m *mockRobot) ipServer(ip string) string {
	if e, ok := m.get("ip", ip); ok {
		return e["server_ip"].(string)
	}
	for _, s := range m.list("subnet", nil) {
		p := strings.TrimSuffix(s["ip"].(string), "::")
		if strings.HasPrefix(ip, p) {
			return s["server_ip"].(string)
		}
	}
	return ""
}
