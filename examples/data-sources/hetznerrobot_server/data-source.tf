data "hetznerrobot_server" "web" {
  server_number = 321
}

output "web_ipv6" {
  value = data.hetznerrobot_server.web.server_ipv6_net
}
