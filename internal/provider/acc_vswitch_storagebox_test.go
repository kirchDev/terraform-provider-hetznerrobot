package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccMockVSwitch(t *testing.T) {
	m := newMockRobot(t)
	cfg := func(name string, vlan int, servers string) string {
		return fmt.Sprintf(`
resource "hetznerrobot_vswitch" "v" {
  name    = %q
  vlan    = %d
  servers = %s
}
data "hetznerrobot_vswitch" "one" {
  id = hetznerrobot_vswitch.v.id
}
data "hetznerrobot_vswitches" "all" {
  depends_on = [hetznerrobot_vswitch.v]
}`, name, vlan, servers)
	}
	var id string
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: cfg("tf-acc", 4000, "[321]"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("hetznerrobot_vswitch.v", "id"),
					resource.TestCheckResourceAttr("hetznerrobot_vswitch.v", "servers.#", "1"),
					resource.TestCheckResourceAttr("hetznerrobot_vswitch.v", "server_details.0.status", "ready"),
					resource.TestCheckResourceAttr("hetznerrobot_vswitch.v", "server_details.0.server_ip", fxServer1IP),
					resource.TestCheckResourceAttr("hetznerrobot_vswitch.v", "cancellation_date", "now"),
					resource.TestCheckResourceAttr("data.hetznerrobot_vswitch.one", "servers.#", "1"),
					resource.TestCheckResourceAttr("data.hetznerrobot_vswitches.all", "vswitches.#", "1"),
					resource.TestCheckResourceAttr("data.hetznerrobot_vswitches.all", "vswitches.0.vlan", "4000"),
					func(s *terraformState) error {
						id = s.RootModule().Resources["hetznerrobot_vswitch.v"].Primary.Attributes["id"]
						return nil
					},
				),
			},
			{
				Config: cfg("tf-acc-renamed", 4001, "[321, 421]"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hetznerrobot_vswitch.v", "name", "tf-acc-renamed"),
					resource.TestCheckResourceAttr("hetznerrobot_vswitch.v", "vlan", "4001"),
					resource.TestCheckResourceAttr("hetznerrobot_vswitch.v", "servers.#", "2"),
					resource.TestCheckResourceAttr("data.hetznerrobot_vswitch.one", "server_details.#", "2"),
				),
			},
			{
				Config: cfg("tf-acc-renamed", 4001, "[421]"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hetznerrobot_vswitch.v", "servers.#", "1"),
					resource.TestCheckTypeSetElemAttr("hetznerrobot_vswitch.v", "servers.*", "421"),
				),
			},
			{
				ResourceName:            "hetznerrobot_vswitch.v",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"cancellation_date"},
			},
		},
		CheckDestroy: func(_ *terraformState) error {
			v, ok := m.get("vswitch", id)
			if !ok || v["cancelled"] != true {
				return fmt.Errorf("vSwitch %s was not cancelled on destroy", id)
			}
			return nil
		},
	})
}

// TestAccMockVSwitchServersUnmanaged leaves servers out: servers attached
// elsewhere must not plan a change.
func TestAccMockVSwitchServersUnmanaged(t *testing.T) {
	m := newMockRobot(t)
	cfg := `
resource "hetznerrobot_vswitch" "v" {
  name = "unmanaged"
  vlan = 4010
}`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{Config: cfg, Check: resource.TestCheckResourceAttr("hetznerrobot_vswitch.v", "servers.#", "0")},
			{
				PreConfig: func() {
					m.mu.Lock()
					defer m.mu.Unlock()
					for _, v := range m.list("vswitch", nil) {
						v["server"] = []map[string]any{{"server_ip": fxServer2IP, "server_ipv6_net": fxServer2V6, "server_number": int64(421), "status": "ready"}}
					}
				},
				Config: cfg,
				Check:  resource.TestCheckResourceAttr("hetznerrobot_vswitch.v", "servers.#", "1"),
			},
		},
	})
}

