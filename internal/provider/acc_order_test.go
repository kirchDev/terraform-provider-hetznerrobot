package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// orderImportIgnore lists what an import cannot read back from a transaction:
// the password, the arguments the provider never overwrites, and the timeouts.
var orderImportIgnore = []string{
	"location", "dist", "lang", "authorized_keys", "password", "comment", "addons", "test", "timeouts",
}

// checkOrders asserts how many orders were posted and that nothing was ever
// deleted or cancelled through an order endpoint.
func checkOrders(m *mockRobot, prefix string, want int) func(*terraform.State) error {
	return func(*terraform.State) error {
		if n := m.count("POST " + prefix); n != want {
			return fmt.Errorf("%d orders posted to %s, want %d", n, prefix, want)
		}
		if n := m.count("DELETE /order"); n != 0 {
			return fmt.Errorf("%d DELETE requests against /order", n)
		}
		return nil
	}
}

func TestAccMockServerOrder(t *testing.T) {
	m := newMockRobot(t)
	cfg := fmt.Sprintf(`
resource "hetznerrobot_ssh_key" "k" {
  name = "tf-acc"
  data = %q
}
resource "hetznerrobot_server_order" "o" {
  product_id      = "EX44"
  location        = "FSN1"
  dist            = "Debian 13 base"
  lang            = "en"
  authorized_keys = [hetznerrobot_ssh_key.k.fingerprint]
  addons          = ["primary_ipv4"]
}`, testKeyData)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hetznerrobot_server_order.o", "status", "ready"),
					resource.TestCheckResourceAttrSet("hetznerrobot_server_order.o", "transaction_id"),
					resource.TestCheckResourceAttrSet("hetznerrobot_server_order.o", "server_number"),
					resource.TestMatchResourceAttr("hetznerrobot_server_order.o", "server_ip", regexp.MustCompile(`^198\.51\.100\.`)),
					resource.TestCheckResourceAttr("hetznerrobot_server_order.o", "host_keys.#", "1"),
					func(s *terraform.State) error {
						n := s.RootModule().Resources["hetznerrobot_server_order.o"].Primary.Attributes["server_number"]
						if _, ok := m.get("server", n); !ok {
							return fmt.Errorf("server %s is not in the account", n)
						}
						return nil
					},
				),
			},
			{
				ResourceName:                         "hetznerrobot_server_order.o",
				ImportState:                          true,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "transaction_id",
				ImportStateIdFunc:                    stateAttr("hetznerrobot_server_order.o", "transaction_id"),
				ImportStateVerifyIgnore:              orderImportIgnore,
			},
		},
		CheckDestroy: checkOrders(m, "/order/server/transaction", 1),
	})
}

// TestAccMockServerOrderTimeout pins the rule that a placed order is never
// placed twice: a wait that ends early saves the state with a warning, and the
// next plan is empty.
func TestAccMockServerOrderTimeout(t *testing.T) {
	m := newMockRobot(t)
	cfg := fmt.Sprintf(`
resource "hetznerrobot_server_order" "o" {
  product_id = %q
  location   = "FSN1"
  password   = "Correct-Horse-Battery-1"
  timeouts {
    create = "50ms"
  }
}`, mockOrderSlow)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hetznerrobot_server_order.o", "status", "in process"),
					resource.TestCheckNoResourceAttr("hetznerrobot_server_order.o", "server_number"),
				),
			},
			{Config: cfg, PlanOnly: true},
		},
		CheckDestroy: checkOrders(m, "/order/server/transaction", 1),
	})
}

