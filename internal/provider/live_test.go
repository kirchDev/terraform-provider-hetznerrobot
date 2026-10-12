package provider

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/kirchDev/terraform-provider-hetznerrobot/internal/client"
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
//	                                    data, adopts it unchanged and sets a reverse
//	                                    DNS entry inside its IPv6 /64 (and deletes it)
//	HETZNER_ROBOT_LIVE_WRITE=1          with the server: renames it, flips its main
//	                                    IP's traffic warnings, activates and
//	                                    deactivates the Rescue System (no reset)
//	                                    and sends Wake on LAN — all restored
//	HETZNER_ROBOT_LIVE_VSWITCH=1        creates a vSwitch and cancels it "now"
//	HETZNER_ROBOT_LIVE_ORDER=1          reads products and currency and places a
//	                                    server order with test = true — Robot
//	                                    validates it but processes nothing. Needs
//	                                    ordering enabled for the user: without it
//	                                    every /order/* call is a 401, which may
//	                                    count towards the 3-failed-logins IP block
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

// TestLiveReadOnly reads every account-wide data source outside /order. An
// account without entries of a kind is fine — Robot answers that with a 404
// (or, for vSwitches, an empty array) the provider turns into an empty list.
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
`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttrSet("data.hetznerrobot_servers.all", "servers.0.server_number"),
				resource.TestCheckResourceAttrSet("data.hetznerrobot_ips.all", "ips.0.ip"),
				resource.TestCheckResourceAttrSet("data.hetznerrobot_resets.all", "servers.0.types.#"),
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
data "hetznerrobot_firewall" "f" {
  server_number = %[1]s
}
data "hetznerrobot_server_cancellation" "c" {
  server_number = %[1]s
}
data "hetznerrobot_ip" "main" {
  ip = data.hetznerrobot_server.s.server_ip
}
data "hetznerrobot_ip_cancellation" "main" {
  ip = data.hetznerrobot_server.s.server_ip
}
data "hetznerrobot_subnet" "v6" {
  ip = data.hetznerrobot_server.s.server_ipv6_net
}
data "hetznerrobot_traffic" "t" {
  type = "month"
  from = "2026-09-01"
  to   = "2026-09-30"
  ips  = [data.hetznerrobot_server.s.server_ip]
}

# Adoptions that change nothing: the name and the traffic settings stay as
# they are, and destroying the resources only forgets them.
resource "hetznerrobot_server" "s" {
  server_number = %[1]s
  server_name   = data.hetznerrobot_server.s.server_name
}
resource "hetznerrobot_ip" "main" {
  ip = data.hetznerrobot_server.s.server_ip
}
resource "hetznerrobot_subnet" "v6" {
  ip = data.hetznerrobot_server.s.server_ipv6_net
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
					resource.TestCheckResourceAttrSet("data.hetznerrobot_firewall.f", "status"),
					resource.TestCheckResourceAttr("data.hetznerrobot_server_cancellation.c", "cancelled", "false"),
					resource.TestCheckResourceAttrSet("data.hetznerrobot_ip.main", "gateway"),
					resource.TestCheckResourceAttrSet("data.hetznerrobot_ip_cancellation.main", "earliest_cancellation_date"),
					resource.TestCheckResourceAttrSet("data.hetznerrobot_subnet.v6", "mask"),
					resource.TestCheckResourceAttrPair("hetznerrobot_server.s", "server_name", "data.hetznerrobot_server.s", "server_name"),
					resource.TestCheckResourceAttrPair("hetznerrobot_ip.main", "traffic_monthly", "data.hetznerrobot_ip.main", "traffic_monthly"),
					resource.TestCheckResourceAttrSet("hetznerrobot_rdns.v6", "ptr"),
				),
			},
			{
				Config: cfg("tf-acc-renamed.invalid"),
				Check:  resource.TestCheckResourceAttr("hetznerrobot_rdns.v6", "ptr", "tf-acc-renamed.invalid"),
			},
			{
				ResourceName:                         "hetznerrobot_server.s",
				ImportState:                          true,
				ImportStateId:                        server,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "server_number",
			},
			{
				ResourceName:                         "hetznerrobot_ip.main",
				ImportState:                          true,
				ImportStateIdFunc:                    stateAttr("hetznerrobot_ip.main", "ip"),
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "ip",
			},
			{
				ResourceName:                         "hetznerrobot_rdns.v6",
				ImportState:                          true,
				ImportStateIdFunc:                    stateAttr("hetznerrobot_rdns.v6", "ip"),
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "ip",
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
data "hetznerrobot_server_market_products" "all" {}
data "hetznerrobot_currency" "c" {}
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
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttrSet("hetznerrobot_server_order.o", "transaction_id"),
				resource.TestCheckResourceAttrSet("data.hetznerrobot_currency.c", "currency"),
			),
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
		"ServerSettings":   TestLiveServerSettings,
	} {
		t.Run(name, func(t *testing.T) {
			newMockRobot(t)
			t.Setenv("HETZNER_ROBOT_LIVE", "1")
			t.Setenv("HETZNER_ROBOT_LIVE_SERVER", fxServer1)
			t.Setenv("HETZNER_ROBOT_LIVE_VSWITCH", "1")
			t.Setenv("HETZNER_ROBOT_LIVE_ORDER", "1")
			t.Setenv("HETZNER_ROBOT_LIVE_WRITE", "1")
			live(t)
		})
	}
}

// TestLiveServerSettings changes what can be changed on a running server
// without touching its operation, and puts everything back: the name, the
// traffic warnings of its main IP, a Rescue System activation (never followed
// by a reset — it would only take effect on the next reboot) and a Wake on LAN
// packet, which a running server ignores. Opt-in on top of the server:
//
//	HETZNER_ROBOT_LIVE_WRITE=1
func TestLiveServerSettings(t *testing.T) {
	liveEnv(t)
	server := liveOptIn(t, "HETZNER_ROBOT_LIVE_SERVER")
	liveOptIn(t, "HETZNER_ROBOT_LIVE_WRITE")
	n, err := parseInt64(server, "HETZNER_ROBOT_LIVE_SERVER")
	if err != nil {
		t.Fatal(err)
	}
	c := client.New(os.Getenv("HETZNER_ROBOT_ENDPOINT"), os.Getenv("HETZNER_ROBOT_USER"), os.Getenv("HETZNER_ROBOT_PASSWORD"), "terraform-provider-hetznerrobot/live-test")
	ctx := context.Background()
	orig, err := client.GetObject[apiServer](ctx, c, "/server/"+server, "server", nil)
	if err != nil {
		t.Fatalf("reading server: %v", err)
	}
	ip, err := client.GetObject[apiIP](ctx, c, "/ip/"+orig.ServerIP.Value, "ip", nil)
	if err != nil {
		t.Fatalf("reading main IP: %v", err)
	}
	// Whatever happens below, the server leaves this test as it came in.
	t.Cleanup(func() {
		if err := c.Send(ctx, http.MethodPost, "/server/"+server, url.Values{"server_name": {orig.ServerName}}, nil); err != nil {
			t.Errorf("restoring the server name: %v", err)
		}
		f := url.Values{
			"traffic_warnings": {client.Bool(ip.TrafficWarnings)},
			"traffic_hourly":   {fmt.Sprint(ip.TrafficHourly.Value)},
			"traffic_daily":    {fmt.Sprint(ip.TrafficDaily.Value)},
			"traffic_monthly":  {fmt.Sprint(ip.TrafficMonthly.Value)},
		}
		if err := c.Send(ctx, http.MethodPost, "/ip/"+orig.ServerIP.Value, f, nil); err != nil {
			t.Errorf("restoring the traffic warnings: %v", err)
		}
		if err := c.Send(ctx, http.MethodDelete, fmt.Sprintf("/boot/%d/rescue", n), nil, nil); err != nil && !client.NotFound(err) {
			t.Logf("deactivating the Rescue System (may already be inactive): %v", err)
		}
	})

	cfg := func(name string, warnings bool, rescue bool) string {
		out := fmt.Sprintf(`