func TestAccMockStorageBox(t *testing.T) {
	m := newMockRobot(t)
	cfg := func(name string, samba bool, password string) string {
		pw := ""
		if password != "" {
			pw = fmt.Sprintf("password = %q", password)
		}
		return fmt.Sprintf(`
resource "hetznerrobot_storagebox" "b" {
  id    = 123456
  name  = %q
  samba = %t
  %s
}
data "hetznerrobot_storagebox" "one" {
  id         = hetznerrobot_storagebox.b.id
}
data "hetznerrobot_storageboxes" "all" {}
data "hetznerrobot_storageboxes" "server2" {
  linked_server = 421
}`, name, samba, pw)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: cfg("backup", true, ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hetznerrobot_storagebox.b", "samba", "true"),
					resource.TestCheckResourceAttr("hetznerrobot_storagebox.b", "ssh", "true"),
					resource.TestCheckResourceAttr("hetznerrobot_storagebox.b", "login", "u12345"),
					resource.TestCheckResourceAttr("hetznerrobot_storagebox.b", "linked_server", "321"),
					resource.TestCheckNoResourceAttr("hetznerrobot_storagebox.b", "password"),
					resource.TestCheckResourceAttr("data.hetznerrobot_storagebox.one", "name", "backup"),
					resource.TestCheckResourceAttr("data.hetznerrobot_storageboxes.all", "storageboxes.#", "1"),
					resource.TestCheckResourceAttr("data.hetznerrobot_storageboxes.server2", "storageboxes.#", "0"),
				),
			},
			{
				Config: cfg("backup-renamed", false, "Sup3r-Secret-Pw"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hetznerrobot_storagebox.b", "name", "backup-renamed"),
					resource.TestCheckResourceAttr("hetznerrobot_storagebox.b", "samba", "false"),
					func(_ *terraformState) error {
						if b, _ := m.get("storagebox", fxStorageBox); b["_password"] != "Sup3r-Secret-Pw" {
							return fmt.Errorf("password not applied: %v", b["_password"])
						}
						return nil
					},
				),
			},
			{
				ResourceName:            "hetznerrobot_storagebox.b",
				ImportState:             true,
				ImportStateId:           fxStorageBox,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"password"},
			},
		},
		// Destroy only forgets the box.
		CheckDestroy: func(_ *terraformState) error {
			if _, ok := m.get("storagebox", fxStorageBox); !ok {
				return fmt.Errorf("Storage Box disappeared on destroy")
			}
			return nil
		},
	})
}

func TestAccMockStorageBoxRejectsWeakPassword(t *testing.T) {
	newMockRobot(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories(),
		Steps: []resource.TestStep{{
			Config: `
resource "hetznerrobot_storagebox" "b" {
  id       = 123456
  password = "short"
}`,
			ExpectError: regexp.MustCompile(`STORAGEBOX_INVALID_PASSWORD`),
		}},
	})
}

func TestAccMockStorageBoxSnapshot(t *testing.T) {
	m := newMockRobot(t)
	cfg := func(comment string) string {
		c := ""
		if comment != "" {
			c = fmt.Sprintf("comment = %q", comment)
		}
		return fmt.Sprintf(`
resource "hetznerrobot_storagebox_snapshot" "s" {
  storagebox_id = 123456
  %s
}
data "hetznerrobot_storagebox_snapshots" "all" {
  storagebox_id = 123456
  depends_on    = [hetznerrobot_storagebox_snapshot.s]
}`, c)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: cfg("before upgrade"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("hetznerrobot_storagebox_snapshot.s", "name"),
					resource.TestCheckResourceAttr("hetznerrobot_storagebox_snapshot.s", "comment", "before upgrade"),
					resource.TestCheckResourceAttr("hetznerrobot_storagebox_snapshot.s", "automatic", "false"),
					resource.TestCheckResourceAttr("hetznerrobot_storagebox_snapshot.s", "filesystem_size", "500"),
					resource.TestCheckResourceAttr("data.hetznerrobot_storagebox_snapshots.all", "snapshots.#", "1"),
					resource.TestCheckResourceAttr("data.hetznerrobot_storagebox_snapshots.all", "snapshots.0.comment", "before upgrade"),
				),
			},
			{
				Config: cfg("after upgrade"),
				Check:  resource.TestCheckResourceAttr("hetznerrobot_storagebox_snapshot.s", "comment", "after upgrade"),
			},
			{
				Config: cfg(""),
				Check:  resource.TestCheckNoResourceAttr("hetznerrobot_storagebox_snapshot.s", "comment"),
			},
			{
				ResourceName:                         "hetznerrobot_storagebox_snapshot.s",
				ImportState:                          true,
				ImportStateIdFunc:                    stateAttrs("hetznerrobot_storagebox_snapshot.s", "storagebox_id", "name"),
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "name",
			},
		},
		CheckDestroy: func(_ *terraformState) error {
			if n := len(m.list("sbsnapshot", nil)); n != 0 {
				return fmt.Errorf("%d snapshots left after destroy", n)
			}
			return nil
		},
	})
}

