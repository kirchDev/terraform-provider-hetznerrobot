package provider

import (
	"fmt"
	"regexp"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccMockServer(t *testing.T) {
	m := newMockRobot(t)
	cfg := func(name string) string {
		return fmt.Sprintf(`
resource "hetznerrobot_server" "s" {
  server_number = 321
  server_name   = %q
}
data "hetznerrobot_server" "s" {
  server_number = hetznerrobot_server.s.server_number
}
data "hetznerrobot_servers" "all" {
  depends_on = [hetznerrobot_server.s]
}`, name)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: cfg("web1"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hetznerrobot_server.s", "server_name", "web1"),
					resource.TestCheckResourceAttr("hetznerrobot_server.s", "server_ip", fxServer1IP),
					resource.TestCheckResourceAttr("hetznerrobot_server.s", "ips.#", "2"),
					resource.TestCheckResourceAttr("hetznerrobot_server.s", "subnets.0.mask", "64"),
					resource.TestCheckResourceAttr("hetznerrobot_server.s", "linked_storagebox", "123456"),
					resource.TestCheckResourceAttr("data.hetznerrobot_server.s", "server_name", "web1"),
					resource.TestCheckResourceAttr("data.hetznerrobot_server.s", "rescue", "true"),
					resource.TestCheckResourceAttr("data.hetznerrobot_servers.all", "servers.#", "2"),
					resource.TestCheckResourceAttr("data.hetznerrobot_servers.all", "servers.1.product", "AX41-NVMe"),
				),
			},
			{
				Config: cfg("web1-renamed"),
				Check:  resource.TestCheckResourceAttr("hetznerrobot_server.s", "server_name", "web1-renamed"),
			},
			{
				ResourceName:                         "hetznerrobot_server.s",
				ImportState:                          true,
				ImportStateId:                        "321",
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "server_number",
			},
		},
		CheckDestroy: func(_ *terraformState) error {
			s, ok := m.get("server", fxServer1)
			if !ok || s["server_name"] != "web1-renamed" {
				return fmt.Errorf("destroy must only forget the server, got %v", s)
			}
			return nil
		},
	})
}

func TestAccMockServerAdoptMissing(t *testing.T) {
	newMockRobot(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories(),
		Steps: []resource.TestStep{{
			Config:      `resource "hetznerrobot_server" "s" { server_number = 999 }`,
			ExpectError: regexp.MustCompile(`no server 999`),
		}},
	})
}

func TestAccMockServerCancellation(t *testing.T) {
	m := newMockRobot(t)
	today := time.Now().UTC().Format("2006-01-02")
	cfg := func(date string) string {
		return fmt.Sprintf(`
resource "hetznerrobot_server_cancellation" "a" {
  server_number       = 321
  cancellation_date   = %q
  cancellation_reason = "Server too expensive"
}
resource "hetznerrobot_server_cancellation" "b" {
  server_number     = 421
  cancellation_date = "now"
  reserve_location  = false
}
data "hetznerrobot_server_cancellation" "a" {
  server_number = 321
  depends_on    = [hetznerrobot_server_cancellation.a]
}`, date)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: cfg("2026-12-31"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hetznerrobot_server_cancellation.a", "cancelled", "true"),
					resource.TestCheckResourceAttr("hetznerrobot_server_cancellation.a", "effective_date", "2026-12-31"),
					resource.TestCheckResourceAttr("hetznerrobot_server_cancellation.a", "server_name", "server1"),
					resource.TestCheckResourceAttr("hetznerrobot_server_cancellation.b", "cancellation_date", "now"),
					resource.TestCheckResourceAttr("hetznerrobot_server_cancellation.b", "effective_date", today),
					resource.TestCheckResourceAttr("hetznerrobot_server_cancellation.b", "reservation_possible", "true"),
					resource.TestCheckResourceAttr("data.hetznerrobot_server_cancellation.a", "cancelled", "true"),
					resource.TestCheckResourceAttr("data.hetznerrobot_server_cancellation.a", "cancellation_reason", "Server too expensive"),
					resource.TestCheckResourceAttr("data.hetznerrobot_server_cancellation.a", "cancellation_reasons.#", "1"),
				),
			},
			{
				// A new date withdraws the old cancellation and files a new one.
				Config: cfg("2027-01-31"),
				Check:  resource.TestCheckResourceAttr("hetznerrobot_server_cancellation.a", "effective_date", "2027-01-31"),
			},
			{
				ResourceName:                         "hetznerrobot_server_cancellation.a",
				ImportState:                          true,
				ImportStateId:                        "321",
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "server_number",
			},
			{
				ResourceName:                         "hetznerrobot_server_cancellation.b",
				ImportState:                          true,
				ImportStateId:                        "421",
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "server_number",
				// "now" is configuration only — Robot holds the actual date; reserve_location is write-only.
				ImportStateVerifyIgnore: []string{"cancellation_date", "reserve_location"},
			},
		},
		CheckDestroy: func(_ *terraformState) error {
			if n := len(m.list("server_cancellation", nil)); n != 0 {
				return fmt.Errorf("%d server cancellations survived destroy", n)
			}
			return nil
		},
	})
}

