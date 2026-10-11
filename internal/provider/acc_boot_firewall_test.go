package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
)

const testBootKey = "15:28:b0:03:95:f0:77:b3:10:56:15:6b:77:22:a5:bb"

// TestAccMockBootRescue activates the Rescue System with a reset, lets the
// server "boot" it, and checks the resource neither disappears nor activates
// it again, and that destroying it no longer deactivates anything.
func TestAccMockBootRescue(t *testing.T) {
	m := newMockRobot(t)
	cfg := fmt.Sprintf(`
resource "hetznerrobot_boot_rescue" "r" {
  server_number   = 321
  os              = "linux"
  authorized_keys = [%q]
  keyboard        = "de"
}
resource "hetznerrobot_server_reset" "boot" {
  server_number = hetznerrobot_boot_rescue.r.server_number
  type          = "hw"
  triggers = {
    rescue = hetznerrobot_boot_rescue.r.password
  }
}
data "hetznerrobot_boot" "b" {
  server_number = 321
  depends_on    = [hetznerrobot_boot_rescue.r]
}`, testBootKey)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hetznerrobot_boot_rescue.r", "active", "true"),
					resource.TestCheckResourceAttrSet("hetznerrobot_boot_rescue.r", "password"),
					resource.TestCheckResourceAttr("hetznerrobot_boot_rescue.r", "server_ip", fxServer1IP),
					resource.TestCheckResourceAttr("hetznerrobot_boot_rescue.r", "host_keys.#", "1"),
					resource.TestCheckResourceAttr("data.hetznerrobot_boot.b", "rescue_active", "true"),
					// While active, Robot reports only the chosen system.
					resource.TestCheckResourceAttr("data.hetznerrobot_boot.b", "rescue_os.#", "1"),
					resource.TestCheckResourceAttr("data.hetznerrobot_boot.b", "linux_dists.#", "2"),
					resource.TestCheckResourceAttr("data.hetznerrobot_boot.b", "windows_os.#", "1"),
					func(*terraformState) error {
						if n := m.count("POST /reset/321"); n != 1 {
							return fmt.Errorf("reset POSTed %d times, want 1", n)
						}
						return nil
					},
				),
			},
			{
				ResourceName:                         "hetznerrobot_boot_rescue.r",
				ImportState:                          true,
				ImportStateId:                        "321",
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "server_number",
				// Robot does not report the keyboard layout back.
				ImportStateVerifyIgnore: []string{"keyboard"},
			},
			{
				// The server boots the Rescue System; Robot clears it.
				PreConfig: func() { m.bootDeactivate(fxServer1) },
				Config:    cfg,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("hetznerrobot_boot_rescue.r", plancheck.ResourceActionNoop),
						plancheck.ExpectResourceAction("hetznerrobot_server_reset.boot", plancheck.ResourceActionNoop),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hetznerrobot_boot_rescue.r", "active", "false"),
					resource.TestCheckResourceAttrSet("hetznerrobot_boot_rescue.r", "password"),
					resource.TestCheckResourceAttr("data.hetznerrobot_boot.b", "rescue_os.#", "2"),
				),
			},
			{
				ResourceName:  "hetznerrobot_boot_rescue.r",
				ImportState:   true,
				ImportStateId: "321",
				ExpectError:   regexp.MustCompile(`not active`),
			},
		},
		CheckDestroy: func(*terraformState) error {
			if n := m.count("DELETE /boot/"); n != 0 {
				return fmt.Errorf("destroy deactivated a used-up configuration (%d DELETEs)", n)
			}
			return nil
		},
	})
}

