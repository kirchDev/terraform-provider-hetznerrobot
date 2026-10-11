data "hetznerrobot_subnet_mac" "v6" {
  ip = "2a01:4f8:111:4221::"
}

output "possible_macs" {
  value = data.hetznerrobot_subnet_mac.v6.possible_mac
}
