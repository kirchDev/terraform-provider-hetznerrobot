# Places a real, billed order. Destroying this resource does NOT cancel the
# server — use hetznerrobot_server_cancellation for that.
resource "hetznerrobot_server_order" "web" {
  product_id      = "EX44"
  location        = "FSN1"
  dist            = "Rescue system"
  authorized_keys = [hetznerrobot_ssh_key.admin.fingerprint]
  addons          = ["primary_ipv4"] # without it the server is IPv6-only

  # Validate the order without placing it:
  # test = true

  timeouts {
    create = "6h"
  }
}

output "web_server_number" {
  value = hetznerrobot_server_order.web.server_number
}