func TestAccMockStorageBoxSnapshotPlan(t *testing.T) {
	m := newMockRobot(t)
	cfg := func(body string) string {
		return fmt.Sprintf(`
resource "hetznerrobot_storagebox_snapshot_plan" "p" {
  storagebox_id = 123456
  %s
}
data "hetznerrobot_storagebox_snapshot_plan" "p" {
  storagebox_id = hetznerrobot_storagebox_snapshot_plan.p.storagebox_id
}`, body)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: cfg(`status = "disabled"
  minute = 5`),
				ExpectError: regexp.MustCompile(`Schedule on a disabled snapshot plan`),
			},
			{
				Config:      cfg(`status = "enabled"`),
				ExpectError: regexp.MustCompile(`Missing snapshot plan minute`),
			},
			{
				Config: cfg(`status        = "enabled"
  minute        = 30
  hour          = 3
  max_snapshots = 7`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hetznerrobot_storagebox_snapshot_plan.p", "status", "enabled"),
					resource.TestCheckResourceAttr("hetznerrobot_storagebox_snapshot_plan.p", "hour", "3"),
					resource.TestCheckNoResourceAttr("hetznerrobot_storagebox_snapshot_plan.p", "day_of_week"),
					resource.TestCheckResourceAttr("data.hetznerrobot_storagebox_snapshot_plan.p", "max_snapshots", "7"),
				),
			},
			{
				Config: cfg(`status      = "enabled"
  minute      = 0
  hour        = 4
  day_of_week = 7`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hetznerrobot_storagebox_snapshot_plan.p", "day_of_week", "7"),
					resource.TestCheckResourceAttr("hetznerrobot_storagebox_snapshot_plan.p", "minute", "0"),
					// Left out, max_snapshots keeps the value Robot holds.
					resource.TestCheckResourceAttr("hetznerrobot_storagebox_snapshot_plan.p", "max_snapshots", "7"),
				),
			},
			{
				ResourceName:                         "hetznerrobot_storagebox_snapshot_plan.p",
				ImportState:                          true,
				ImportStateId:                        fxStorageBox,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "storagebox_id",
			},
		},
		CheckDestroy: func(_ *terraformState) error {
			if p, _ := m.get("sbplan", fxStorageBox); p["status"] != "disabled" {
				return fmt.Errorf("snapshot plan still %v after destroy", p["status"])
			}
			return nil
		},
	})
}

func TestAccMockStorageBoxSubaccount(t *testing.T) {
	m := newMockRobot(t)
	cfg := func(dir string, readonly bool, password string) string {
		pw := ""
		if password != "" {
			pw = fmt.Sprintf("password = %q", password)
		}
		return fmt.Sprintf(`
resource "hetznerrobot_storagebox_subaccount" "s" {
  storagebox_id  = 123456
  home_directory = %q
  ssh            = true
  readonly       = %t
  comment        = "tf-acc"
  %s
}
data "hetznerrobot_storagebox_subaccounts" "all" {
  storagebox_id = 123456
  depends_on    = [hetznerrobot_storagebox_subaccount.s]
}`, dir, readonly, pw)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: cfg("backups", false, ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hetznerrobot_storagebox_subaccount.s", "username", "u12345-sub1"),
					resource.TestCheckResourceAttr("hetznerrobot_storagebox_subaccount.s", "password", "GeneratedPassw0rd!"),
					resource.TestCheckResourceAttr("hetznerrobot_storagebox_subaccount.s", "ssh", "true"),
					resource.TestCheckResourceAttr("hetznerrobot_storagebox_subaccount.s", "samba", "false"),
					resource.TestCheckResourceAttr("hetznerrobot_storagebox_subaccount.s", "account_id", "u12345"),
					resource.TestCheckResourceAttr("data.hetznerrobot_storagebox_subaccounts.all", "subaccounts.#", "1"),
					resource.TestCheckResourceAttr("data.hetznerrobot_storagebox_subaccounts.all", "subaccounts.0.home_directory", "backups"),
				),
			},
			{
				Config: cfg("backups/db", true, ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hetznerrobot_storagebox_subaccount.s", "home_directory", "backups/db"),
					resource.TestCheckResourceAttr("hetznerrobot_storagebox_subaccount.s", "readonly", "true"),
					resource.TestCheckResourceAttr("hetznerrobot_storagebox_subaccount.s", "password", "GeneratedPassw0rd!"),
				),
			},
			{
				Config: cfg("backups/db", true, "An0ther-Secret-Pw"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hetznerrobot_storagebox_subaccount.s", "password", "An0ther-Secret-Pw"),
					func(_ *terraformState) error {
						if s, _ := m.get("sbsub", fxStorageBox+"/u12345-sub1"); s["_password"] != "An0ther-Secret-Pw" {
							return fmt.Errorf("password not applied: %v", s["_password"])
						}
						return nil
					},
				),
			},
			{
				ResourceName:                         "hetznerrobot_storagebox_subaccount.s",
				ImportState:                          true,
				ImportStateId:                        fxStorageBox + "/u12345-sub1",
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "username",
				ImportStateVerifyIgnore:              []string{"password"},
			},
		},
		CheckDestroy: func(_ *terraformState) error {
			if n := len(m.list("sbsub", nil)); n != 0 {
				return fmt.Errorf("%d sub-accounts left after destroy", n)
			}
			return nil
		},
	})
}
