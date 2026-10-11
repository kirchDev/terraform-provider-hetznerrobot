data "hetznerrobot_reset" "web" {
  server_number = 321
}

output "reset_types" {
  value = data.hetznerrobot_reset.web.types
}
