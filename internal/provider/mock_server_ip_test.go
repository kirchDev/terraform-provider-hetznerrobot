package provider

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Mock endpoints for servers, server/IP/subnet cancellations, single IPs,
// subnets, their MAC addresses and failover IPs.

var mockCancellationReasons = []any{
	"Upgrade to a new server",
	"Dissatisfied with the hardware",
	"Server no longer necessary due to project ending",
	"Server too expensive",
}

const mockEarliestCancellation = "2026-11-30"

// mockServerFlags are the fields GET /server/{n} has and GET /server lacks.
var mockServerFlags = []string{"reset", "rescue", "vnc", "windows", "plesk", "cpanel", "wol", "hot_swap", "linked_storagebox"}

// mockMainMAC is the MAC of a server's main IP; mockSecondMAC the one of its
// additional IP — what an IPv6 subnet may be routed to.
const (
	mockMainMAC   = "00:21:85:62:3e:9c"
	mockSecondMAC = "00:21:85:62:3e:9d"
)

func init() {
	seed(func(m *mockRobot) {
		// Only server 421's location can be reserved after a cancellation.
		m.put("reservation_possible", fxServer2, map[string]any{})
	})

	// --- servers ---
	route(http.MethodGet, `/server`, func(m *mockRobot, _ *http.Request, _ []string, _ url.Values) (int, any) {
		items := []map[string]any{}
		for _, s := range m.list("server", nil) {
			c := copyMap(s)
			for _, k := range mockServerFlags {
				delete(c, k)
			}
			items = append(items, c)
		}
		return wrapList("server", items, "No server found")
	})
	route(http.MethodGet, `/server/(\d+)`, func(m *mockRobot, _ *http.Request, a []string, _ url.Values) (int, any) {
		s, ok := m.get("server", a[0])
		if !ok {
			return mockErr(404, "SERVER_NOT_FOUND", "Server with id "+a[0]+" not found")
		}
		return 200, wrap("server", copyMap(s))
	})
	route(http.MethodPost, `/server/(\d+)`, func(m *mockRobot, _ *http.Request, a []string, form url.Values) (int, any) {
		s, ok := m.get("server", a[0])
		if !ok {
			return mockErr(404, "SERVER_NOT_FOUND", "Server with id "+a[0]+" not found")
		}
		if strings.TrimSpace(form.Get("server_name")) == "" {
			return mockInvalid("server_name")
		}
		s["server_name"] = form.Get("server_name")
		return 200, wrap("server", copyMap(s))
	})

	// --- server cancellation ---
	route(http.MethodGet, `/server/(\d+)/cancellation`, func(m *mockRobot, _ *http.Request, a []string, _ url.Values) (int, any) {
		if _, ok := m.get("server", a[0]); !ok {
			return mockErr(404, "SERVER_NOT_FOUND", "Server with id "+a[0]+" not found")
		}
		return 200, wrap("cancellation", m.serverCancellation(a[0]))
	})
	route(http.MethodPost, `/server/(\d+)/cancellation`, func(m *mockRobot, _ *http.Request, a []string, form url.Values) (int, any) {
		if _, ok := m.get("server", a[0]); !ok {
			return mockErr(404, "SERVER_NOT_FOUND", "Server with id "+a[0]+" not found")
		}
		if c, ok := m.get("server_cancellation", a[0]); ok && c["cancelled"] == true {
			return mockErr(409, "CONFLICT", "The server is already cancelled")
		}
		date, ok := mockCancellationDate(form)
		if !ok {
			return mockInvalid("cancellation_date")
		}
		_, possible := m.get("reservation_possible", a[0])
		reserve, given, err := formBoolValue(form, "reserve_location")
		switch {
		case err != nil:
			return mockInvalid("reserve_location")
		case possible && !given:
			return mockInvalid("reserve_location")
		case !possible && given && reserve:
			return mockErr(409, "SERVER_CANCELLATION_RESERVE_LOCATION_FALSE_ONLY",
				"It is not possible to reserve the location. Remove parameter reserve_location or set value to 'false'")
		}
		var reason any
		if r := form.Get("cancellation_reason"); r != "" {
			reason = r
		}
		m.put("server_cancellation", a[0], map[string]any{"cancelled": true, "date": date, "reason": reason, "reserved": reserve})
		return 200, wrap("cancellation", m.serverCancellation(a[0]))
	})
	route(http.MethodDelete, `/server/(\d+)/cancellation`, func(m *mockRobot, _ *http.Request, a []string, _ url.Values) (int, any) {
		if _, ok := m.get("server", a[0]); !ok {
			return mockErr(404, "SERVER_NOT_FOUND", "Server with id "+a[0]+" not found")
		}
		if c, ok := m.get("server_cancellation", a[0]); !ok || c["cancelled"] != true {
			return mockErr(409, "CONFLICT", "The cancellation cannot be revoked")
		}
		m.del("server_cancellation", a[0])
		return 200, nil
	})

	// --- single IPs ---
	route(http.MethodGet, `/ip`, func(m *mockRobot, _ *http.Request, _ []string, form url.Values) (int, any) {
		serverIP := form.Get("server_ip")
		items := []map[string]any{}
		for _, ip := range m.list("ip", func(e map[string]any) bool { return serverIP == "" || e["server_ip"] == serverIP }) {
			c := copyMap(ip)
			delete(c, "gateway")
			delete(c, "mask")
			delete(c, "broadcast")
			items = append(items, c)
		}
		return wrapList("ip", items, "No IP addresses found")
	})
	route(http.MethodGet, `/ip/([^/]+)`, func(m *mockRobot, _ *http.Request, a []string, _ url.Values) (int, any) {
		ip, ok := m.get("ip", a[0])
		if !ok {
			return mockErr(404, "IP_NOT_FOUND", "No IP addresses found")
		}
		return 200, wrap("ip", copyMap(ip))
	})
	route(http.MethodPost, `/ip/([^/]+)`, func(m *mockRobot, _ *http.Request, a []string, form url.Values) (int, any) {
		ip, ok := m.get("ip", a[0])
		if !ok {
			return mockErr(404, "IP_NOT_FOUND", "No IP addresses found")
		}
		if code, body, bad := mockApplyTraffic(ip, form); bad {
			return code, body
		}
		return 200, wrap("ip", copyMap(ip))
	})

	// --- separate MAC of a single IP ---
	route(http.MethodGet, `/ip/([^/]+)/mac`, func(m *mockRobot, _ *http.Request, a []string, _ url.Values) (int, any) {
		ip, code, body := m.macIP(a[0])
		if ip == nil {
			return code, body
		}
		if ip["separate_mac"] == nil {
			return mockErr(404, "MAC_NOT_FOUND", "There is no separate MAC address set")
		}
		return 200, wrap("mac", map[string]any{"ip": a[0], "mac": ip["separate_mac"]})
	})
	route(http.MethodPut, `/ip/([^/]+)/mac`, func(m *mockRobot, _ *http.Request, a []string, _ url.Values) (int, any) {
		ip, code, body := m.macIP(a[0])
		if ip == nil {
			return code, body
		}
		if ip["separate_mac"] != nil {
			return mockErr(409, "MAC_ALREADY_SET", "There is already a separate MAC address set")
		}
		ip["separate_mac"] = "00:50:56:00:4c:1f"
		return 200, wrap("mac", map[string]any{"ip": a[0], "mac": ip["separate_mac"]})
	})
	route(http.MethodDelete, `/ip/([^/]+)/mac`, func(m *mockRobot, _ *http.Request, a []string, _ url.Values) (int, any) {
		ip, code, body := m.macIP(a[0])
		if ip == nil {
			return code, body
		}
		if ip["separate_mac"] == nil {
			return mockErr(409, "MAC_NOT_FOUND", "There is no separate MAC address set")
		}
		ip["separate_mac"] = nil
		return 200, wrap("mac", map[string]any{"ip": a[0], "mac": nil})
	})

	// --- IP cancellation ---
	route(http.MethodGet, `/ip/([^/]+)/cancellation`, func(m *mockRobot, _ *http.Request, a []string, _ url.Values) (int, any) {
		return m.addrCancellation("ip", a[0], nil, false)
	})
	route(http.MethodPost, `/ip/([^/]+)/cancellation`, func(m *mockRobot, _ *http.Request, a []string, form url.Values) (int, any) {
		return m.addrCancellation("ip", a[0], form, false)
	})
	route(http.MethodDelete, `/ip/([^/]+)/cancellation`, func(m *mockRobot, _ *http.Request, a []string, _ url.Values) (int, any) {
		return m.addrCancellation("ip", a[0], nil, true)
	})

	// --- subnets ---
	route(http.MethodGet, `/subnet`, func(m *mockRobot, _ *http.Request, _ []string, form url.Values) (int, any) {
		serverIP := form.Get("server_ip")
		return wrapList("subnet", m.list("subnet", func(e map[string]any) bool {
			return serverIP == "" || e["server_ip"] == serverIP
		}), "No subnets found")
	})
	route(http.MethodGet, `/subnet/([^/]+)`, func(m *mockRobot, _ *http.Request, a []string, _ url.Values) (int, any) {
		s, ok := m.get("subnet", a[0])
		if !ok {
			return mockErr(404, "SUBNET_NOT_FOUND", "Subnet not found")
		}
		return 200, wrap("subnet", copyMap(s))
	})
	route(http.MethodPost, `/subnet/([^/]+)`, func(m *mockRobot, _ *http.Request, a []string, form url.Values) (int, any) {
		s, ok := m.get("subnet", a[0])
		if !ok {
			return mockErr(404, "SUBNET_NOT_FOUND", "Subnet not found")
		}
		if code, body, bad := mockApplyTraffic(s, form); bad {
			return code, body
		}
		return 200, wrap("subnet", copyMap(s))
	})

	// --- MAC of a subnet ---
	route(http.MethodGet, `/subnet/([^/]+)/mac`, func(m *mockRobot, _ *http.Request, a []string, _ url.Values) (int, any) {
		return m.subnetMAC(a[0], nil, false)
	})
	route(http.MethodPut, `/subnet/([^/]+)/mac`, func(m *mockRobot, _ *http.Request, a []string, form url.Values) (int, any) {
		return m.subnetMAC(a[0], form, false)
	})
	route(http.MethodDelete, `/subnet/([^/]+)/mac`, func(m *mockRobot, _ *http.Request, a []string, _ url.Values) (int, any) {
		return m.subnetMAC(a[0], nil, true)
	})

	// --- subnet cancellation ---
	route(http.MethodGet, `/subnet/([^/]+)/cancellation`, func(m *mockRobot, _ *http.Request, a []string, _ url.Values) (int, any) {
		return m.addrCancellation("subnet", a[0], nil, false)
	})
	route(http.MethodPost, `/subnet/([^/]+)/cancellation`, func(m *mockRobot, _ *http.Request, a []string, form url.Values) (int, any) {
		return m.addrCancellation("subnet", a[0], form, false)
	})
	route(http.MethodDelete, `/subnet/([^/]+)/cancellation`, func(m *mockRobot, _ *http.Request, a []string, _ url.Values) (int, any) {
		return m.addrCancellation("subnet", a[0], nil, true)
	})

	// --- failover ---
	route(http.MethodGet, `/failover`, func(m *mockRobot, _ *http.Request, _ []string, _ url.Values) (int, any) {
		return wrapList("failover", m.list("failover", nil), "No failover IP addresses found")
	})
	route(http.MethodGet, `/failover/([^/]+)`, func(m *mockRobot, _ *http.Request, a []string, _ url.Values) (int, any) {
		f, ok := m.get("failover", a[0])
		if !ok {
			return mockErr(404, "NOT_FOUND", "Failover IP address not found")
		}
		return 200, wrap("failover", copyMap(f))
	})
	route(http.MethodPost, `/failover/([^/]+)`, func(m *mockRobot, _ *http.Request, a []string, form url.Values) (int, any) {
		f, ok := m.get("failover", a[0])
		if !ok {
			return mockErr(404, "NOT_FOUND", "Failover IP address not found")
		}
		if m.failoverLocked(a[0]) {
			return mockErr(409, "FAILOVER_LOCKED", "Switching the failover IP address is blocked due to another active request")
		}
		target := form.Get("active_server_ip")
		if target == "" {
			return mockInvalid("active_server_ip")
		}
		found := false
		for _, s := range m.list("server", nil) {
			if s["server_ip"] == target || s["server_ipv6_net"] == target {
				found = true
			}
		}
		if !found {
			return mockErr(404, "FAILOVER_NEW_SERVER_NOT_FOUND", "Destination server not found")
		}
		if f["active_server_ip"] == target {
			return mockErr(409, "FAILOVER_ALREADY_ROUTED", "The failover IP address is already routed to the selected server")
		}
		f["active_server_ip"] = target
		return 200, wrap("failover", copyMap(f))
	})
	route(http.MethodDelete, `/failover/([^/]+)`, func(m *mockRobot, _ *http.Request, a []string, _ url.Values) (int, any) {
		f, ok := m.get("failover", a[0])
		if !ok {
			return mockErr(404, "NOT_FOUND", "Failover IP address not found")
		}
		if m.failoverLocked(a[0]) {
			return mockErr(409, "FAILOVER_LOCKED", "Deleting the failover IP routing is blocked due to another active request")
		}
		f["active_server_ip"] = nil
		return 200, wrap("failover", copyMap(f))
	})
}

