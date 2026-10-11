package provider

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// vSwitch and Storage Box endpoints of the mock. vSwitches answer bare
// objects; a server that joins is "in process" for one read, then "ready".
// Storage Box rows live in "storagebox" (fixture 123456), snapshots in
// "sbsnapshot" ("<box>/<name>"), the plans in "sbplan", sub-accounts in
// "sbsub" ("<box>/<username>").

var mockDateRe = regexp.MustCompile(`^(now|\d{4}-\d{2}-\d{2})$`)

func init() {
	seed(func(m *mockRobot) {
		m.put("sbplan", fxStorageBox, map[string]any{
			"status": "disabled", "minute": nil, "hour": nil, "day_of_week": nil,
			"day_of_month": nil, "month": nil, "max_snapshots": int64(0),
		})
	})

	// --- vSwitch ---
	route(http.MethodGet, `/vswitch`, func(m *mockRobot, _ *http.Request, _ []string, _ url.Values) (int, any) {
		out := []map[string]any{}
		for _, v := range m.list("vswitch", nil) {
			out = append(out, map[string]any{"id": v["id"], "name": v["name"], "vlan": v["vlan"], "cancelled": v["cancelled"]})
		}
		return 200, out
	})
	route(http.MethodPost, `/vswitch`, func(m *mockRobot, _ *http.Request, _ []string, form url.Values) (int, any) {
		if form.Get("name") == "" || form.Get("vlan") == "" {
			return mockInvalid("name", "vlan")
		}
		vlan, status, body := m.vswitchVLAN(form, 0)
		if status != 0 {
			return status, body
		}
		id := m.newID()
		v := map[string]any{
			"id": id, "name": form.Get("name"), "vlan": vlan, "cancelled": false,
			"server": []map[string]any{}, "subnet": []map[string]any{}, "cloud_network": []map[string]any{},
		}
		m.put("vswitch", strconv.FormatInt(id, 10), v)
		return 201, m.vswitchView(v)
	})
	route(http.MethodGet, `/vswitch/(\d+)`, func(m *mockRobot, _ *http.Request, a []string, _ url.Values) (int, any) {
		v, ok := m.get("vswitch", a[0])
		if !ok {
			return mockErr(404, "NOT_FOUND", "vSwitch not found")
		}
		out := m.vswitchView(v)
		// A joining server finishes after one read.
		for _, s := range v["server"].([]map[string]any) {
			if s["status"] == "in process" {
				s["status"] = "ready"
			}
		}
		return 200, out
	})
	route(http.MethodPost, `/vswitch/(\d+)`, func(m *mockRobot, _ *http.Request, a []string, form url.Values) (int, any) {
		v, ok := m.get("vswitch", a[0])
		if !ok {
			return mockErr(404, "NOT_FOUND", "vSwitch not found")
		}
		if form.Has("vlan") {
			vlan, status, body := m.vswitchVLAN(form, v["id"].(int64))
			if status != 0 {
				return status, body
			}
			v["vlan"] = vlan
		}
		if form.Has("name") {
			v["name"] = form.Get("name")
		}
		return 200, nil
	})
	route(http.MethodDelete, `/vswitch/(\d+)`, func(m *mockRobot, _ *http.Request, a []string, form url.Values) (int, any) {
		v, ok := m.get("vswitch", a[0])
		if !ok {
			return mockErr(404, "NOT_FOUND", "vSwitch not found")
		}
		if !mockDateRe.MatchString(form.Get("cancellation_date")) {
			return mockInvalid("cancellation_date")
		}
		if v["cancelled"] == true {
			return mockErr(409, "CONFLICT", "The vSwitch is already cancelled")
		}
		v["cancelled"] = true
		return 200, nil
	})
	vswitchServers := func(add bool) mockHandler {
		return func(m *mockRobot, _ *http.Request, a []string, form url.Values) (int, any) {
			v, ok := m.get("vswitch", a[0])
			if !ok {
				return mockErr(404, "NOT_FOUND", "vSwitch not found")
			}
			ids := form["server[]"]
			if len(ids) == 0 {
				return mockInvalid("server")
			}
			servers := v["server"].([]map[string]any)
			for _, s := range servers {
				if s["status"] == "in process" {
					return mockErr(409, "VSWITCH_IN_PROCESS", "The vSwitch cannot be updated because a update is currently running")
				}
			}
			for _, id := range ids {
				srv := m.serverByIdentifier(id)
				if srv == nil {
					return mockErr(404, "SERVER_NOT_FOUND", "A submitted server is not found")
				}
				number := srv["server_number"].(int64)
				idx := -1
				for i, s := range servers {
					if s["server_number"] == number {
						idx = i
					}
				}
				switch {
				case add && idx < 0:
					servers = append(servers, map[string]any{
						"server_ip": srv["server_ip"], "server_ipv6_net": srv["server_ipv6_net"],
						"server_number": number, "status": "in process",
					})
				case !add && idx >= 0:
					servers = append(servers[:idx], servers[idx+1:]...)
				}
			}
			v["server"] = servers
			return 201, nil
		}
	}
	route(http.MethodPost, `/vswitch/(\d+)/server`, vswitchServers(true))
	route(http.MethodDelete, `/vswitch/(\d+)/server`, vswitchServers(false))

	// --- Storage Box ---
	route(http.MethodGet, `/storagebox`, func(m *mockRobot, _ *http.Request, _ []string, form url.Values) (int, any) {
		linked := form.Get("linked_server")
		items := []map[string]any{}
		for _, b := range m.list("storagebox", func(b map[string]any) bool {
			return linked == "" || fmt.Sprint(b["linked_server"]) == linked
		}) {
			item := map[string]any{}
			for _, k := range []string{"id", "login", "name", "product", "cancelled", "locked", "location", "linked_server", "paid_until"} {
				item[k] = b[k]
			}
			items = append(items, item)
		}
		if len(items) == 0 {
			return mockErr(404, "STORAGEBOX_NOT_FOUND", "No Storage Boxes found")
		}
		return wrapList("storagebox", items, "")
	})
	route(http.MethodGet, `/storagebox/(\d+)`, func(m *mockRobot, _ *http.Request, a []string, _ url.Values) (int, any) {
		b, ok := m.get("storagebox", a[0])
		if !ok {
			return mockStorageBoxNotFound(a[0])
		}
		return 200, wrap("storagebox", copyMap(b))
	})
	route(http.MethodPost, `/storagebox/(\d+)`, func(m *mockRobot, _ *http.Request, a []string, form url.Values) (int, any) {
		b, ok := m.get("storagebox", a[0])
		if !ok {
			return mockStorageBoxNotFound(a[0])
		}
		if form.Has("storagebox_name") {
			b["name"] = form.Get("storagebox_name")
		}
		if k, ok := applyForm(b, form, "samba", "webdav", "ssh", "external_reachability", "zfs"); !ok {
			return mockBadValue(k)
		}
		return 200, wrap("storagebox", copyMap(b))
	})
	route(http.MethodPost, `/storagebox/(\d+)/password`, func(m *mockRobot, _ *http.Request, a []string, form url.Values) (int, any) {
		b, ok := m.get("storagebox", a[0])
		if !ok {
			return mockStorageBoxNotFound(a[0])
		}
		pw, status, body := mockStorageBoxPassword(form)
		if status != 0 {
			return status, body
		}
		b["_password"] = pw
		return 200, map[string]any{"password": pw}
	})

	// snapshots
	route(http.MethodGet, `/storagebox/(\d+)/snapshot`, func(m *mockRobot, _ *http.Request, a []string, _ url.Values) (int, any) {
		if _, ok := m.get("storagebox", a[0]); !ok {
			return mockStorageBoxNotFound(a[0])
		}
		out := []map[string]any{}
		for _, s := range m.list("sbsnapshot", func(s map[string]any) bool { return s["_box"] == a[0] }) {
			out = append(out, wrap("snapshot", mockPublic(s)))
		}
		// An empty snapshot list is [], not a 404: only the box is looked up.
		return 200, out
	})
	route(http.MethodPost, `/storagebox/(\d+)/snapshot`, func(m *mockRobot, _ *http.Request, a []string, _ url.Values) (int, any) {
		b, ok := m.get("storagebox", a[0])
		if !ok {
			return mockStorageBoxNotFound(a[0])
		}
		n := m.newID()
		name := fmt.Sprintf("2026-10-11T12-%02d-%02d", (n/60)%60, n%60)
		ts := fmt.Sprintf("2026-10-11T12:%02d:%02d+00:00", (n/60)%60, n%60)
		s := map[string]any{
			"_box": a[0], "name": name, "timestamp": ts, "size": int64(0),
			"filesystem_size": b["disk_usage_data"], "automatic": false, "comment": nil,
		}
		m.put("sbsnapshot", a[0]+"/"+name, s)
		return 201, wrap("snapshot", map[string]any{"name": name, "timestamp": ts, "size": int64(0)})
	})
	route(http.MethodDelete, `/storagebox/(\d+)/snapshot/([^/]+)`, func(m *mockRobot, _ *http.Request, a []string, _ url.Values) (int, any) {
		if _, ok := m.get("storagebox", a[0]); !ok {
			return mockStorageBoxNotFound(a[0])
		}
		if _, ok := m.get("sbsnapshot", a[0]+"/"+a[1]); !ok {
			return mockErr(404, "SNAPSHOT_NOT_FOUND", "Snapshot with name "+a[1]+" not found")
		}
		m.del("sbsnapshot", a[0]+"/"+a[1])
		return 200, nil
	})
	route(http.MethodPost, `/storagebox/(\d+)/snapshot/([^/]+)/comment`, func(m *mockRobot, _ *http.Request, a []string, form url.Values) (int, any) {
		if _, ok := m.get("storagebox", a[0]); !ok {
			return mockStorageBoxNotFound(a[0])
		}
		s, ok := m.get("sbsnapshot", a[0]+"/"+a[1])
		if !ok {
			return mockErr(404, "SNAPSHOT_NOT_FOUND", "Snapshot with name "+a[1]+" not found")
		}
		if !form.Has("comment") {
			return mockInvalid("comment")
		}
		s["comment"] = form.Get("comment")
		return 200, nil
	})

	// snapshot plan
	route(http.MethodGet, `/storagebox/(\d+)/snapshotplan`, func(m *mockRobot, _ *http.Request, a []string, _ url.Values) (int, any) {
		p, ok := m.get("sbplan", a[0])
		if !ok {
			return mockStorageBoxNotFound(a[0])
		}
		return 200, []map[string]any{wrap("snapshotplan", copyMap(p))}
	})
	route(http.MethodPost, `/storagebox/(\d+)/snapshotplan`, func(m *mockRobot, _ *http.Request, a []string, form url.Values) (int, any) {
		p, ok := m.get("sbplan", a[0])
		if !ok {
			return mockStorageBoxNotFound(a[0])
		}
		status := form.Get("status")
		schedule := []string{"minute", "hour", "day_of_week", "day_of_month", "month"}
		switch status {
		case "enabled":
			if !form.Has("minute") || !form.Has("hour") {
				return mockInvalid("minute", "hour")
			}
			for _, k := range schedule {
				p[k] = nil
				if form.Has(k) {
					n, _, err := formIntValue(form, k)
					if err != nil {
						return mockBadValue(k)
					}
					p[k] = n
				}
			}
		case "disabled":
			for _, k := range schedule {
				p[k] = nil
			}
		default:
			return mockBadValue("status")
		}
		p["status"] = status
		if form.Has("max_snapshots") {
			n, _, err := formIntValue(form, "max_snapshots")
			if err != nil {
				return mockBadValue("max_snapshots")
			}
			p["max_snapshots"] = n
		}
		return 200, []map[string]any{wrap("snapshotplan", copyMap(p))}
	})

	// sub-accounts
	route(http.MethodGet, `/storagebox/(\d+)/subaccount`, func(m *mockRobot, _ *http.Request, a []string, _ url.Values) (int, any) {
		if _, ok := m.get("storagebox", a[0]); !ok {
			return mockStorageBoxNotFound(a[0])
		}
		out := []map[string]any{}
		for _, s := range m.list("sbsub", func(s map[string]any) bool { return s["_box"] == a[0] }) {
			out = append(out, wrap("subaccount", mockPublic(s)))
		}
		return 200, out
	})
	route(http.MethodPost, `/storagebox/(\d+)/subaccount`, func(m *mockRobot, _ *http.Request, a []string, form url.Values) (int, any) {
		b, ok := m.get("storagebox", a[0])
		if !ok {
			return mockStorageBoxNotFound(a[0])
		}
		if form.Get("homedirectory") == "" {
			return mockInvalid("homedirectory")
		}
		pw, status, body := mockStorageBoxPassword(form)
		if status != 0 {
			return status, body
		}
		login := b["login"].(string)
		username := ""
		for i := 1; ; i++ {
			username = fmt.Sprintf("%s-sub%d", login, i)
			if _, taken := m.get("sbsub", a[0]+"/"+username); !taken {
				break
			}
		}
		s := map[string]any{
			"_box": a[0], "_password": pw, "username": username, "accountid": login,
			"server": username + ".your-storagebox.de", "homedirectory": form.Get("homedirectory"),
			"samba": false, "ssh": false, "external_reachability": false, "webdav": false, "readonly": false,
			"createtime": "2026-10-11 12:00:00", "comment": "",
		}
		if k, ok := applyForm(s, form, "samba", "ssh", "external_reachability", "webdav", "readonly", "comment"); !ok {
			return mockBadValue(k)
		}
		m.put("sbsub", a[0]+"/"+username, s)
		return 201, wrap("subaccount", map[string]any{
			"username": username, "password": pw, "accountid": login,
			"server": s["server"], "homedirectory": s["homedirectory"],
		})
	})
	route(http.MethodPut, `/storagebox/(\d+)/subaccount/([^/]+)`, func(m *mockRobot, _ *http.Request, a []string, form url.Values) (int, any) {
		if _, ok := m.get("storagebox", a[0]); !ok {
			return mockStorageBoxNotFound(a[0])
		}
		s, ok := m.get("sbsub", a[0]+"/"+a[1])
		if !ok {
			return mockErr(404, "STORAGEBOX_SUBACCOUNT_NOT_FOUND", "Sub-account not found")
		}
		if k, ok := applyForm(s, form, "homedirectory", "samba", "ssh", "external_reachability", "webdav", "readonly", "comment"); !ok {
			return mockBadValue(k)
		}
		return 200, nil
	})
	route(http.MethodDelete, `/storagebox/(\d+)/subaccount/([^/]+)`, func(m *mockRobot, _ *http.Request, a []string, _ url.Values) (int, any) {
		if _, ok := m.get("storagebox", a[0]); !ok {
			return mockStorageBoxNotFound(a[0])
		}
		if _, ok := m.get("sbsub", a[0]+"/"+a[1]); !ok {
			return mockErr(404, "STORAGEBOX_SUBACCOUNT_NOT_FOUND", "Sub-account not found")
		}
		m.del("sbsub", a[0]+"/"+a[1])
		return 200, nil
	})
	route(http.MethodPost, `/storagebox/(\d+)/subaccount/([^/]+)/password`, func(m *mockRobot, _ *http.Request, a []string, form url.Values) (int, any) {
		if _, ok := m.get("storagebox", a[0]); !ok {
			return mockStorageBoxNotFound(a[0])
		}
		s, ok := m.get("sbsub", a[0]+"/"+a[1])
		if !ok {
			return mockErr(404, "STORAGEBOX_SUBACCOUNT_NOT_FOUND", "Sub-account not found")
		}
		pw, status, body := mockStorageBoxPassword(form)
		if status != 0 {
			return status, body
		}
		s["_password"] = pw
		return 200, map[string]any{"password": pw}
	})
}

