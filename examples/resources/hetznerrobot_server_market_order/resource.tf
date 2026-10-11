# The cheapest market offer with at least 64 GB of memory.
data "hetznerrobot_server_market_products" "all" {}

locals {
  candidates = [for p in data.hetznerrobot_server_market_products.all.products : p if p.memory_size >= 64]
  lowest     = min([for c in local.candidates : tonumber(c.price)]...)
  cheapest   = [for p in local.candidates : p if tonumber(p.price) == local.lowest][0]
}

# Places a real, billed order. Destroying this resource does NOT cancel the
# server — use hetznerrobot_server_cancellation for that.
resource "hetznerrobot_server_market_order" "storage" {
  product_id      = local.cheapest.id
  authorized_keys = [hetznerrobot_ssh_key.admin.fingerprint]
  addons          = ["primary_ipv4"]

  # Validate the order without placing it:
  # test = true

  lifecycle {
    # Market offers come and go; never re-order because the cheapest changed.
    ignore_changes = [product_id]
  }
}