// mockCancellationDate reads cancellation_date; "now" becomes today's date.
func mockCancellationDate(form url.Values) (string, bool) {
	d := form.Get("cancellation_date")
	switch {
	case d == "now":
		return time.Now().UTC().Format("2006-01-02"), true
	case len(d) == 10:
		if _, err := time.Parse("2006-01-02", d); err == nil {
			return d, true
		}
	}
	return "", false
}

func (m *mockRobot) serverCancellation(n string) map[string]any {
	s, _ := m.get("server", n)
	_, possible := m.get("reservation_possible", n)
	out := map[string]any{
		"server_ip": s["server_ip"], "server_ipv6_net": s["server_ipv6_net"], "server_number": s["server_number"],
		"server_name": s["server_name"], "earliest_cancellation_date": mockEarliestCancellation,
		"cancelled": false, "reservation_possible": possible, "reserved": false,
		"cancellation_date": nil, "cancellation_reason": mockCancellationReasons,
	}
	if c, ok := m.get("server_cancellation", n); ok && c["cancelled"] == true {
		out["cancelled"] = true
		out["cancellation_date"] = c["date"]
		out["cancellation_reason"] = c["reason"]
		out["reserved"] = c["reserved"]
	}
	return out
}

// addrCancellation serves GET (form nil), POST (form set) and DELETE (revoke)
// of /ip/{ip}/cancellation and /subnet/{ip}/cancellation. The answer spells the
// date key "cancellation-date", as the documented examples do.
func (m *mockRobot) addrCancellation(kind, ip string, form url.Values, revoke bool) (int, any) {
	e, ok := m.get(kind, ip)
	notFound := "IP address not found"
	if kind == "subnet" {
		notFound = "Subnet not found"
	}
	if !ok {
		return mockErr(404, "IP_NOT_FOUND", notFound)
	}
	// A server's main IP and its main IPv6 subnet go with the server.
	if s, _ := m.get("server", mockKey(e["server_number"])); s != nil && (s["server_ip"] == ip || s["server_ipv6_net"] == ip) {
		return mockErr(409, "CONFLICT", "It is not possible to cancel this "+map[string]string{"ip": "IP", "subnet": "subnet"}[kind]+".")
	}
	table := kind + "_cancellation"
	c, cancelled := m.get(table, ip)
	switch {
	case revoke:
		if !cancelled {
			return mockErr(409, "CONFLICT", "The cancellation cannot be revoked because there is none.")
		}
		m.del(table, ip)
		c, cancelled = nil, false
	case form != nil:
		if cancelled {
			return mockErr(409, "CONFLICT", "The cancellation cannot be done because it is already cancelled.")
		}
		date, ok := mockCancellationDate(form)
		if !ok {
			return mockInvalid("cancellation_date")
		}
		c = map[string]any{"date": date}
		m.put(table, ip, c)
		cancelled = true
	}
	out := map[string]any{
		"ip": ip, "server_number": e["server_number"], "earliest_cancellation_date": mockEarliestCancellation,
		"cancelled": cancelled, "cancellation-date": nil,
	}
	if cancelled {
		out["cancellation-date"] = c["date"]
	}
	if kind == "subnet" {
		out["mask"] = mockKey(e["mask"])
	}
	return 200, wrap("cancellation", out)
}

