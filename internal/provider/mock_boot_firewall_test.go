package provider

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
)

// Boot configurations, resets, Wake on LAN and the firewall, per the
// documentation:
//   - a boot configuration lists its choices as arrays while inactive and the
//     chosen strings while active; the password is in the activation answer and
//     in reads while active; only one configuration can be active per server;
//   - a firewall change answers "in process" and stays so for a couple of reads;
//     a write in that window is a 409 FIREWALL_IN_PROCESS;
//   - the single-template answers carry no name, the list does.

// mockBootOptions are the choices per configuration kind: the main choice
// (os/dist) and the languages.
var mockBootOptions = map[string]struct {
	choiceKey string
	choices   []string
	langs     []string
}{
	"rescue":  {"os", []string{"linux", "vkvm"}, nil},
	"linux":   {"dist", []string{"Debian 12 base", "Ubuntu 24.04 LTS base"}, []string{"en", "de"}},
	"vnc":     {"dist", []string{"Fedora-40", "openSUSE-15.6"}, []string{"en_US", "de_DE"}},
	"windows": {"os", []string{"Windows Server 2022 Standard Edition"}, []string{"en", "de"}},
}

var mockFirewallRuleKey = regexp.MustCompile(`^rules\[(input|output)\]\[(\d+)\]\[(\w+)\]$`)

