data "hetznerrobot_server_addon_products" "web" {
  server_number = 321
}

output "addon_ids" {
  value = data.hetznerrobot_server_addon_products.web.products[*].id
}
