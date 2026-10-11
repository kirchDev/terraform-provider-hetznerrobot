# Adopts an IP to manage its traffic warnings; destroying it only forgets it.
resource "hetznerrobot_ip" "main" {
  ip               = "123.123.123.123"
  traffic_warnings = true
  traffic_hourly   = 500  # MB
  traffic_daily    = 5000 # MB
  traffic_monthly  = 50   # GB
}