func init() {
	seed(func(m *mockRobot) {
		m.put("reset", fxServer1, map[string]any{"types": []any{"sw", "hw", "man"}, "operating_status": "not supported"})
		m.put("reset", fxServer2, map[string]any{"types": []any{"power", "power_long", "hw", "man"}, "operating_status": "running"})
		for _, n := range []string{fxServer1, fxServer2} {
			s, _ := m.get("server", n)
			m.put("firewall", n, map[string]any{
				"server_ip": s["server_ip"], "server_number": s["server_number"], "status": "disabled",
				"filter_ipv6": false, "whitelist_hos": true, "port": "main",
				"input": []map[string]any{}, "output": []map[string]any{}, "pending": 0, "target": "disabled",
			})
		}
	})

	// --- boot ---
	route(http.MethodGet, `/boot/(\d+)`, func(m *mockRobot, _ *http.Request, a []string, _ url.Values) (int, any) {
		if _, ok := m.get("server", a[0]); !ok {
			return mockErr(404, "SERVER_NOT_FOUND", "Server with id "+a[0]+" not found")
		}
		out := map[string]any{}
		for kind := range mockBootOptions {
			if c, ok := m.bootView(a[0], kind); ok {
				out[kind] = c
			} else {
				out[kind] = nil
			}
		}
		return 200, wrap("boot", out)
	})
	route(http.MethodGet, `/boot/(\d+)/(rescue|linux|vnc|windows)`, func(m *mockRobot, _ *http.Request, a []string, _ url.Values) (int, any) {
		if _, ok := m.get("server", a[0]); !ok {
			return mockErr(404, "SERVER_NOT_FOUND", "Server with id "+a[0]+" not found")
		}
		c, ok := m.bootView(a[0], a[1])
		if !ok {
			return mockErr(404, "BOOT_NOT_AVAILABLE", "No boot configuration available for this server")
		}
		return 200, wrap(a[1], c)
	})
	route(http.MethodPost, `/boot/(\d+)/(rescue|linux|vnc|windows)`, func(m *mockRobot, _ *http.Request, a []string, form url.Values) (int, any) {
		n, kind := a[0], a[1]
		if _, ok := m.get("server", n); !ok {
			return mockErr(404, "SERVER_NOT_FOUND", "Server with id "+n+" not found")
		}
		if _, ok := m.bootView(n, kind); !ok {
			return mockErr(404, "BOOT_NOT_AVAILABLE", "No boot configuration available for this server")
		}
		opts := mockBootOptions[kind]
		if !slices.Contains(opts.choices, form.Get(opts.choiceKey)) {
			return mockInvalid(opts.choiceKey)
		}
		if opts.langs != nil && !slices.Contains(opts.langs, form.Get("lang")) {
			return mockInvalid("lang")
		}
		if b, ok := m.get("boot", n); ok && b["active"] != "" {
			return mockErr(409, "BOOT_ALREADY_ENABLED", "A boot configuration is already active: "+b["active"].(string))
		}
		m.put("boot", n, map[string]any{
			"active": kind, "choice": form.Get(opts.choiceKey), "lang": form.Get("lang"),
			"password": fmt.Sprintf("mockPass%s%d", n, m.newID()), "keys": form["authorized_key[]"],
		})
		c, _ := m.bootView(n, kind)
		return 200, wrap(kind, c)
	})
	route(http.MethodDelete, `/boot/(\d+)/(rescue|linux|vnc|windows)`, func(m *mockRobot, _ *http.Request, a []string, _ url.Values) (int, any) {
		n, kind := a[0], a[1]
		if _, ok := m.bootView(n, kind); !ok {
			return mockErr(404, "BOOT_NOT_AVAILABLE", "No boot configuration available for this server")
		}
		if b, ok := m.get("boot", n); ok && b["active"] == kind {
			m.del("boot", n)
		}
		c, _ := m.bootView(n, kind)
		return 200, wrap(kind, c)
	})

	// --- reset & Wake on LAN ---
	route(http.MethodGet, `/reset`, func(m *mockRobot, _ *http.Request, _ []string, _ url.Values) (int, any) {
		items := []map[string]any{}
		for _, s := range m.list("server", nil) {
			n := strconv.FormatInt(s["server_number"].(int64), 10)
			if r, ok := m.get("reset", n); ok {
				items = append(items, map[string]any{
					"server_ip": s["server_ip"], "server_ipv6_net": s["server_ipv6_net"],
					"server_number": s["server_number"], "type": r["types"],
				})
			}
		}
		return wrapList("reset", items, "No servers with reset option found")
	})
	route(http.MethodGet, `/reset/(\d+)`, func(m *mockRobot, _ *http.Request, a []string, _ url.Values) (int, any) {
		s, r, status, body := m.resetTarget(a[0])
		if s == nil {
			return status, body
		}
		return 200, wrap("reset", map[string]any{
			"server_ip": s["server_ip"], "server_ipv6_net": s["server_ipv6_net"], "server_number": s["server_number"],
			"type": r["types"], "operating_status": r["operating_status"],
		})
	})
	route(http.MethodPost, `/reset/(\d+)`, func(m *mockRobot, _ *http.Request, a []string, form url.Values) (int, any) {
		s, r, status, body := m.resetTarget(a[0])
		if s == nil {
			return status, body
		}
		if !slices.Contains(r["types"].([]any), any(form.Get("type"))) {
			return mockInvalid("type")
		}
		return 200, wrap("reset", map[string]any{
			"server_ip": s["server_ip"], "server_ipv6_net": s["server_ipv6_net"], "type": form.Get("type"),
		})
	})
	wol := func(m *mockRobot, _ *http.Request, a []string, _ url.Values) (int, any) {
		s, ok := m.get("server", a[0])
		if !ok {
			return mockErr(404, "SERVER_NOT_FOUND", "Server not found")
		}
		if s["wol"] != true {
			return mockErr(404, "WOL_NOT_AVAILABLE", "Wake On LAN is not available on this server")
		}
		return 200, wrap("wol", map[string]any{
			"server_ip": s["server_ip"], "server_ipv6_net": s["server_ipv6_net"], "server_number": s["server_number"],
		})
	}
	route(http.MethodGet, `/wol/(\d+)`, wol)
	route(http.MethodPost, `/wol/(\d+)`, wol)

	// --- firewall templates ---
	route(http.MethodGet, `/firewall/template`, func(m *mockRobot, _ *http.Request, _ []string, _ url.Values) (int, any) {
		items := []map[string]any{}
		for _, t := range m.listByNumber("fwtemplate") {
			items = append(items, map[string]any{
				"id": t["id"], "name": t["name"], "filter_ipv6": t["filter_ipv6"],
				"whitelist_hos": t["whitelist_hos"], "is_default": t["is_default"],
			})
		}
		return wrapList("firewall_template", items, "No firewall templates found")
	})
	route(http.MethodPost, `/firewall/template`, func(m *mockRobot, _ *http.Request, _ []string, form url.Values) (int, any) {
		if form.Get("name") == "" {
			return mockInvalid("name")
		}
		id := m.newID()
		t := map[string]any{"id": id, "name": "", "filter_ipv6": false, "whitelist_hos": false, "is_default": false}
		if code, body, ok := m.applyTemplateForm(t, form); !ok {
			return code, body
		}
		m.put("fwtemplate", strconv.FormatInt(id, 10), t)
		return 201, wrap("firewall_template", mockTemplateView(t))
	})
	route(http.MethodGet, `/firewall/template/(\d+)`, func(m *mockRobot, _ *http.Request, a []string, _ url.Values) (int, any) {
		t, ok := m.get("fwtemplate", a[0])
		if !ok {
			return mockErr(404, "NOT_FOUND", "Firewall template not found")
		}
		return 200, wrap("firewall_template", mockTemplateView(t))
	})
	route(http.MethodPost, `/firewall/template/(\d+)`, func(m *mockRobot, _ *http.Request, a []string, form url.Values) (int, any) {
		t, ok := m.get("fwtemplate", a[0])
		if !ok {
			return mockErr(404, "NOT_FOUND", "Firewall template not found")
		}
		if code, body, ok := m.applyTemplateForm(t, form); !ok {
			return code, body
		}
		return 200, wrap("firewall_template", mockTemplateView(t))
	})
	route(http.MethodDelete, `/firewall/template/(\d+)`, func(m *mockRobot, _ *http.Request, a []string, _ url.Values) (int, any) {
		if _, ok := m.get("fwtemplate", a[0]); !ok {
			return mockErr(404, "NOT_FOUND", "Firewall template not found")
		}
		m.del("fwtemplate", a[0])
		return 200, nil
	})

	// --- firewall ---
	route(http.MethodGet, `/firewall/(\d+)`, func(m *mockRobot, _ *http.Request, a []string, _ url.Values) (int, any) {
		f, ok := m.get("firewall", a[0])
		if !ok {
			return mockErr(404, "SERVER_NOT_FOUND", "Server with ID "+a[0]+" not found")
		}
		if p := f["pending"].(int); p > 0 {
			f["pending"] = p - 1
			if p == 1 {
				f["status"] = f["target"]
			}
		}
		return 200, wrap("firewall", mockFirewallView(f, false))
	})
	route(http.MethodPost, `/firewall/(\d+)`, func(m *mockRobot, _ *http.Request, a []string, form url.Values) (int, any) {
		f, ok := m.get("firewall", a[0])
		if !ok {
			return mockErr(404, "SERVER_NOT_FOUND", "Server with ID "+a[0]+" not found")
		}
		if f["pending"].(int) > 0 {
			return mockErr(409, "FIREWALL_IN_PROCESS", "The firewall cannot be updated because a update is currently running")
		}
		status := form.Get("status")
		if status != "active" && status != "disabled" {
			return mockInvalid("status")
		}
		input, output, ok := mockParseRules(form)
		if !ok {
			return mockInvalid("rules")
		}
		if v, set, err := formBoolValue(form, "filter_ipv6"); err != nil {
			return mockInvalid("filter_ipv6")
		} else if set {
			f["filter_ipv6"] = v
		}
		if id := form.Get("template_id"); id != "" {
			if _, set := form["whitelist_hos"]; set || len(input)+len(output) > 0 {
				return mockErr(400, "INVALID_INPUT", "template_id is not possible in combination with whitelist_hos and rules")
			}
			t, ok := m.get("fwtemplate", id)
			if !ok {
				return mockErr(404, "FIREWALL_TEMPLATE_NOT_FOUND", "Template with ID "+id+" not found")
			}
			f["whitelist_hos"], f["filter_ipv6"] = t["whitelist_hos"], t["filter_ipv6"]
			input, output = t["input"].([]map[string]any), t["output"].([]map[string]any)
		} else {
			v, set, err := formBoolValue(form, "whitelist_hos")
			if err != nil || !set {
				return mockInvalid("whitelist_hos")
			}
			f["whitelist_hos"] = v
		}
		f["input"], f["output"] = input, output
		f["status"], f["target"], f["pending"] = "in process", status, 2
		return 200, wrap("firewall", mockFirewallView(f, false))
	})
	route(http.MethodDelete, `/firewall/(\d+)`, func(m *mockRobot, _ *http.Request, a []string, _ url.Values) (int, any) {
		f, ok := m.get("firewall", a[0])
		if !ok {
			return mockErr(404, "SERVER_NOT_FOUND", "Server with ID "+a[0]+" not found")
		}
		if f["pending"].(int) > 0 {
			return mockErr(409, "FIREWALL_IN_PROCESS", "The firewall cannot be updated because a update is currently running")
		}
		f["input"], f["output"], f["whitelist_hos"] = []map[string]any{}, []map[string]any{}, true
		f["status"], f["target"], f["pending"] = "in process", "disabled", 2
		return 200, wrap("firewall", mockFirewallView(f, true))
	})
}

