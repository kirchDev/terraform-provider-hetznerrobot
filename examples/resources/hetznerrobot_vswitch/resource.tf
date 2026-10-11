resource "hetznerrobot_vswitch" "backend" {
  name    = "backend"
  vlan    = 4000
  servers = [321, 421]

  # Cancelled on destroy; "now" is the default.
  cancellation_date = "now"
}
