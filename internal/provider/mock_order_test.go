package provider

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Magic product ids that steer the order mock:
//   - mockOrderNotEnabled answers 412 PRECONDITION_FAILED, like an account that
//     has not enabled ordering through the webservice;
//   - mockOrderSlow is accepted but its transaction never leaves "in process".
const (
	mockOrderNotEnabled = "NOT-ENABLED"
	mockOrderSlow       = "EX-SLOW"
	mockMarketProduct   = int64(2001002)
)

func mockPrice(location, net, setup string, hourly bool) map[string]any {
	p := map[string]any{"net": net, "gross": net, "hourly_net": nil, "hourly_gross": nil}
	if hourly {
		p["hourly_net"], p["hourly_gross"] = "0.0795", "0.0795"
	}
	return map[string]any{"location": location, "price": p, "price_setup": map[string]any{"net": setup, "gross": setup}}
}

func mockPrimaryIPv4Addon() []any {
	return []any{map[string]any{
		"id": "primary_ipv4", "name": "Primary IPv4", "min": 0, "max": 1,
		"prices": []any{mockPrice("FSN1", "1.7000", "0.0000", true), mockPrice("NBG1", "1.7000", "0.0000", true)},
	}}
}

func mockStandardProducts() []map[string]any {
	return []map[string]any{
		{
			"id": "AX41-NVMe", "name": "Dedicated Server AX41-NVMe",
			"description": []any{"AMD Ryzen 5 3600", "64 GB DDR4 RAM", "2 x 512 GB NVMe SSD"},
			"traffic":     "unlimited", "dist": []any{"Rescue system", "Debian 13 base"},
			"@deprecated arch": []any{64}, "lang": []any{"en"}, "location": []any{"FSN1"},
			"prices":           []any{mockPrice("FSN1", "39.0000", "0.0000", true)},
			"orderable_addons": mockPrimaryIPv4Addon(),
		},
		{
			"id": "EX44", "name": "Dedicated Server EX44",
			"description": []any{"Intel Core i5-13500", "64 GB DDR4 RAM", "2 x 512 GB NVMe SSD"},
			"traffic":     "unlimited", "dist": []any{"Rescue system", "Debian 13 base", "Ubuntu 24.04 LTS base"},
			"@deprecated arch": []any{64}, "lang": []any{"en", "de"}, "location": []any{"FSN1", "NBG1"},
			"prices":           []any{mockPrice("FSN1", "44.0000", "79.0000", false), mockPrice("NBG1", "44.0000", "79.0000", false)},
			"orderable_addons": mockPrimaryIPv4Addon(),
		},
	}
}

func mockMarketProducts() []map[string]any {
	product := func(id int64, name string, memory int64, price string) map[string]any {
		return map[string]any{
			"id": id, "name": name, "description": []any{"Intel Core i7-6700", fmt.Sprintf("%d GB RAM", memory)},
			"traffic": "unlimited", "dist": []any{"Rescue system"}, "@deprecated arch": []any{64}, "lang": []any{"en"},
			"cpu": "Intel Core i7-6700", "cpu_benchmark": 8164, "memory_size": memory, "hdd_size": 512,
			"hdd_text": "ENT.HDD ECC INIC", "hdd_count": 2, "datacenter": "FSN1-DC14", "network_speed": "1 Gbit/s",
			"price": price, "price_hourly": nil, "price_setup": "0.0000", "price_vat": price,
			"price_hourly_vat": nil, "price_setup_vat": "0.0000", "fixed_price": false, "next_reduce": -10800,
			"next_reduce_date": "2026-10-12 12:00:00", "orderable_addons": mockPrimaryIPv4Addon(),
		}
	}
	return []map[string]any{product(2001001, "SB34", 32, "30.0000"), product(mockMarketProduct, "SB109", 64, "36.0000")}
}

