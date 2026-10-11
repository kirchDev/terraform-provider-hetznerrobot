# Adopts an existing server; only the name is managed.
resource "hetznerrobot_server" "web" {
  server_number = 321
  server_name   = "web1"
}
