data "hetznerrobot_failover" "vip" {
  ip = "88.99.1.10"
}

output "vip_target" {
  value = data.hetznerrobot_failover.vip.active_server_ip
}