// mockAddonProducts are the addons every server of the mock can order.
func mockAddonProducts() []map[string]any {
	addon := func(id, name, typ, net, setup string) map[string]any {
		return map[string]any{"id": id, "name": name, "type": typ, "price": mockPrice("FSN1", net, setup, true)}
	}
	return []map[string]any{
		addon("additional_ipv4", "Additional IP address", "ip_ipv4", "1.7000", "0.0000"),
		addon("subnet_ipv4_29", "Additional subnet /29 (monthly charge)", "subnet_ipv4", "6.7227", "152.0000"),
		addon("failover_subnet_ipv4_29", "Failover subnet /29", "failover_subnet_ipv4", "15.1261", "152.0000"),
	}
}

func findProduct(list []map[string]any, id string) (map[string]any, bool) {
	for _, p := range list {
		if fmt.Sprint(p["id"]) == id {
			return p, true
		}
	}
	return nil, false
}

func stringsOf(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, fmt.Sprint(x))
	}
	return out
}

func init() {
	route(http.MethodGet, `/order/currency`, func(*mockRobot, *http.Request, []string, url.Values) (int, any) {
		return 200, map[string]any{"currency": "EUR"}
	})

	// --- products ---
	route(http.MethodGet, `/order/server/product`, func(_ *mockRobot, _ *http.Request, _ []string, form url.Values) (int, any) {
		var out []map[string]any
		for _, p := range mockStandardProducts() {
			price, _ := strconv.ParseFloat(p["prices"].([]any)[0].(map[string]any)["price"].(map[string]any)["net"].(string), 64)
			setup, _ := strconv.ParseFloat(p["prices"].([]any)[0].(map[string]any)["price_setup"].(map[string]any)["net"].(string), 64)
			keep := true
			for key, check := range map[string]func(float64) bool{
				"min_price": func(f float64) bool { return price >= f }, "max_price": func(f float64) bool { return price <= f },
				"min_price_setup": func(f float64) bool { return setup >= f }, "max_price_setup": func(f float64) bool { return setup <= f },
			} {
				if v := form.Get(key); v != "" {
					f, err := strconv.ParseFloat(v, 64)
					if err != nil {
						return mockErr(400, "INVALID_INPUT", "invalid input")
					}
					keep = keep && check(f)
				}
			}
			if loc := form.Get("location"); loc != "" {
				keep = keep && slices.Contains(stringsOf(p["location"]), loc)
			}
			if keep {
				out = append(out, p)
			}
		}
		return wrapList("product", out, "No products found")
	})
	route(http.MethodGet, `/order/server/product/([^/]+)`, func(_ *mockRobot, _ *http.Request, a []string, _ url.Values) (int, any) {
		if p, ok := findProduct(mockStandardProducts(), a[0]); ok {
			return 200, wrap("product", p)
		}
		return mockErr(404, "NOT_FOUND", "Product not found")
	})
	route(http.MethodGet, `/order/server_market/product`, func(*mockRobot, *http.Request, []string, url.Values) (int, any) {
		return wrapList("product", mockMarketProducts(), "No products found")
	})
	route(http.MethodGet, `/order/server_market/product/([^/]+)`, func(_ *mockRobot, _ *http.Request, a []string, _ url.Values) (int, any) {
		if p, ok := findProduct(mockMarketProducts(), a[0]); ok {
			return 200, wrap("product", p)
		}
		return mockErr(404, "NOT_FOUND", "Product not found")
	})
	route(http.MethodGet, `/order/server_addon/(\d+)/product`, func(m *mockRobot, _ *http.Request, a []string, _ url.Values) (int, any) {
		if _, ok := m.get("server", a[0]); !ok {
			return mockErr(404, "SERVER_NOT_FOUND", "Server with id "+a[0]+" not found")
		}
		return wrapList("product", mockAddonProducts(), "No products found")
	})

	// --- server orders ---
	for _, kind := range []string{"server", "server_market"} {
		market := kind == "server_market"
		route(http.MethodPost, `/order/`+kind+`/transaction`, func(m *mockRobot, _ *http.Request, _ []string, form url.Values) (int, any) {
			return m.orderServer(form, market)
		})
		route(http.MethodGet, `/order/`+kind+`/transaction/([^/]+)`, func(m *mockRobot, _ *http.Request, a []string, _ url.Values) (int, any) {
			tx, ok := m.get("order_tx", a[0])
			if !ok || tx["_kind"] != kind {
				return mockErr(404, "NOT_FOUND", "Transaction not found")
			}
			m.advanceServerOrder(tx)
			return 200, wrap("transaction", publicTx(tx))
		})
	}

	// --- addon orders ---
	route(http.MethodPost, `/order/server_addon/transaction`, func(m *mockRobot, _ *http.Request, _ []string, form url.Values) (int, any) {
		server := form.Get("server_number")
		if _, ok := m.get("server", server); !ok {
			return mockErr(404, "SERVER_NOT_FOUND", "Server with id "+server+" not found")
		}
		p, ok := findProduct(mockAddonProducts(), form.Get("product_id"))
		if !ok {
			return mockInvalid("product_id")
		}
		if form.Get("reason") == "" {
			return mockInvalid("reason")
		}
		tx := map[string]any{
			"id": m.txID("S"), "date": "2026-10-12T12:00:00+02:00", "status": "in process",
			"server_number": mustInt(server), "resources": []any{}, "_kind": "server_addon", "_polls": 0,
			"_gateway": form.Get("gateway"),
			"product":  map[string]any{"id": p["id"], "name": p["name"], "type": p["type"], "price": p["price"]},
		}
		if form.Get("test") != "true" {
			m.put("order_tx", tx["id"].(string), tx)
		}
		return 201, wrap("transaction", publicTx(tx))
	})
	route(http.MethodGet, `/order/server_addon/transaction/([^/]+)`, func(m *mockRobot, _ *http.Request, a []string, _ url.Values) (int, any) {
		tx, ok := m.get("order_tx", a[0])
		if !ok || tx["_kind"] != "server_addon" {
			return mockErr(404, "NOT_FOUND", "Transaction not found")
		}
		m.advanceAddonOrder(tx)
		out := publicTx(tx)
		// The transaction read leaves out the product type, as documented.
		prod := copyMap(out["product"].(map[string]any))
		delete(prod, "type")
		out["product"] = prod
		return 200, wrap("transaction", out)
	})

	// --- traffic ---
	route(http.MethodPost, `/traffic`, func(m *mockRobot, _ *http.Request, _ []string, form url.Values) (int, any) {
		return m.traffic(form)
	})
}