// mockApplyTraffic validates and applies the four traffic fields of an IP or a
// subnet. bad reports an error answer.
func mockApplyTraffic(e map[string]any, form url.Values) (int, any, bool) {
	if len(form) == 0 {
		code, body := mockInvalid("traffic_warnings", "traffic_hourly", "traffic_daily", "traffic_monthly")
		return code, body, true
	}
	if key, ok := applyForm(e, form, "traffic_warnings", "traffic_hourly", "traffic_daily", "traffic_monthly"); !ok {
		return 400, map[string]any{"error": map[string]any{
			"status": 400, "code": "INVALID_INPUT", "message": "invalid input", "missing": nil, "invalid": []string{key},
		}}, true
	}
	return 0, nil, false
}

// macIP finds a single IP whose MAC may be separated; a server's main IP may not.
func (m *mockRobot) macIP(ip string) (map[string]any, int, any) {
	e, ok := m.get("ip", ip)
	if !ok {
		code, body := mockErr(404, "IP_NOT_FOUND", "IP address not found")
		return nil, code, body
	}
	if e["server_ip"] == ip {
		code, body := mockErr(404, "MAC_NOT_AVAILABLE", "For this IP address it is not possible to set a separate MAC address")
		return nil, code, body
	}
	return e, 0, nil
}

