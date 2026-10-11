package provider

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"math/big"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// The live tests run against the real Robot webservice. They never run by
// accident: besides TF_ACC they need HETZNER_ROBOT_LIVE=1 and the credentials
// of a webservice user.
//
//	TF_ACC=1 HETZNER_ROBOT_LIVE=1 HETZNER_ROBOT_USER=… HETZNER_ROBOT_PASSWORD=… \
//	  go test ./internal/provider -run TestLive -v
//
// Everything that touches more than throwaway entities is opt-in on top:
//
//	HETZNER_ROBOT_LIVE_SERVER=<number>  reads that server's boot, reset and firewall
//	                                    data and sets a reverse DNS entry inside its
//	                                    IPv6 /64 (and deletes it again)
//	HETZNER_ROBOT_LIVE_VSWITCH=1        creates a vSwitch and cancels it "now"
//	HETZNER_ROBOT_LIVE_ORDER=1          places a server order with test = true —
//	                                    Robot validates it but processes nothing;
//	                                    ordering must be enabled for the user
//
// Nothing here resets, reinstalls, cancels or reroutes a server, changes a
// firewall or a failover IP, or places a real order — those stay mock-only.
func liveEnv(t *testing.T) {
	t.Helper()
	if os.Getenv("HETZNER_ROBOT_LIVE") != "1" || os.Getenv("HETZNER_ROBOT_USER") == "" || os.Getenv("HETZNER_ROBOT_PASSWORD") == "" {
		t.Skip("set HETZNER_ROBOT_LIVE=1, HETZNER_ROBOT_USER and HETZNER_ROBOT_PASSWORD to run live tests against the real webservice")
	}
	accEnv(t)
}

func liveOptIn(t *testing.T, env string) string {
	t.Helper()
	v := os.Getenv(env)
	if v == "" {
		t.Skipf("set %s to run this live test", env)
	}
	return v
}

// TestLiveReadOnly reads every account-wide data source. An account without
// entries of a kind is fine — Robot answers that with a 404 the provider turns
// into an empty list.
func TestLiveReadOnly(t *testing.T) {
	liveEnv(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories(),
		Steps: []resource.TestStep{{
			Config: `
data "hetznerrobot_servers" "all" {}
data "hetznerrobot_ips" "all" {}
data "hetznerrobot_subnets" "all" {}
data "hetznerrobot_failovers" "all" {}
data "hetznerrobot_rdns_entries" "all" {}
data "hetznerrobot_ssh_keys" "all" {}
data "hetznerrobot_resets" "all" {}
data "hetznerrobot_storageboxes" "all" {}
data "hetznerrobot_vswitches" "all" {}
data "hetznerrobot_firewall_templates" "all" {}
data "hetznerrobot_currency" "c" {}
data "hetznerrobot_server_products" "all" {}
data "hetznerrobot_server_market_products" "all" {}
`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttrSet("data.hetznerrobot_currency.c", "currency"),
				resource.TestCheckResourceAttrSet("data.hetznerrobot_servers.all", "servers.#"),
				resource.TestCheckResourceAttrSet("data.hetznerrobot_server_products.all", "products.0.id"),
			),
		}},
	})
}

// liveSSHKey returns a fresh ed25519 public key in OpenSSH format.
func liveSSHKey(t *testing.T) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var blob []byte
	for _, part := range [][]byte{[]byte("ssh-ed25519"), pub} {
		blob = binary.BigEndian.AppendUint32(blob, uint32(len(part)))
		blob = append(blob, part...)
	}
	return "ssh-ed25519 " + base64.StdEncoding.EncodeToString(blob)
}

func TestLiveSSHKey(t *testing.T) {
	liveEnv(t)
	name := "tf-acc-" + acctest.RandString(8)
	data := liveSSHKey(t)
	cfg := func(n string) string {
		return fmt.Sprintf(`
resource "hetznerrobot_ssh_key" "k" {
  name = %q
  data = %q
}
data "hetznerrobot_ssh_key" "by_name" {
  name = hetznerrobot_ssh_key.k.name
}`, n, data)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: cfg(name),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("hetznerrobot_ssh_key.k", "fingerprint"),
					resource.TestCheckResourceAttr("hetznerrobot_ssh_key.k", "type", "ED25519"),
					resource.TestCheckResourceAttrPair("data.hetznerrobot_ssh_key.by_name", "fingerprint", "hetznerrobot_ssh_key.k", "fingerprint"),
				),
			},
			{
				Config: cfg(name + "-renamed"),
				Check:  resource.TestCheckResourceAttr("hetznerrobot_ssh_key.k", "name", name+"-renamed"),
			},
			{
				ResourceName:                         "hetznerrobot_ssh_key.k",
				ImportState:                          true,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "fingerprint",
				ImportStateIdFunc:                    stateAttr("hetznerrobot_ssh_key.k", "fingerprint"),
				ImportStateVerifyIgnore:              []string{"data"},
			},
		},
	})
}