// vswitchVLAN validates the vlan form value: 4000–4091 and unique among the
// vSwitches that are not cancelled (self excluded). status is 0 when valid.
func (m *mockRobot) vswitchVLAN(form url.Values, self int64) (int64, int, any) {
	vlan, _, err := formIntValue(form, "vlan")
	if err != nil || vlan < 4000 || vlan > 4091 {
		status, body := mockBadValue("vlan")
		return 0, status, body
	}
	for _, v := range m.list("vswitch", nil) {
		if v["id"] != self && v["cancelled"] == false && v["vlan"] == vlan {
			status, body := mockErr(409, "VSWITCH_VLAN_NOT_UNIQUE", "The vSwitch cannot be updated because of a conflicting VLAN ID")
			return 0, status, body
		}
	}
	return vlan, 0, nil
}

// vswitchView renders a vSwitch bare, with copies of its server rows.
func (m *mockRobot) vswitchView(v map[string]any) map[string]any {
	out := copyMap(v)
	servers := []map[string]any{}
	for _, s := range v["server"].([]map[string]any) {
		servers = append(servers, copyMap(s))
	}
	out["server"] = servers
	return out
}

// serverByIdentifier finds a fixture server by number or main IP.
func (m *mockRobot) serverByIdentifier(id string) map[string]any {
	if s, ok := m.get("server", id); ok {
		return s
	}
	for _, s := range m.list("server", nil) {
		if s["server_ip"] == id {
			return s
		}
	}
	return nil
}