// subnetMAC serves the MAC endpoints of an IPv6 subnet: GET (form nil), PUT
// (form set) and DELETE (reset to the main IP's MAC).
func (m *mockRobot) subnetMAC(ip string, form url.Values, reset bool) (int, any) {
	s, ok := m.get("subnet", ip)
	if !ok {
		return mockErr(404, "SUBNET_NOT_FOUND", "Subnet not found")
	}
	if !strings.Contains(ip, ":") {
		return mockErr(404, "MAC_NOT_AVAILABLE", "For this IP address it is not possible to set a separate MAC address")
	}
	possible := map[string]any{s["server_ip"].(string): mockMainMAC}
	for _, other := range m.list("ip", func(e map[string]any) bool { return e["server_ip"] == s["server_ip"] && e["ip"] != s["server_ip"] }) {
		possible[other["ip"].(string)] = mockSecondMAC
	}
	cur := mockMainMAC
	if e, ok := m.get("subnet_mac", ip); ok {
		cur = e["mac"].(string)
	}
	switch {
	case reset:
		m.del("subnet_mac", ip)
		cur = mockMainMAC
	case form != nil:
		mac := form.Get("mac")
		valid := false
		for _, v := range possible {
			if v == mac {
				valid = true
			}
		}
		if !valid {
			return 400, map[string]any{"error": map[string]any{
				"status": 400, "code": "INVALID_INPUT", "message": "invalid input", "missing": nil, "invalid": []string{"mac"},
			}}
		}
		m.put("subnet_mac", ip, map[string]any{"mac": mac})
		cur = mac
	}
	return 200, wrap("mac", map[string]any{"ip": ip, "mask": mockKey(s["mask"]), "mac": cur, "possible_mac": possible})
}

// failoverLocked consumes one of the FAILOVER_LOCKED answers a test queued in
// the "failover_lock" table ({"n": count}).
func (m *mockRobot) failoverLocked(ip string) bool {
	l, ok := m.get("failover_lock", ip)
	if !ok || l["n"].(int) <= 0 {
		return false
	}
	l["n"] = l["n"].(int) - 1
	return true
}

// mockKey renders a numeric table value as the text tables are keyed by.
func mockKey(v any) string {
	switch n := v.(type) {
	case int64:
		return strconv.FormatInt(n, 10)
	case string:
		return n
	}
	return ""
}