// bootView renders one boot configuration of a server the way the API does,
// or false when Robot does not offer it (Windows on a server without it).
func (m *mockRobot) bootView(n, kind string) (map[string]any, bool) {
	s, ok := m.get("server", n)
	if !ok || (kind == "windows" && s["windows"] != true) {
		return nil, false
	}
	opts := mockBootOptions[kind]
	c := map[string]any{
		"server_ip": s["server_ip"], "server_ipv6_net": s["server_ipv6_net"], "server_number": s["server_number"],
		"active": false, "password": nil,
	}
	anyList := func(v []string) []any {
		out := make([]any, len(v))
		for i, x := range v {
			out[i] = x
		}
		return out
	}
	c[opts.choiceKey] = anyList(opts.choices)
	if opts.langs != nil {
		c["lang"] = anyList(opts.langs)
	}
	if kind == "rescue" || kind == "linux" {
		c["authorized_key"], c["host_key"] = []any{}, []any{}
	}
	if b, ok := m.get("boot", n); ok && b["active"] == kind {
		c["active"], c["password"], c[opts.choiceKey] = true, b["password"], b["choice"]
		if opts.langs != nil {
			c["lang"] = b["lang"]
		}
		if kind == "rescue" || kind == "linux" {
			keys := []any{}
			for _, fp := range b["keys"].([]string) {
				keys = append(keys, map[string]any{"key": map[string]any{"name": "k", "fingerprint": fp, "type": "ED25519", "size": 256}})
			}
			c["authorized_key"] = keys
			c["host_key"] = []any{map[string]any{"key": map[string]any{"fingerprint": "c1:e4:08:73:dd:f7:e9:d1:94:ab:e9:0f:28:b2:d2:ed", "type": "ED25519", "size": 256}}}
		}
	}
	return c, true
}