func (m *mockRobot) txID(suffix string) string {
	return fmt.Sprintf("B20261012-%d-%s", m.newID(), suffix)
}

// publicTx drops the mock's bookkeeping fields (prefixed "_").
func publicTx(tx map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range tx {
		if !strings.HasPrefix(k, "_") {
			out[k] = v
		}
	}
	return out
}

func (m *mockRobot) orderServer(form url.Values, market bool) (int, any) {
	id := form.Get("product_id")
	if id == mockOrderNotEnabled {
		return mockErr(412, "PRECONDITION_FAILED", "Order cannot be accepted. Please place your order via the web frontend for further details.")
	}
	var product map[string]any
	switch {
	case !market && id == mockOrderSlow:
		product, _ = findProduct(mockStandardProducts(), "EX44")
		product = copyMap(product)
		product["id"] = mockOrderSlow
	case market:
		p, ok := findProduct(mockMarketProducts(), id)
		if !ok {
			return mockInvalid("product_id")
		}
		product = p
	default:
		p, ok := findProduct(mockStandardProducts(), id)
		if !ok {
			return mockInvalid("product_id")
		}
		product = p
	}

	location := form.Get("location")
	if !market {
		locs := stringsOf(product["location"])
		if location == "" && len(locs) > 1 || location != "" && !slices.Contains(locs, location) {
			return mockErr(400, "INVALID_INPUT", "invalid input")
		}
		if location == "" {
			location = locs[0]
		}
	}
	dist := form.Get("dist")
	if dist != "" && !slices.Contains(stringsOf(product["dist"]), dist) {
		return mockErr(400, "INVALID_INPUT", "invalid input")
	}
	if dist == "" {
		dist = "Rescue system"
	}
	keys := form["authorized_key[]"]
	if len(keys) == 0 && form.Get("password") == "" {
		return mockInvalid("authorized_key", "password")
	}
	authorized := []any{}
	for _, fp := range keys {
		k, ok := m.get("key", fp)
		if !ok {
			return mockErr(400, "INVALID_INPUT", "invalid input")
		}
		authorized = append(authorized, map[string]any{"key": map[string]any{
			"name": k["name"], "fingerprint": fp, "type": k["type"], "size": k["size"]}})
	}
	addons := []any{}
	for _, a := range form["addon[]"] {
		addons = append(addons, a)
	}

	kind := "server"
	txProduct := map[string]any{
		"id": product["id"], "name": product["name"], "description": product["description"],
		"traffic": product["traffic"], "dist": dist, "@deprecated arch": "64", "lang": "en",
	}
	if market {
		kind = "server_market"
		for _, k := range []string{"cpu", "cpu_benchmark", "memory_size", "hdd_size", "hdd_text", "hdd_count", "datacenter", "network_speed"} {
			txProduct[k] = product[k]
		}
	} else {
		txProduct["location"] = location
	}
	var comment any
	if c := form.Get("comment"); c != "" {
		comment = c
	}
	tx := map[string]any{
		"id": m.txID("251479"), "date": "2026-10-12T12:00:00+02:00", "status": "in process",
		"server_number": nil, "server_ip": nil, "authorized_key": authorized, "host_key": []any{},
		"comment": comment, "product": txProduct, "addons": addons,
		"_kind": kind, "_polls": 0, "_slow": id == mockOrderSlow,
	}
	if form.Get("test") != "true" {
		m.put("order_tx", tx["id"].(string), tx)
	}
	return 201, wrap("transaction", publicTx(tx))
}

