# Starts a VNC-guided installation on the next reboot — this wipes the server.
resource "hetznerrobot_boot_vnc" "installer" {
  server_number = 321
  dist          = "Fedora-40"
  lang          = "en_US"
}

output "vnc_password" {
  value     = hetznerrobot_boot_vnc.installer.password
  sensitive = true
}