resource "hetznerrobot_server" "s" {
  server_number = %[1]d
  server_name   = %[2]q
}
resource "hetznerrobot_ip" "main" {
  ip               = %[3]q
  traffic_warnings = %[4]t
  traffic_monthly  = %[5]d
}
resource "hetznerrobot_wake_on_lan" "w" {
  server_number = %[1]d
}`, n, name, orig.ServerIP.Value, warnings, ip.TrafficMonthly.Value+1)
		if rescue {
			out += fmt.Sprintf(`
resource "hetznerrobot_boot_rescue" "r" {
  server_number = %d
  os            = "linux"
}`, n)
		}
		return out
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: cfg(orig.ServerName+"-tfacc", !ip.TrafficWarnings, true),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hetznerrobot_server.s", "server_name", orig.ServerName+"-tfacc"),
					resource.TestCheckResourceAttr("hetznerrobot_ip.main", "traffic_warnings", fmt.Sprint(!ip.TrafficWarnings)),
					resource.TestCheckResourceAttr("hetznerrobot_ip.main", "traffic_monthly", fmt.Sprint(ip.TrafficMonthly.Value+1)),
					resource.TestCheckResourceAttr("hetznerrobot_boot_rescue.r", "active", "true"),
					resource.TestCheckResourceAttrSet("hetznerrobot_boot_rescue.r", "password"),
				),
			},
			{
				ResourceName:                         "hetznerrobot_boot_rescue.r",
				ImportState:                          true,
				ImportStateId:                        server,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "server_number",
			},
			{
				// Dropping the rescue resource deactivates it again.
				Config: func() string {
					// Back to the original monthly limit as well.
					return strings.Replace(cfg(orig.ServerName, ip.TrafficWarnings, false),
						fmt.Sprintf("traffic_monthly  = %d", ip.TrafficMonthly.Value+1),
						fmt.Sprintf("traffic_monthly  = %d", ip.TrafficMonthly.Value), 1)
				}(),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hetznerrobot_server.s", "server_name", orig.ServerName),
					resource.TestCheckResourceAttr("hetznerrobot_ip.main", "traffic_warnings", fmt.Sprint(ip.TrafficWarnings)),
				),
			},
		},
	})
}
