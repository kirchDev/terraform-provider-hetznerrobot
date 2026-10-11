resource "hetznerrobot_firewall_template" "base" {
  name          = "base"
  whitelist_hos = true

  input_rules = [
    { name = "ssh", ip_version = "ipv4", dst_port = "22", protocol = "tcp", action = "accept" },
    { name = "drop the rest", action = "discard" },
  ]
  output_rules = [
    { name = "allow all", action = "accept" },
  ]
}
