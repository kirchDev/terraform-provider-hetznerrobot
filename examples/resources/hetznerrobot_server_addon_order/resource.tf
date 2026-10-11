# An additional IPv4 address for server 321. Destroying this resource does NOT
# cancel the IP — use hetznerrobot_ip_cancellation for that.
resource "hetznerrobot_server_addon_order" "extra_ip" {
  server_number = 321
  product_id    = "additional_ipv4"
  reason        = "Virtual machines with public addresses"
}

output "extra_ip" {
  value = one([for r in hetznerrobot_server_addon_order.extra_ip.resources : r.id if r.type == "ip"])
}