// advanceServerOrder lets a transaction become ready on its second read and
// puts the new server (and its IPv4, when ordered) into the account.
func (m *mockRobot) advanceServerOrder(tx map[string]any) {
	tx["_polls"] = tx["_polls"].(int) + 1
	if tx["status"] != "in process" || tx["_slow"] == true || tx["_polls"].(int) < 2 {
		return
	}
	number := m.newID()
	v6 := fmt.Sprintf("2a01:4f8:200:%x::", number)
	var serverIP any
	ips := []any{}
	if slices.Contains(tx["addons"].([]any), any("primary_ipv4")) {
		ip := fmt.Sprintf("198.51.100.%d", number%250)
		serverIP = ip
		ips = append(ips, ip)
		m.put("ip", ip, map[string]any{
			"ip": ip, "gateway": "198.51.100.1", "mask": int64(26), "broadcast": "198.51.100.63",
			"server_ip": ip, "server_number": number, "locked": false, "separate_mac": nil,
			"traffic_warnings": false, "traffic_hourly": int64(200), "traffic_daily": int64(2000), "traffic_monthly": int64(20),
		})
	}
	product := tx["product"].(map[string]any)
	dc := "FSN1-DC14"
	if d, ok := product["datacenter"].(string); ok {
		dc = d
	}
	m.put("server", strconv.FormatInt(number, 10), map[string]any{
		"server_ip": serverIP, "server_ipv6_net": v6, "server_number": number,
		"server_name": "", "product": product["name"], "dc": dc, "traffic": "unlimited",
		"status": "ready", "cancelled": false, "paid_until": "2026-11-12",
		"ip": ips, "subnet": []any{map[string]any{"ip": v6, "mask": "64"}},
		"reset": true, "rescue": true, "vnc": true, "windows": false, "plesk": false, "cpanel": false,
		"wol": false, "hot_swap": false, "linked_storagebox": nil,
	})
	tx["status"] = "ready"
	tx["server_number"] = number
	tx["server_ip"] = serverIP
	tx["host_key"] = []any{map[string]any{"key": map[string]any{
		"fingerprint": "c1:e4:08:73:dd:f7:e9:d1:94:ab:e9:0f:28:b2:d2:ed", "type": "ED25519", "size": 256}}}
}

