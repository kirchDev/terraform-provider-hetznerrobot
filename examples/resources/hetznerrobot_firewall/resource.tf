resource "hetznerrobot_firewall" "web" {
  server_number = 321
  filter_ipv6   = true

  input_rules = [
    { name = "ssh from office", ip_version = "ipv4", src_ip = "198.51.100.0/24", dst_port = "22", protocol = "tcp", action = "accept" },
    { name = "https", dst_port = "443", action = "accept" },
    { name = "replies", ip_version = "ipv4", dst_port = "32768-65535", protocol = "tcp", tcp_flags = "ack", action = "accept" },
    { name = "drop the rest", action = "discard" },
  ]
  output_rules = [
    { name = "allow all", action = "accept" },
  ]
}

# Or apply a template instead of listing the rules.
resource "hetznerrobot_firewall" "db" {
  server_number = 421
  template_id   = hetznerrobot_firewall_template.base.id
}
