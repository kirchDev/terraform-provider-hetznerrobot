# Resets server 321 on creation and whenever the triggers change.
resource "hetznerrobot_server_reset" "reboot" {
  server_number = 321
  type          = "hw"
  triggers = {
    kernel = "6.12"
  }
}