// TestAccMockBootInstalls covers the Linux, VNC and Windows installations and
// the deactivation of a still pending one on destroy.
func TestAccMockBootInstalls(t *testing.T) {
	m := newMockRobot(t)
	linux := `
resource "hetznerrobot_boot_linux" "l" {
  server_number = 321
  dist          = "Debian 12 base"
  lang          = "en"
}`
	vnc := `
resource "hetznerrobot_boot_vnc" "v" {
  server_number = 421
  dist          = "Fedora-40"
  lang          = "de_DE"
}`
	windows := `
resource "hetznerrobot_boot_windows" "w" {
  server_number = 321
  os            = "Windows Server 2022 Standard Edition"
  lang          = "de"
}`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: linux + vnc,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hetznerrobot_boot_linux.l", "active", "true"),
					resource.TestCheckResourceAttr("hetznerrobot_boot_linux.l", "host_keys.#", "1"),
					resource.TestCheckResourceAttrSet("hetznerrobot_boot_vnc.v", "password"),
				),
			},
			{
				ResourceName:                         "hetznerrobot_boot_vnc.v",
				ImportState:                          true,
				ImportStateId:                        "421",
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "server_number",
			},
			{
				// Dropping the pending Linux installation deactivates it.
				Config: vnc,
				Check: func(*terraformState) error {
					if n := m.count("DELETE /boot/321/linux"); n != 1 {
						return fmt.Errorf("linux DELETEd %d times, want 1", n)
					}
					return nil
				},
			},
			{
				Config: vnc + windows,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hetznerrobot_boot_windows.w", "active", "true"),
					resource.TestCheckResourceAttr("hetznerrobot_boot_windows.w", "lang", "de"),
				),
			},
		},
	})
}

func TestAccMockServerResetAndWakeOnLAN(t *testing.T) {
	m := newMockRobot(t)
	cfg := func(trigger string) string {
		return fmt.Sprintf(`
resource "hetznerrobot_server_reset" "r" {
  server_number = 421
  type          = "power"
  triggers = {
    v = %q
  }
}
resource "hetznerrobot_wake_on_lan" "w" {
  server_number = 321
}
data "hetznerrobot_reset" "one" {
  server_number = 421
}
data "hetznerrobot_resets" "all" {}`, trigger)
	}
	resets := func(want int) func(*terraformState) error {
		return func(*terraformState) error {
			if n := m.count("POST /reset/421"); n != want {
				return fmt.Errorf("reset POSTed %d times, want %d", n, want)
			}
			if n := m.count("POST /wol/321"); n != 1 {
				return fmt.Errorf("Wake on LAN POSTed %d times, want 1", n)
			}
			return nil
		}
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: cfg("1"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resets(1),
					resource.TestCheckResourceAttr("data.hetznerrobot_reset.one", "types.#", "4"),
					resource.TestCheckResourceAttr("data.hetznerrobot_reset.one", "operating_status", "running"),
					resource.TestCheckResourceAttr("data.hetznerrobot_resets.all", "servers.#", "2"),
					resource.TestCheckResourceAttr("data.hetznerrobot_resets.all", "servers.0.types.0", "sw"),
				),
			},
			{Config: cfg("1"), Check: resets(1)},
			{Config: cfg("2"), Check: resets(2)},
			{
				ResourceName:                         "hetznerrobot_server_reset.r",
				ImportState:                          true,
				ImportStateId:                        "421/power",
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "server_number",
				ImportStateVerifyIgnore:              []string{"triggers"},
			},
			{
				ResourceName:                         "hetznerrobot_wake_on_lan.w",
				ImportState:                          true,
				ImportStateId:                        "321",
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "server_number",
			},
		},
	})
}

func TestAccMockWakeOnLANUnavailable(t *testing.T) {
	newMockRobot(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories(),
		Steps: []resource.TestStep{{
			Config:      `resource "hetznerrobot_wake_on_lan" "w" { server_number = 421 }`,
			ExpectError: regexp.MustCompile(`WOL_NOT_AVAILABLE`),
		}},
	})
}

