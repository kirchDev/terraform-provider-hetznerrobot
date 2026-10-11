resource "hetznerrobot_rdns" "main" {
  ip  = data.hetznerrobot_server.web.server_ip
  ptr = "web1.example.com"
}

# Any address inside the server's IPv6 /64 can carry its own entry.
resource "hetznerrobot_rdns" "v6" {
  ip  = "${data.hetznerrobot_server.web.server_ipv6_net}1"
  ptr = "web1.example.com"
}
