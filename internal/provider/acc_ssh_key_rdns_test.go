package provider

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

const testKeyData = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIHetznerRobotProviderTestKeyOnlyForTheMock tf-acc"

func TestAccMockSSHKey(t *testing.T) {
	newMockRobot(t)
	cfg := func(name string) string {
		return fmt.Sprintf(`
resource "hetznerrobot_ssh_key" "k" {
  name = %q
  data = %q
}
data "hetznerrobot_ssh_key" "by_name" {
  name       = hetznerrobot_ssh_key.k.name
}
data "hetznerrobot_ssh_key" "by_fp" {
  fingerprint = hetznerrobot_ssh_key.k.fingerprint
}
data "hetznerrobot_ssh_keys" "all" {
  depends_on = [hetznerrobot_ssh_key.k]
}`, name, testKeyData)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: cfg("tf-acc"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hetznerrobot_ssh_key.k", "fingerprint", mockFingerprint(testKeyData)),
					resource.TestCheckResourceAttr("hetznerrobot_ssh_key.k", "type", "ED25519"),
					// Robot drops the comment; the configured data stays in state.
					resource.TestCheckResourceAttr("hetznerrobot_ssh_key.k", "data", testKeyData),
					resource.TestCheckResourceAttrPair("data.hetznerrobot_ssh_key.by_name", "fingerprint", "hetznerrobot_ssh_key.k", "fingerprint"),
					resource.TestCheckResourceAttr("data.hetznerrobot_ssh_key.by_fp", "name", "tf-acc"),
					resource.TestCheckResourceAttr("data.hetznerrobot_ssh_keys.all", "keys.#", "1"),
				),
			},
			{
				Config: cfg("tf-acc-renamed"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hetznerrobot_ssh_key.k", "name", "tf-acc-renamed"),
					resource.TestCheckResourceAttr("data.hetznerrobot_ssh_key.by_fp", "name", "tf-acc-renamed"),
				),
			},
			{
				ResourceName:                         "hetznerrobot_ssh_key.k",
				ImportState:                          true,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "fingerprint",
				ImportStateIdFunc:                    stateAttr("hetznerrobot_ssh_key.k", "fingerprint"),
				// An import has no configuration to keep, so it holds Robot's form.
				ImportStateVerifyIgnore: []string{"data"},
			},
		},
	})
}

func TestAccMockRDNS(t *testing.T) {
	m := newMockRobot(t)
	cfg := func(ptr string) string {
		return fmt.Sprintf(`
resource "hetznerrobot_rdns" "v6" {
  ip  = "2a01:4f8:111:4221::1"
  ptr = %q
}
data "hetznerrobot_rdns" "one" {
  ip         = hetznerrobot_rdns.v6.ip
}
data "hetznerrobot_rdns_entries" "server1" {
  server_ip  = %q
  depends_on = [hetznerrobot_rdns.v6]
}
data "hetznerrobot_rdns_entries" "all" {
  depends_on = [hetznerrobot_rdns.v6]
}`, ptr, fxServer1IP)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: cfg("v6.example.com"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hetznerrobot_rdns.v6", "ptr", "v6.example.com"),
					resource.TestCheckResourceAttr("data.hetznerrobot_rdns.one", "ptr", "v6.example.com"),
					resource.TestCheckResourceAttr("data.hetznerrobot_rdns_entries.server1", "entries.#", "2"),
					resource.TestCheckResourceAttr("data.hetznerrobot_rdns_entries.all", "entries.#", "2"),
				),
			},
			{
				Config: cfg("v6-renamed.example.com"),
				Check:  resource.TestCheckResourceAttr("hetznerrobot_rdns.v6", "ptr", "v6-renamed.example.com"),
			},
			{
				ResourceName:                         "hetznerrobot_rdns.v6",
				ImportState:                          true,
				ImportStateId:                        "2a01:4f8:111:4221::1",
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "ip",
			},
		},
		CheckDestroy: func(_ *terraformState) error {
			if _, ok := m.get("rdns", "2a01:4f8:111:4221::1"); ok {
				return fmt.Errorf("reverse DNS entry still exists after destroy")
			}
			return nil
		},
	})
}