func TestAccMockFirewall(t *testing.T) {
	m := newMockRobot(t)
	v1 := `
resource "hetznerrobot_firewall_template" "t" {
  name          = "tf-acc"
  whitelist_hos = false
  input_rules = [
    { name = "ssh", ip_version = "ipv4", dst_port = "22", protocol = "tcp", action = "accept" },
  ]
}
resource "hetznerrobot_firewall" "f" {
  server_number = 321
  filter_ipv6   = true
  input_rules = [
    { name = "ssh", ip_version = "ipv4", src_ip = "198.51.100.0/24", dst_port = "22", protocol = "tcp", action = "accept" },
    { name = "web", dst_port = "443", action = "accept" },
    { name = "rest", action = "discard" },
  ]
  output_rules = [
    { name = "all", action = "accept" },
  ]
}
resource "hetznerrobot_firewall" "from_template" {
  server_number = 421
  template_id   = hetznerrobot_firewall_template.t.id
}`
	v2 := `
resource "hetznerrobot_firewall_template" "t" {
  name          = "tf-acc-renamed"
  whitelist_hos = false
  input_rules = [
    { name = "ssh", ip_version = "ipv4", dst_port = "22", protocol = "tcp", action = "accept" },
    { name = "http", ip_version = "ipv4", dst_port = "80", protocol = "tcp", action = "accept" },
  ]
}
resource "hetznerrobot_firewall" "f" {
  server_number = 321
  status        = "disabled"
  filter_ipv6   = true
  input_rules = [
    { name = "ssh", ip_version = "ipv4", src_ip = "198.51.100.0/24", dst_port = "22", protocol = "tcp", action = "accept" },
    { name = "dns", ip_version = "ipv4", dst_port = "53", protocol = "udp", action = "accept" },
    { name = "rest", action = "discard" },
  ]
  output_rules = [
    { name = "all", action = "accept" },
  ]
}
resource "hetznerrobot_firewall" "from_template" {
  server_number = 421
  template_id   = hetznerrobot_firewall_template.t.id
}
data "hetznerrobot_firewall" "f" {
  server_number = 321
  depends_on    = [hetznerrobot_firewall.f]
}
data "hetznerrobot_firewall_template" "by_id" {
  id         = hetznerrobot_firewall_template.t.id
}
data "hetznerrobot_firewall_template" "by_name" {
  name       = hetznerrobot_firewall_template.t.name
}
data "hetznerrobot_firewall_templates" "all" {
  depends_on = [hetznerrobot_firewall_template.t]
}`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: v1,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hetznerrobot_firewall.f", "status", "active"),
					resource.TestCheckResourceAttr("hetznerrobot_firewall.f", "whitelist_hos", "true"),
					resource.TestCheckResourceAttr("hetznerrobot_firewall.f", "input_rules.#", "3"),
					resource.TestCheckNoResourceAttr("hetznerrobot_firewall.f", "input_rules.1.ip_version"),
					resource.TestCheckResourceAttr("hetznerrobot_firewall.f", "port", "main"),
					resource.TestCheckResourceAttr("hetznerrobot_firewall_template.t", "name", "tf-acc"),
					resource.TestCheckResourceAttr("hetznerrobot_firewall.from_template", "whitelist_hos", "false"),
					resource.TestCheckResourceAttr("hetznerrobot_firewall.from_template", "input_rules.#", "1"),
					resource.TestCheckResourceAttr("hetznerrobot_firewall.from_template", "output_rules.#", "0"),
				),
			},
			{
				Config: v2,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						// The template changed, but it is applied only when template_id changes.
						plancheck.ExpectResourceAction("hetznerrobot_firewall.from_template", plancheck.ResourceActionNoop),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hetznerrobot_firewall.f", "status", "disabled"),
					resource.TestCheckResourceAttr("hetznerrobot_firewall.f", "input_rules.1.name", "dns"),
					resource.TestCheckResourceAttr("hetznerrobot_firewall_template.t", "input_rules.#", "2"),
					resource.TestCheckResourceAttr("hetznerrobot_firewall.from_template", "input_rules.#", "1"),
					resource.TestCheckResourceAttr("data.hetznerrobot_firewall.f", "input_rules.#", "3"),
					resource.TestCheckResourceAttr("data.hetznerrobot_firewall.f", "input_rules.0.src_ip", "198.51.100.0/24"),
					resource.TestCheckResourceAttr("data.hetznerrobot_firewall_template.by_id", "name", "tf-acc-renamed"),
					resource.TestCheckResourceAttr("data.hetznerrobot_firewall_template.by_id", "input_rules.#", "2"),
					resource.TestCheckResourceAttrPair("data.hetznerrobot_firewall_template.by_name", "id", "hetznerrobot_firewall_template.t", "id"),
					resource.TestCheckResourceAttr("data.hetznerrobot_firewall_templates.all", "templates.#", "1"),
				),
			},
			{
				ResourceName:                         "hetznerrobot_firewall.f",
				ImportState:                          true,
				ImportStateId:                        "321",
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "server_number",
			},
			{
				ResourceName:                         "hetznerrobot_firewall_template.t",
				ImportState:                          true,
				ImportStateIdFunc:                    stateAttr("hetznerrobot_firewall_template.t", "id"),
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "id",
			},
		},
		CheckDestroy: func(*terraformState) error {
			m.mu.Lock()
			defer m.mu.Unlock()
			for _, n := range []string{fxServer1, fxServer2} {
				f, _ := m.get("firewall", n)
				if len(f["input"].([]map[string]any))+len(f["output"].([]map[string]any)) > 0 {
					return fmt.Errorf("firewall of server %s still has rules after destroy", n)
				}
			}
			if len(m.list("fwtemplate", nil)) > 0 {
				return fmt.Errorf("firewall template still exists after destroy")
			}
			return nil
		},
	})
}