// advanceAddonOrder lets an addon order become ready on its second read and
// adds what it ordered to the account.
func (m *mockRobot) advanceAddonOrder(tx map[string]any) {
	tx["_polls"] = tx["_polls"].(int) + 1
	if tx["status"] != "in process" || tx["_polls"].(int) < 2 {
		return
	}
	number := tx["server_number"].(int64)
	server, _ := m.get("server", strconv.FormatInt(number, 10))
	n := m.newID() % 250
	switch tx["product"].(map[string]any)["type"] {
	case "ip_ipv4":
		ip := fmt.Sprintf("203.0.113.%d", n)
		m.put("ip", ip, map[string]any{
			"ip": ip, "gateway": "203.0.113.1", "mask": int64(32), "broadcast": ip,
			"server_ip": server["server_ip"], "server_number": number, "locked": false, "separate_mac": nil,
			"traffic_warnings": false, "traffic_hourly": int64(200), "traffic_daily": int64(2000), "traffic_monthly": int64(20),
		})
		server["ip"] = append(server["ip"].([]any), ip)
		tx["resources"] = []any{map[string]any{"type": "ip", "id": ip}}
	default:
		subnet := fmt.Sprintf("10.%d.0.0", n)
		m.put("subnet", subnet, map[string]any{
			"ip": subnet, "mask": int64(29), "gateway": fmt.Sprintf("10.%d.0.1", n), "server_ip": server["server_ip"],
			"server_number": number, "failover": tx["product"].(map[string]any)["type"] == "failover_subnet_ipv4", "locked": false,
			"traffic_warnings": false, "traffic_hourly": int64(100), "traffic_daily": int64(500), "traffic_monthly": int64(2),
		})
		server["subnet"] = append(server["subnet"].([]any), map[string]any{"ip": subnet, "mask": "29"})
		tx["resources"] = []any{map[string]any{"type": "subnet", "id": subnet}}
	}
	tx["status"] = "ready"
}

var mockTrafficFormats = map[string]*regexp.Regexp{
	"day":   regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T(\d{2})$`),
	"month": regexp.MustCompile(`^\d{4}-\d{2}-(\d{2})$`),
	"year":  regexp.MustCompile(`^\d{4}-(\d{2})$`),
}

// traffic answers POST /traffic: one sum per known address, or one value per
// hour/day/month with single_values. Unknown addresses are left out; when none
// is left, data is [] — PHP renders an empty object that way.
func (m *mockRobot) traffic(form url.Values) (int, any) {
	typ := form.Get("type")
	re, ok := mockTrafficFormats[typ]
	if !ok {
		return mockInvalid("type")
	}
	from, to := re.FindStringSubmatch(form.Get("from")), re.FindStringSubmatch(form.Get("to"))
	if from == nil || to == nil {
		return 400, map[string]any{"error": map[string]any{
			"status": 400, "code": "INVALID_INPUT", "message": "invalid input", "missing": nil, "invalid": []string{"from", "to"}}}
	}
	ips, subnets := form["ip[]"], form["subnet[]"]
	if len(ips) == 0 && len(subnets) == 0 {
		return mockInvalid("ip", "subnet")
	}
	first, _ := strconv.Atoi(from[1])
	last, _ := strconv.Atoi(to[1])
	value := func(i int) map[string]any {
		in, out := float64(i)*1.5, float64(i)*0.5
		return map[string]any{"in": in, "out": out, "sum": in + out}
	}
	data := map[string]any{}
	add := func(key string) {
		if form.Get("single_values") != "true" {
			data[key] = value(last - first + 1)
			return
		}
		byInterval := map[string]any{}
		for i := first; i <= last; i++ {
			byInterval[fmt.Sprintf("%02d", i)] = value(i)
		}
		data[key] = byInterval
	}
	for _, ip := range ips {
		if m.knownIP(ip) {
			add(ip)
		}
	}
	for _, s := range subnets {
		if e, ok := m.get("subnet", s); ok {
			add(fmt.Sprintf("%s/%d", s, e["mask"]))
		}
	}
	var body any = data
	if len(data) == 0 {
		body = []any{}
	}
	return 200, wrap("traffic", map[string]any{"type": typ, "from": form.Get("from"), "to": form.Get("to"), "data": body})
}
