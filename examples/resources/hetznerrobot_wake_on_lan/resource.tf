# Wakes server 321 on creation and whenever the triggers change.
resource "hetznerrobot_wake_on_lan" "wake" {
  server_number = 321
  triggers = {
    date = "2026-10-12"
  }
}