func TestAccMockServerOrderTestMode(t *testing.T) {
	m := newMockRobot(t)
	servers := len(m.list("server", nil))
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: `
resource "hetznerrobot_server_order" "o" {
  product_id = "AX41-NVMe"
  password   = "Correct-Horse-Battery-1"
  test       = true
}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hetznerrobot_server_order.o", "status", "in process"),
					resource.TestCheckNoResourceAttr("hetznerrobot_server_order.o", "server_number"),
					func(*terraform.State) error {
						if n := len(m.list("server", nil)); n != servers {
							return fmt.Errorf("a test order created a server (%d servers, want %d)", n, servers)
						}
						return nil
					},
				),
			},
		},
	})
}

func TestAccMockServerOrderErrors(t *testing.T) {
	newMockRobot(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: `
resource "hetznerrobot_server_order" "o" {
  product_id = "EX44"
  location   = "FSN1"
}`,
				ExpectError: regexp.MustCompile(`Missing root access`),
			},
			{
				Config: fmt.Sprintf(`
resource "hetznerrobot_server_order" "o" {
  product_id = %q
  password   = "Correct-Horse-Battery-1"
}`, mockOrderNotEnabled),
				ExpectError: regexp.MustCompile(`PRECONDITION_FAILED`),
			},
		},
	})
}

func TestAccMockMarketOrder(t *testing.T) {
	m := newMockRobot(t)
	cfg := fmt.Sprintf(`
data "hetznerrobot_server_market_product" "p" {
  id = %d
}
resource "hetznerrobot_server_market_order" "o" {
  product_id = data.hetznerrobot_server_market_product.p.id
  password   = "Correct-Horse-Battery-1"
}`, mockMarketProduct)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hetznerrobot_server_market_order.o", "status", "ready"),
					resource.TestCheckResourceAttrSet("hetznerrobot_server_market_order.o", "server_number"),
					// No primary_ipv4 addon: an IPv6-only server.
					resource.TestCheckNoResourceAttr("hetznerrobot_server_market_order.o", "server_ip"),
				),
			},
			{
				ResourceName:                         "hetznerrobot_server_market_order.o",
				ImportState:                          true,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "transaction_id",
				ImportStateIdFunc:                    stateAttr("hetznerrobot_server_market_order.o", "transaction_id"),
				ImportStateVerifyIgnore:              orderImportIgnore,
			},
		},
		CheckDestroy: checkOrders(m, "/order/server_market/transaction", 1),
	})
}

func TestAccMockAddonOrder(t *testing.T) {
	m := newMockRobot(t)
	cfg := func(product, reason string) string {
		return fmt.Sprintf(`
data "hetznerrobot_server_addon_products" "p" {
  server_number = %s
}
resource "hetznerrobot_server_addon_order" "ip" {
  server_number = %s
  product_id    = %q
  reason        = %s
}`, fxServer1, fxServer1, product, reason)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config:      cfg("subnet_ipv4_29", "null"),
				ExpectError: regexp.MustCompile(`missing: reason`),
			},
			{
				Config: cfg("additional_ipv4", `"Virtual machines"`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.hetznerrobot_server_addon_products.p", "products.#", "3"),
					resource.TestCheckResourceAttr("data.hetznerrobot_server_addon_products.p", "products.0.type", "ip_ipv4"),
					resource.TestCheckResourceAttr("data.hetznerrobot_server_addon_products.p", "products.0.price.setup_net", "0.0000"),
					resource.TestCheckResourceAttr("hetznerrobot_server_addon_order.ip", "status", "ready"),
					resource.TestCheckResourceAttr("hetznerrobot_server_addon_order.ip", "product_type", "ip_ipv4"),
					resource.TestCheckResourceAttr("hetznerrobot_server_addon_order.ip", "resources.#", "1"),
					resource.TestCheckResourceAttr("hetznerrobot_server_addon_order.ip", "resources.0.type", "ip"),
					func(s *terraform.State) error {
						ip := s.RootModule().Resources["hetznerrobot_server_addon_order.ip"].Primary.Attributes["resources.0.id"]
						if _, ok := m.get("ip", ip); !ok {
							return fmt.Errorf("ordered IP %s is not in the account", ip)
						}
						return nil
					},
				),
			},
			{
				ResourceName:                         "hetznerrobot_server_addon_order.ip",
				ImportState:                          true,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "transaction_id",
				ImportStateIdFunc:                    stateAttr("hetznerrobot_server_addon_order.ip", "transaction_id"),
				// The transaction read does not carry the product type.
				ImportStateVerifyIgnore: []string{"reason", "gateway", "test", "timeouts", "product_type"},
			},
		},
		// Two posts: the refused one without a reason, and the order itself.
		CheckDestroy: checkOrders(m, "/order/server_addon/transaction", 2),
	})
}

func TestAccMockProducts(t *testing.T) {
	newMockRobot(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories(),
		Steps: []resource.TestStep{{
			Config: fmt.Sprintf(`
data "hetznerrobot_server_products" "all" {}
data "hetznerrobot_server_products" "cheap" {
  max_price = 40
}
data "hetznerrobot_server_products" "nbg" {
  location        = "NBG1"
  min_price_setup = 50
}
data "hetznerrobot_server_product" "ex44" {
  id = "EX44"
}
data "hetznerrobot_server_market_products" "all" {}
data "hetznerrobot_server_market_product" "one" {
  id = %d
}
data "hetznerrobot_currency" "c" {}`, mockMarketProduct),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.hetznerrobot_server_products.all", "products.#", "2"),
				resource.TestCheckResourceAttr("data.hetznerrobot_server_products.cheap", "products.#", "1"),
				resource.TestCheckResourceAttr("data.hetznerrobot_server_products.cheap", "products.0.id", "AX41-NVMe"),
				resource.TestCheckResourceAttr("data.hetznerrobot_server_products.nbg", "products.0.id", "EX44"),
				resource.TestCheckResourceAttr("data.hetznerrobot_server_product.ex44", "locations.#", "2"),
				resource.TestCheckResourceAttr("data.hetznerrobot_server_product.ex44", "prices.0.monthly_net", "44.0000"),
				resource.TestCheckNoResourceAttr("data.hetznerrobot_server_product.ex44", "prices.0.hourly_net"),
				resource.TestCheckResourceAttr("data.hetznerrobot_server_product.ex44", "orderable_addons.0.id", "primary_ipv4"),
				resource.TestCheckResourceAttr("data.hetznerrobot_server_product.ex44", "orderable_addons.0.prices.0.hourly_net", "0.0795"),
				resource.TestCheckResourceAttr("data.hetznerrobot_server_market_products.all", "products.#", "2"),
				resource.TestCheckResourceAttr("data.hetznerrobot_server_market_product.one", "name", "SB109"),
				resource.TestCheckResourceAttr("data.hetznerrobot_server_market_product.one", "memory_size", "64"),
				resource.TestCheckResourceAttr("data.hetznerrobot_server_market_product.one", "fixed_price", "false"),
				resource.TestCheckResourceAttr("data.hetznerrobot_currency.c", "currency", "EUR"),
			),
		}},
	})
}

func TestAccMockTraffic(t *testing.T) {
	newMockRobot(t)
	cfg := func(typ, from, to, extra string) string {
		return fmt.Sprintf(`
data "hetznerrobot_traffic" "t" {
  type    = %q
  from    = %q
  to      = %q
  ips     = [%q, "192.0.2.99"]
  subnets = [%q]
  %s
}`, typ, from, to, fxServer1IP, fxSubnetV4, extra)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config:      cfg("day", "2026-09-01", "2026-09-07", ""),
				ExpectError: regexp.MustCompile(`Invalid interval`),
			},
			{
				Config: cfg("month", "2026-09-01", "2026-09-07", ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					// The unknown address is left out.
					resource.TestCheckResourceAttr("data.hetznerrobot_traffic.t", "entries.#", "2"),
					resource.TestCheckResourceAttr("data.hetznerrobot_traffic.t", "entries.0.address", fxServer1IP),
					resource.TestCheckNoResourceAttr("data.hetznerrobot_traffic.t", "entries.0.interval"),
					resource.TestCheckResourceAttr("data.hetznerrobot_traffic.t", "entries.0.sum", "14"),
					resource.TestCheckResourceAttr("data.hetznerrobot_traffic.t", "entries.1.address", fxSubnetV4+"/29"),
				),
			},
			{
				Config: cfg("month", "2026-09-01", "2026-09-07", "single_values = true"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.hetznerrobot_traffic.t", "entries.#", "14"),
					resource.TestCheckResourceAttr("data.hetznerrobot_traffic.t", "entries.0.interval", "01"),
					resource.TestCheckResourceAttr("data.hetznerrobot_traffic.t", "entries.6.interval", "07"),
					resource.TestCheckResourceAttr("data.hetznerrobot_traffic.t", "entries.1.in", "3"),
				),
			},
			{
				Config: `
data "hetznerrobot_traffic" "t" {
  type = "year"
  from = "2026-01"
  to   = "2026-09"
  ips  = ["192.0.2.99"]
}`,
				Check: resource.TestCheckResourceAttr("data.hetznerrobot_traffic.t", "entries.#", "0"),
			},
		},
	})
}

// TestAccMockOrderImportOrdersNothing imports orders into a configuration that
// sets more than an import can read back (keys, reason) and plans and applies
// it: nothing may be ordered again, and the in-place update must not leave the
// computed values unknown.
func TestAccMockOrderImportOrdersNothing(t *testing.T) {
	m := newMockRobot(t)
	cfg := fmt.Sprintf(`
resource "hetznerrobot_ssh_key" "k" {
  name = "tf-acc"
  data = %q
}
resource "hetznerrobot_server_order" "o" {
  product_id      = "EX44"
  location        = "FSN1"
  authorized_keys = [hetznerrobot_ssh_key.k.fingerprint]
  addons          = ["primary_ipv4"]
}
resource "hetznerrobot_server_addon_order" "ip" {
  server_number = %s
  product_id    = "additional_ipv4"
  reason        = "Virtual machines"
}`, testKeyData, fxServer1)
	var serverTx, addonTx string
	capture := func(s *terraform.State) error {
		serverTx = s.RootModule().Resources["hetznerrobot_server_order.o"].Primary.Attributes["transaction_id"]
		addonTx = s.RootModule().Resources["hetznerrobot_server_addon_order.ip"].Primary.Attributes["transaction_id"]
		return nil
	}
	// Place the orders, then forget them: destroy only removes them from state.
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories(),
		Steps:                    []resource.TestStep{{Config: cfg, Check: capture}},
	})
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config:        cfg,
				ResourceName:  "hetznerrobot_server_order.o",
				ImportState:   true,
				ImportStateId: "B-unknown",
				ExpectError:   regexp.MustCompile(`Cannot import server order`),
			},
			{Config: cfg, ResourceName: "hetznerrobot_server_order.o", ImportState: true, ImportStatePersist: true,
				ImportStateIdFunc: func(*terraform.State) (string, error) { return serverTx, nil }},
			{Config: cfg, ResourceName: "hetznerrobot_server_addon_order.ip", ImportState: true, ImportStatePersist: true,
				ImportStateIdFunc: func(*terraform.State) (string, error) { return addonTx, nil }},
			{
				Config: cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hetznerrobot_server_order.o", "status", "ready"),
					resource.TestCheckResourceAttrSet("hetznerrobot_server_order.o", "server_number"),
					resource.TestCheckResourceAttr("hetznerrobot_server_addon_order.ip", "resources.#", "1"),
				),
			},
			{Config: cfg, PlanOnly: true},
		},
		CheckDestroy: func(*terraform.State) error {
			if n := m.count("POST /order/server/transaction"); n != 1 {
				return fmt.Errorf("%d server orders posted, want 1", n)
			}
			if n := m.count("POST /order/server_addon/transaction"); n != 1 {
				return fmt.Errorf("%d addon orders posted, want 1", n)
			}
			return nil
		},
	})
}
