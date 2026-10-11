data "hetznerrobot_server_products" "affordable" {
  location  = "FSN1"
  max_price = 60
}

output "affordable_products" {
  value = data.hetznerrobot_server_products.affordable.products[*].id
}
