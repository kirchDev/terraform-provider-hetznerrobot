resource "hetznerrobot_ssh_key" "admin" {
  name = "admin"
  data = file("~/.ssh/id_ed25519.pub")
}