func mockStorageBoxNotFound(id string) (int, any) {
	return mockErr(404, "STORAGEBOX_NOT_FOUND", "Storage Box with ID "+id+" not found")
}

func mockBadValue(key string) (int, any) {
	return 400, map[string]any{"error": map[string]any{
		"status": 400, "code": "INVALID_INPUT", "message": "invalid input", "missing": nil, "invalid": []string{key},
	}}
}

// mockPublic drops the mock's own bookkeeping keys (those starting with "_").
func mockPublic(in map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range in {
		if !strings.HasPrefix(k, "_") {
			out[k] = v
		}
	}
	return out
}

// mockStorageBoxPassword applies the documented password guideline to the
// optional "password" parameter, generating one when it is left out.
func mockStorageBoxPassword(form url.Values) (string, int, any) {
	if !form.Has("password") {
		return "GeneratedPassw0rd!", 0, nil
	}
	pw := form.Get("password")
	var upper, lower, digit, special bool
	for _, r := range pw {
		switch {
		case unicode.IsUpper(r):
			upper = true
		case unicode.IsLower(r):
			lower = true
		case unicode.IsDigit(r):
			digit = true
		default:
			special = true
		}
	}
	if n := len([]rune(pw)); n < 12 || n > 128 || !upper || !lower || !digit || !special {
		status, body := mockErr(409, "STORAGEBOX_INVALID_PASSWORD",
			"The chosen password has been considered insecure or does not comply with our password guideline")
		return "", status, body
	}
	return pw, 0, nil
}
