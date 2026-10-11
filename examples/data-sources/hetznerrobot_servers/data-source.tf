data "hetznerrobot_servers" "all" {}

output "server_numbers" {
  value = data.hetznerrobot_servers.all.servers[*].server_number
}