// bootDeactivate simulates the server booting its pending configuration.
func (m *mockRobot) bootDeactivate(n string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.del("boot", n)
}

func (m *mockRobot) resetTarget(n string) (map[string]any, map[string]any, int, any) {
	s, ok := m.get("server", n)
	if !ok {
		status, body := mockErr(404, "SERVER_NOT_FOUND", "Server with id "+n+" not found")
		return nil, nil, status, body
	}
	r, ok := m.get("reset", n)
	if !ok {
		status, body := mockErr(404, "RESET_NOT_AVAILABLE", "The server has no reset option")
		return nil, nil, status, body
	}
	return s, r, 0, nil
}

// listByNumber lists a table whose ids are numbers in numeric order.
func (m *mockRobot) listByNumber(kind string) []map[string]any {
	out := m.list(kind, nil)
	sort.Slice(out, func(i, j int) bool { return out[i]["id"].(int64) < out[j]["id"].(int64) })
	return out
}

// mockParseRules reads rules[<dir>][<i>][<field>] into ordered rule lists; a
// rule without a valid action is invalid input.
func mockParseRules(form url.Values) ([]map[string]any, []map[string]any, bool) {
	byDir := map[string]map[int]map[string]any{"input": {}, "output": {}}
	for k, v := range form {
		g := mockFirewallRuleKey.FindStringSubmatch(k)
		if g == nil {
			continue
		}
		i, _ := strconv.Atoi(g[2])
		if byDir[g[1]][i] == nil {
			byDir[g[1]][i] = map[string]any{}
		}
		byDir[g[1]][i][g[3]] = v[0]
	}
	ordered := func(dir string) ([]map[string]any, bool) {
		idx := make([]int, 0, len(byDir[dir]))
		for i := range byDir[dir] {
			idx = append(idx, i)
		}
		sort.Ints(idx)
		out := []map[string]any{}
		for _, i := range idx {
			r := byDir[dir][i]
			if a := r["action"]; a != "accept" && a != "discard" {
				return nil, false
			}
			full := map[string]any{}
			for _, f := range []string{"ip_version", "name", "dst_ip", "src_ip", "dst_port", "src_port", "protocol", "tcp_flags", "action"} {
				full[f] = r[f] // missing fields render as null
			}
			out = append(out, full)
		}
		return out, true
	}
	in, ok1 := ordered("input")
	out, ok2 := ordered("output")
	return in, out, ok1 && ok2
}

// mockFirewallView renders a firewall. Without rules it answers "rules": {}
// after a DELETE and [] otherwise — both shapes occur in PHP answers.
func mockFirewallView(f map[string]any, deleted bool) map[string]any {
	out := map[string]any{}
	for _, k := range []string{"server_ip", "server_number", "status", "filter_ipv6", "whitelist_hos", "port"} {
		out[k] = f[k]
	}
	in, o := f["input"].([]map[string]any), f["output"].([]map[string]any)
	switch {
	case len(in)+len(o) > 0:
		out["rules"] = map[string]any{"input": in, "output": o}
	case deleted:
		out["rules"] = map[string]any{}
	default:
		out["rules"] = []any{}
	}
	return out
}

func (m *mockRobot) applyTemplateForm(t map[string]any, form url.Values) (int, any, bool) {
	if k, ok := applyForm(t, form, "name", "filter_ipv6", "whitelist_hos", "is_default"); !ok {
		status, body := mockInvalid(k)
		return status, body, false
	}
	in, out, ok := mockParseRules(form)
	if !ok {
		status, body := mockInvalid("rules")
		return status, body, false
	}
	t["input"], t["output"] = in, out
	return 0, nil, true
}

// mockTemplateView renders a single template the way the documented answers
// do: with its rules, but without the name.
func mockTemplateView(t map[string]any) map[string]any {
	return map[string]any{
		"id": t["id"], "filter_ipv6": t["filter_ipv6"], "whitelist_hos": t["whitelist_hos"], "is_default": t["is_default"],
		"rules": map[string]any{"input": t["input"], "output": t["output"]},
	}
}