func TestLiveFirewallTemplate(t *testing.T) {
	liveEnv(t)
	name := "tf-acc-" + acctest.RandString(8)
	cfg := func(port string) string {
		return fmt.Sprintf(`
resource "hetznerrobot_firewall_template" "t" {
  name          = %q
  whitelist_hos = true
  filter_ipv6   = false
  is_default    = false
  input_rules = [
    { name = "ssh", ip_version = "ipv4", dst_port = %q, protocol = "tcp", action = "accept" },
  ]
  output_rules = [
    { name = "allow all", action = "accept" },
  ]
}`, name, port)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: cfg("22"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hetznerrobot_firewall_template.t", "name", name),
					resource.TestCheckResourceAttr("hetznerrobot_firewall_template.t", "input_rules.0.dst_port", "22"),
				),
			},
			{
				Config: cfg("2222"),
				Check:  resource.TestCheckResourceAttr("hetznerrobot_firewall_template.t", "input_rules.0.dst_port", "2222"),
			},
			{
				ResourceName:      "hetznerrobot_firewall_template.t",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

func TestLiveServer(t *testing.T) {
	liveEnv(t)
	server := liveOptIn(t, "HETZNER_ROBOT_LIVE_SERVER")
	cfg := func(ptr string) string {
		return fmt.Sprintf(`
data "hetznerrobot_server" "s" {
  server_number = %[1]s
}
data "hetznerrobot_boot" "b" {
  server_number = %[1]s
}
data "hetznerrobot_reset" "r" {
  server_number = %[1]s
}
resource "hetznerrobot_rdns" "v6" {
  ip  = "${data.hetznerrobot_server.s.server_ipv6_net}dead:beef"
  ptr = %[2]q
}`, server, ptr)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: cfg("tf-acc-" + acctest.RandString(6) + ".invalid"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.hetznerrobot_server.s", "server_number", server),
					resource.TestCheckResourceAttrSet("data.hetznerrobot_reset.r", "types.#"),
					resource.TestCheckResourceAttrSet("hetznerrobot_rdns.v6", "ptr"),
				),
			},
			{
				Config: cfg("tf-acc-renamed.invalid"),
				Check:  resource.TestCheckResourceAttr("hetznerrobot_rdns.v6", "ptr", "tf-acc-renamed.invalid"),
			},
		},
	})
}

func TestLiveVSwitch(t *testing.T) {
	liveEnv(t)
	liveOptIn(t, "HETZNER_ROBOT_LIVE_VSWITCH")
	n, err := rand.Int(rand.Reader, big.NewInt(92))
	if err != nil {
		t.Fatal(err)
	}
	vlan := 4000 + n.Int64()
	name := "tf-acc-" + acctest.RandString(8)
	cfg := func(n string) string {
		return fmt.Sprintf(`
resource "hetznerrobot_vswitch" "v" {
  name    = %q
  vlan    = %d
  servers = []
}`, n, vlan)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: cfg(name),
				Check:  resource.TestCheckResourceAttr("hetznerrobot_vswitch.v", "vlan", fmt.Sprint(vlan)),
			},
			{
				Config: cfg(name + "-renamed"),
				Check:  resource.TestCheckResourceAttr("hetznerrobot_vswitch.v", "name", name+"-renamed"),
			},
			{
				ResourceName:            "hetznerrobot_vswitch.v",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"cancellation_date"},
			},
		},
	})
}

// TestLiveServerTestOrder places an order with test = true: Robot validates
// product, location and key, and processes nothing.
func TestLiveServerTestOrder(t *testing.T) {
	liveEnv(t)
	liveOptIn(t, "HETZNER_ROBOT_LIVE_ORDER")
	name := "tf-acc-" + acctest.RandString(8)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories(),
		Steps: []resource.TestStep{{
			Config: fmt.Sprintf(`
data "hetznerrobot_server_products" "all" {}
resource "hetznerrobot_ssh_key" "k" {
  name = %q
  data = %q
}
resource "hetznerrobot_server_order" "o" {
  product_id      = data.hetznerrobot_server_products.all.products[0].id
  location        = data.hetznerrobot_server_products.all.products[0].locations[0]
  authorized_keys = [hetznerrobot_ssh_key.k.fingerprint]
  test            = true
}`, name, liveSSHKey(t)),
			Check: resource.TestCheckResourceAttrSet("hetznerrobot_server_order.o", "transaction_id"),
		}},
	})
}

// TestAccMockLiveSuite runs the live tests' configurations against the mock, so
// CI covers them without an account and a broken live test shows up before
// someone spends a real run on it.
func TestAccMockLiveSuite(t *testing.T) {
	for name, live := range map[string]func(*testing.T){
		"ReadOnly":         TestLiveReadOnly,
		"SSHKey":           TestLiveSSHKey,
		"FirewallTemplate": TestLiveFirewallTemplate,
		"Server":           TestLiveServer,
		"VSwitch":          TestLiveVSwitch,
		"ServerTestOrder":  TestLiveServerTestOrder,
	} {
		t.Run(name, func(t *testing.T) {
			newMockRobot(t)
			t.Setenv("HETZNER_ROBOT_LIVE", "1")
			t.Setenv("HETZNER_ROBOT_LIVE_SERVER", fxServer1)
			t.Setenv("HETZNER_ROBOT_LIVE_VSWITCH", "1")
			t.Setenv("HETZNER_ROBOT_LIVE_ORDER", "1")
			live(t)
		})
	}
}
