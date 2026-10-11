data "hetznerrobot_server" "primary" {
  server_number = 321
}

# Switch the failover IP by changing active_server_ip. Destroying the resource
# deletes the routing — the IP then reaches no server.
resource "hetznerrobot_failover" "vip" {
  ip               = "88.99.1.10"
  active_server_ip = data.hetznerrobot_server.primary.server_ip
}