func TestAccMockServerCancellationData(t *testing.T) {
	newMockRobot(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: `data "hetznerrobot_server_cancellation" "s" { server_number = 421 }`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.hetznerrobot_server_cancellation.s", "cancelled", "false"),
					resource.TestCheckResourceAttr("data.hetznerrobot_server_cancellation.s", "cancellation_reasons.#", "4"),
					resource.TestCheckNoResourceAttr("data.hetznerrobot_server_cancellation.s", "cancellation_reason"),
					resource.TestCheckResourceAttr("data.hetznerrobot_server_cancellation.s", "earliest_cancellation_date", mockEarliestCancellation),
				),
			},
			{
				Config: `
resource "hetznerrobot_server_cancellation" "s" {
  server_number     = 321
  cancellation_date = "end of month"
}`,
				ExpectError: regexp.MustCompile(`must be .now. or a date`),
			},
			{
				// Robot requires reserve_location where the location can be reserved.
				Config: `
resource "hetznerrobot_server_cancellation" "s" {
  server_number     = 421
  cancellation_date = "2026-12-31"
}`,
				ExpectError: regexp.MustCompile(`missing: reserve_location`),
			},
		},
	})
}

func TestAccMockIP(t *testing.T) {
	m := newMockRobot(t)
	cfg := func(daily int) string {
		return fmt.Sprintf(`
resource "hetznerrobot_ip" "extra" {
  ip               = %q
  traffic_warnings = true
  traffic_hourly   = 300
  traffic_daily    = %d
}
resource "hetznerrobot_ip_mac" "extra" {
  ip = %q
}
data "hetznerrobot_ip" "extra" {
  ip         = hetznerrobot_ip.extra.ip
  depends_on = [hetznerrobot_ip_mac.extra]
}
data "hetznerrobot_ips" "server1" {
  server_ip = %q
}
data "hetznerrobot_ips" "all" {}`, fxServer1IP2, daily, fxServer1IP2, fxServer1IP)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: cfg(3000),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hetznerrobot_ip.extra", "traffic_warnings", "true"),
					resource.TestCheckResourceAttr("hetznerrobot_ip.extra", "traffic_hourly", "300"),
					resource.TestCheckResourceAttr("hetznerrobot_ip.extra", "traffic_monthly", "20"),
					resource.TestCheckResourceAttr("hetznerrobot_ip.extra", "server_number", "321"),
					resource.TestCheckResourceAttr("hetznerrobot_ip.extra", "mask", "26"),
					resource.TestCheckResourceAttr("hetznerrobot_ip_mac.extra", "mac", "00:50:56:00:4c:1f"),
					resource.TestCheckResourceAttr("data.hetznerrobot_ip.extra", "separate_mac", "00:50:56:00:4c:1f"),
					resource.TestCheckResourceAttr("data.hetznerrobot_ip.extra", "broadcast", "123.123.123.127"),
					resource.TestCheckResourceAttr("data.hetznerrobot_ips.server1", "ips.#", "2"),
					resource.TestCheckResourceAttr("data.hetznerrobot_ips.all", "ips.#", "3"),
				),
			},
			{
				Config: cfg(4000),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hetznerrobot_ip.extra", "traffic_daily", "4000"),
					resource.TestCheckResourceAttr("hetznerrobot_ip.extra", "traffic_hourly", "300"),
				),
			},
			{
				ResourceName:                         "hetznerrobot_ip.extra",
				ImportState:                          true,
				ImportStateId:                        fxServer1IP2,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "ip",
				// Set by hetznerrobot_ip_mac after the IP was last read.
				ImportStateVerifyIgnore: []string{"separate_mac"},
			},
			{
				ResourceName:                         "hetznerrobot_ip_mac.extra",
				ImportState:                          true,
				ImportStateId:                        fxServer1IP2,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "ip",
			},
		},
		CheckDestroy: func(_ *terraformState) error {
			ip, _ := m.get("ip", fxServer1IP2)
			if ip["separate_mac"] != nil {
				return fmt.Errorf("separate MAC survived destroy")
			}
			if ip["traffic_daily"] != int64(4000) {
				return fmt.Errorf("destroy must only forget the IP, got %v", ip)
			}
			return nil
		},
	})
}

