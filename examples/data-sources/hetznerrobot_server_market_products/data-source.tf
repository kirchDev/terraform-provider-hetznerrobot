data "hetznerrobot_server_market_products" "all" {}

output "big_memory_offers" {
  value = [for p in data.hetznerrobot_server_market_products.all.products : "${p.id}: ${p.price}" if p.memory_size >= 128]
}
