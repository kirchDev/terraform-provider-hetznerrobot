package provider

// The account every mock starts with. Robot entities that the webservice
// cannot create — servers, IPs, subnets, failover IPs, the inclusive Storage
// Box — exist from the start, the way they do on a real account:
//
//	server 321  123.123.123.123  2a01:4f8:111:4221::/64  + IP 123.123.123.125, subnet 178.63.123.120/29
//	server 421  123.123.123.124  2a01:4f8:111:4222::/64
//	failover IP 88.99.1.10 (ordered on 321, routed to 321)
//	Storage Box 123456 (BX10, linked to 321)
const (
	fxServer1    = "321"
	fxServer1IP  = "123.123.123.123"
	fxServer1V6  = "2a01:4f8:111:4221::"
	fxServer1IP2 = "123.123.123.125"
	fxServer2    = "421"
	fxServer2IP  = "123.123.123.124"
	fxServer2V6  = "2a01:4f8:111:4222::"
	fxSubnetV4   = "178.63.123.120"
	fxFailover   = "88.99.1.10"
	fxStorageBox = "123456"
)

func init() {
	seed(func(m *mockRobot) {
		m.put("server", fxServer1, map[string]any{
			"server_ip": fxServer1IP, "server_ipv6_net": fxServer1V6, "server_number": int64(321),
			"server_name": "server1", "product": "EX44", "dc": "FSN1-DC14", "traffic": "unlimited",
			"status": "ready", "cancelled": false, "paid_until": "2026-12-31",
			"ip":     []any{fxServer1IP, fxServer1IP2},
			"subnet": []any{map[string]any{"ip": fxServer1V6, "mask": "64"}, map[string]any{"ip": fxSubnetV4, "mask": "29"}},
			"reset":  true, "rescue": true, "vnc": true, "windows": true, "plesk": false, "cpanel": false,
			"wol": true, "hot_swap": false, "linked_storagebox": int64(123456),
		})
		m.put("server", fxServer2, map[string]any{
			"server_ip": fxServer2IP, "server_ipv6_net": fxServer2V6, "server_number": int64(421),
			"server_name": "server2", "product": "AX41-NVMe", "dc": "NBG1-DC3", "traffic": "unlimited",
			"status": "ready", "cancelled": false, "paid_until": "2026-12-31",
			"ip":     []any{fxServer2IP},
			"subnet": []any{map[string]any{"ip": fxServer2V6, "mask": "64"}},
			"reset":  true, "rescue": true, "vnc": true, "windows": false, "plesk": false, "cpanel": false,
			"wol": false, "hot_swap": false, "linked_storagebox": nil,
		})

		for _, ip := range []struct{ ip, server, serverIP string }{
			{fxServer1IP, fxServer1, fxServer1IP},
			{fxServer1IP2, fxServer1, fxServer1IP},
			{fxServer2IP, fxServer2, fxServer2IP},
		} {
			m.put("ip", ip.ip, map[string]any{
				"ip": ip.ip, "gateway": "123.123.123.65", "mask": int64(26), "broadcast": "123.123.123.127",
				"server_ip": ip.serverIP, "server_number": mustInt(ip.server), "locked": false, "separate_mac": nil,
				"traffic_warnings": false, "traffic_hourly": int64(200), "traffic_daily": int64(2000), "traffic_monthly": int64(20),
			})
		}

		for _, s := range []struct {
			ip, gateway, server, serverIP string
			mask                          int64
		}{
			{fxServer1V6, "fe80::1", fxServer1, fxServer1IP, 64},
			{fxServer2V6, "fe80::1", fxServer2, fxServer2IP, 64},
			{fxSubnetV4, "178.63.123.121", fxServer1, fxServer1IP, 29},
		} {
			m.put("subnet", s.ip, map[string]any{
				"ip": s.ip, "mask": s.mask, "gateway": s.gateway, "server_ip": s.serverIP,
				"server_number": mustInt(s.server), "failover": false, "locked": false,
				"traffic_warnings": false, "traffic_hourly": int64(100), "traffic_daily": int64(500), "traffic_monthly": int64(2),
			})
		}

		m.put("failover", fxFailover, map[string]any{
			"ip": fxFailover, "netmask": "255.255.255.255", "server_ip": fxServer1IP,
			"server_ipv6_net": fxServer1V6, "server_number": int64(321), "active_server_ip": fxServer1IP,
		})

		m.put("rdns", fxServer1IP, map[string]any{"ip": fxServer1IP, "ptr": "server1.example.com"})

		m.put("storagebox", fxStorageBox, map[string]any{
			"id": int64(123456), "login": "u12345", "name": "Backup Server 1", "product": "BX10",
			"cancelled": false, "locked": false, "location": "FSN1", "linked_server": int64(321),
			"paid_until": "2026-12-31", "disk_quota": int64(102400), "disk_usage": int64(900),
			"disk_usage_data": int64(500), "disk_usage_snapshots": int64(400), "webdav": false,
			"samba": false, "ssh": true, "external_reachability": false, "zfs": false,
			"server": "u12345.your-storagebox.de", "host_system": "FSN1-BX355",
		})
	})
}

func mustInt(s string) int64 {
	n, err := parseInt64(s, "fixture id")
	if err != nil {
		panic(err)
	}
	return n
}

// knownIP reports whether ip is a single IP or lies in a subnet of the account,
// the check every per-IP endpoint (rDNS, traffic) makes first.
func (m *mockRobot) knownIP(ip string) bool {
	if _, ok := m.get("ip", ip); ok {
		return true
	}
	for _, s := range m.list("subnet", nil) {
		prefix := s["ip"].(string)
		if len(prefix) > 2 && prefix[len(prefix)-2:] == "::" && len(ip) >= len(prefix)-2 && ip[:len(prefix)-2] == prefix[:len(prefix)-2] {
			return true
		}
		if ip == prefix {
			return true
		}
	}
	_, ok := m.get("failover", ip)
	return ok
}
