data "hetznerrobot_server_product" "ex44" {
  id = "EX44"
}

output "ex44_dists" {
  value = data.hetznerrobot_server_product.ex44.dists
}
