resource "hetznerrobot_subnet" "v6" {
  ip               = "2a01:4f8:111:4221::"
  traffic_warnings = true
  traffic_monthly  = 20 # GB
}