// TestAccMockFirewallRuleValidation pins the documented IPv6 limitations as
// plan-time errors.
func TestAccMockFirewallRuleValidation(t *testing.T) {
	newMockRobot(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: `
resource "hetznerrobot_firewall" "f" {
  server_number = 321
  input_rules   = [{ src_ip = "198.51.100.1", action = "accept" }]
}`,
				ExpectError: regexp.MustCompile(`Address filter needs IPv4`),
			},
			{
				Config: `
resource "hetznerrobot_firewall_template" "t" {
  name        = "x"
  input_rules = [{ protocol = "tcp", action = "accept" }]
}`,
				ExpectError: regexp.MustCompile(`Protocol filter needs an IP version`),
			},
		},
	})
}

// TestAccMockFirewallAdoptKeepsRules pins the adoption of a live firewall: a
// configuration that lists no rules and no status must keep both — every POST
// replaces the whole configuration, so sending none would wipe them.
func TestAccMockFirewallAdoptKeepsRules(t *testing.T) {
	m := newMockRobot(t)
	fw, _ := m.get("firewall", fxServer1)
	fw["status"], fw["target"] = "active", "active"
	fw["input"] = []map[string]any{{
		"ip_version": "ipv4", "name": "ssh", "dst_ip": nil, "src_ip": nil, "dst_port": "22",
		"src_port": nil, "protocol": "tcp", "tcp_flags": nil, "action": "accept",
	}}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories(),
		Steps: []resource.TestStep{{
			Config: `
resource "hetznerrobot_firewall" "f" {
  server_number = 321
  filter_ipv6   = true
}`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("hetznerrobot_firewall.f", "status", "active"),
				resource.TestCheckResourceAttr("hetznerrobot_firewall.f", "filter_ipv6", "true"),
				resource.TestCheckResourceAttr("hetznerrobot_firewall.f", "input_rules.#", "1"),
				resource.TestCheckResourceAttr("hetznerrobot_firewall.f", "input_rules.0.dst_port", "22"),
			),
		}},
	})
}
