# Boot server 321 into the Rescue System: activate it, then reset the server.
# The reset runs again whenever a new activation brings a new password.
resource "hetznerrobot_boot_rescue" "rescue" {
  server_number   = 321
  os              = "linux"
  authorized_keys = [hetznerrobot_ssh_key.admin.fingerprint]
}

resource "hetznerrobot_server_reset" "into_rescue" {
  server_number = hetznerrobot_boot_rescue.rescue.server_number
  type          = "hw"
  triggers = {
    activation = hetznerrobot_boot_rescue.rescue.password
  }
}
