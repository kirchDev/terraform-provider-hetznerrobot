data "hetznerrobot_subnet_mac" "v6" {
  ip = "2a01:4f8:111:4221::"
}

# Routes the IPv6 subnet to the MAC of the additional IP instead of the
# server's main IP.
resource "hetznerrobot_subnet_mac" "v6" {
  ip  = data.hetznerrobot_subnet_mac.v6.ip
  mac = data.hetznerrobot_subnet_mac.v6.possible_mac["123.123.123.125"]
}
