data "hetznerrobot_boot" "web" {
  server_number = 321
}

output "installable_linux" {
  value = data.hetznerrobot_boot.web.linux_dists
}