func TestAccMockIPMACNotAvailable(t *testing.T) {
	newMockRobot(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories(),
		Steps: []resource.TestStep{{
			Config:      fmt.Sprintf(`resource "hetznerrobot_ip_mac" "main" { ip = %q }`, fxServer1IP),
			ExpectError: regexp.MustCompile(`MAC_NOT_AVAILABLE`),
		}},
	})
}

func TestAccMockIPCancellation(t *testing.T) {
	m := newMockRobot(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
resource "hetznerrobot_ip_cancellation" "extra" {
  ip                = %q
  cancellation_date = "2026-12-31"
}
data "hetznerrobot_ip_cancellation" "extra" {
  ip         = hetznerrobot_ip_cancellation.extra.ip
}`, fxServer1IP2),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hetznerrobot_ip_cancellation.extra", "cancelled", "true"),
					resource.TestCheckResourceAttr("hetznerrobot_ip_cancellation.extra", "effective_date", "2026-12-31"),
					resource.TestCheckResourceAttr("hetznerrobot_ip_cancellation.extra", "server_number", "321"),
					resource.TestCheckResourceAttr("data.hetznerrobot_ip_cancellation.extra", "cancellation_date", "2026-12-31"),
					resource.TestCheckResourceAttr("data.hetznerrobot_ip_cancellation.extra", "earliest_cancellation_date", mockEarliestCancellation),
				),
			},
			{
				ResourceName:                         "hetznerrobot_ip_cancellation.extra",
				ImportState:                          true,
				ImportStateId:                        fxServer1IP2,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "ip",
			},
			{
				// Live (2026-10): Robot refuses even to read the cancellation of an
				// IPv6 subnet; the data source surfaces that as an error.
				Config:      fmt.Sprintf(`data "hetznerrobot_subnet_cancellation" "v6" { ip = %q }`, fxServer1V6),
				ExpectError: regexp.MustCompile(`cancellation of IPv6 subnet is not possible`),
			},
		},
		CheckDestroy: func(_ *terraformState) error {
			if _, ok := m.get("ip_cancellation", fxServer1IP2); ok {
				return fmt.Errorf("IP cancellation survived destroy")
			}
			return nil
		},
	})
}

func TestAccMockSubnet(t *testing.T) {
	m := newMockRobot(t)
	cfg := func(mac string, monthly int) string {
		return fmt.Sprintf(`
resource "hetznerrobot_subnet" "v6" {
  ip              = %q
  traffic_monthly = %d
}
resource "hetznerrobot_subnet_mac" "v6" {
  ip  = hetznerrobot_subnet.v6.ip
  mac = %q
}
data "hetznerrobot_subnet" "v6" {
  ip = hetznerrobot_subnet.v6.ip
}
data "hetznerrobot_subnet_mac" "v6" {
  ip         = hetznerrobot_subnet_mac.v6.ip
}
data "hetznerrobot_subnets" "server1" {
  server_ip = %q
}
data "hetznerrobot_subnets" "all" {}`, fxServer1V6, monthly, mac, fxServer1IP)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: cfg(mockSecondMAC, 5),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hetznerrobot_subnet.v6", "traffic_monthly", "5"),
					resource.TestCheckResourceAttr("hetznerrobot_subnet.v6", "traffic_warnings", "false"),
					resource.TestCheckResourceAttr("hetznerrobot_subnet.v6", "mask", "64"),
					resource.TestCheckResourceAttr("hetznerrobot_subnet_mac.v6", "mac", mockSecondMAC),
					resource.TestCheckResourceAttr("hetznerrobot_subnet_mac.v6", "mask", "64"),
					resource.TestCheckResourceAttr("hetznerrobot_subnet_mac.v6", "possible_mac.%", "2"),
					resource.TestCheckResourceAttr("hetznerrobot_subnet_mac.v6", "possible_mac."+fxServer1IP, mockMainMAC),
					resource.TestCheckResourceAttr("data.hetznerrobot_subnet_mac.v6", "mac", mockSecondMAC),
					resource.TestCheckResourceAttr("data.hetznerrobot_subnet.v6", "gateway", "fe80::1"),
					resource.TestCheckResourceAttr("data.hetznerrobot_subnets.server1", "subnets.#", "2"),
					resource.TestCheckResourceAttr("data.hetznerrobot_subnets.all", "subnets.#", "3"),
				),
			},
			{
				Config: cfg(mockMainMAC, 7),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hetznerrobot_subnet.v6", "traffic_monthly", "7"),
					resource.TestCheckResourceAttr("hetznerrobot_subnet_mac.v6", "mac", mockMainMAC),
				),
			},
			{
				Config:      cfg("00:00:00:00:00:01", 7),
				ExpectError: regexp.MustCompile(`invalid: mac`),
			},
			{
				Config: cfg(mockSecondMAC, 7),
			},
			{
				ResourceName:                         "hetznerrobot_subnet.v6",
				ImportState:                          true,
				ImportStateId:                        fxServer1V6,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "ip",
			},
			{
				ResourceName:                         "hetznerrobot_subnet_mac.v6",
				ImportState:                          true,
				ImportStateId:                        fxServer1V6,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "ip",
			},
		},
		CheckDestroy: func(_ *terraformState) error {
			if _, ok := m.get("subnet_mac", fxServer1V6); ok {
				return fmt.Errorf("subnet MAC was not reset on destroy")
			}
			if s, _ := m.get("subnet", fxServer1V6); s["traffic_monthly"] != int64(7) {
				return fmt.Errorf("destroy must only forget the subnet, got %v", s)
			}
			return nil
		},
	})
}

func TestAccMockSubnetCancellation(t *testing.T) {
	m := newMockRobot(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
resource "hetznerrobot_subnet_cancellation" "v4" {
  ip                = %q
  cancellation_date = "2026-12-31"
}
data "hetznerrobot_subnet_cancellation" "v4" {
  ip = hetznerrobot_subnet_cancellation.v4.ip
}`, fxSubnetV4),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hetznerrobot_subnet_cancellation.v4", "cancelled", "true"),
					resource.TestCheckResourceAttr("hetznerrobot_subnet_cancellation.v4", "mask", "29"),
					resource.TestCheckResourceAttr("hetznerrobot_subnet_cancellation.v4", "effective_date", "2026-12-31"),
					resource.TestCheckResourceAttr("data.hetznerrobot_subnet_cancellation.v4", "mask", "29"),
					resource.TestCheckResourceAttr("data.hetznerrobot_subnet_cancellation.v4", "cancelled", "true"),
				),
			},
			{
				ResourceName:                         "hetznerrobot_subnet_cancellation.v4",
				ImportState:                          true,
				ImportStateId:                        fxSubnetV4,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "ip",
			},
		},
		CheckDestroy: func(_ *terraformState) error {
			if _, ok := m.get("subnet_cancellation", fxSubnetV4); ok {
				return fmt.Errorf("subnet cancellation survived destroy")
			}
			return nil
		},
	})
}

