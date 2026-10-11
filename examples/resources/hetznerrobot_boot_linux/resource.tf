# Reinstalls server 321 with Debian on its next reboot — this wipes the server.
resource "hetznerrobot_boot_linux" "debian" {
  server_number   = 321
  dist            = "Debian 12 base"
  lang            = "en"
  authorized_keys = [hetznerrobot_ssh_key.admin.fingerprint]
}

resource "hetznerrobot_server_reset" "install" {
  server_number = hetznerrobot_boot_linux.debian.server_number
  type          = "hw"
  triggers = {
    activation = hetznerrobot_boot_linux.debian.password
  }
}