func TestAccMockFailover(t *testing.T) {
	m := newMockRobot(t)
	cfg := func(target string) string {
		return fmt.Sprintf(`
resource "hetznerrobot_failover" "f" {
  ip               = %q
  active_server_ip = %q
}
data "hetznerrobot_failover" "f" {
  ip         = hetznerrobot_failover.f.ip
}
data "hetznerrobot_failovers" "all" {
  depends_on = [hetznerrobot_failover.f]
}`, fxFailover, target)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: cfg(fxServer2IP),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hetznerrobot_failover.f", "active_server_ip", fxServer2IP),
					resource.TestCheckResourceAttr("hetznerrobot_failover.f", "server_number", "321"),
					resource.TestCheckResourceAttr("hetznerrobot_failover.f", "netmask", "255.255.255.255"),
					resource.TestCheckResourceAttr("data.hetznerrobot_failover.f", "active_server_ip", fxServer2IP),
					resource.TestCheckResourceAttr("data.hetznerrobot_failovers.all", "failovers.#", "1"),
				),
			},
			{
				// Two switches still running: the provider waits them out.
				PreConfig: func() {
					m.mu.Lock()
					defer m.mu.Unlock()
					m.put("failover_lock", fxFailover, map[string]any{"n": 2})
				},
				Config: cfg(fxServer1IP),
				Check:  resource.TestCheckResourceAttr("hetznerrobot_failover.f", "active_server_ip", fxServer1IP),
			},
			{
				// Unrouted out of band: the next apply routes it again.
				PreConfig: func() {
					m.mu.Lock()
					defer m.mu.Unlock()
					f, _ := m.get("failover", fxFailover)
					f["active_server_ip"] = nil
				},
				Config: cfg(fxServer1IP),
				Check:  resource.TestCheckResourceAttr("hetznerrobot_failover.f", "active_server_ip", fxServer1IP),
			},
			{
				ResourceName:                         "hetznerrobot_failover.f",
				ImportState:                          true,
				ImportStateId:                        fxFailover,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "ip",
			},
		},
		CheckDestroy: func(_ *terraformState) error {
			if f, _ := m.get("failover", fxFailover); f["active_server_ip"] != nil {
				return fmt.Errorf("failover routing survived destroy: %v", f["active_server_ip"])
			}
			return nil
		},
	})
}

// TestAccMockCancellationOutlivesItsServer pins what happens once a
// cancellation has taken effect and the server is gone: the cancellation stays
// in state as done, and no plan tries to cancel a server that no longer exists.
func TestAccMockCancellationOutlivesItsServer(t *testing.T) {
	m := newMockRobot(t)
	cfg := `
resource "hetznerrobot_server_cancellation" "c" {
  server_number     = 421
  cancellation_date = "now"
  reserve_location  = false
}
resource "hetznerrobot_ip_cancellation" "ip" {
  ip                = "123.123.123.125"
  cancellation_date = "now"
}`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{Config: cfg},
			{
				PreConfig: func() {
					m.mu.Lock()
					defer m.mu.Unlock()
					m.del("server", fxServer2)
					m.del("ip", fxServer1IP2)
				},
				Config:   cfg,
				PlanOnly: true,
			},
		},
	})
}
