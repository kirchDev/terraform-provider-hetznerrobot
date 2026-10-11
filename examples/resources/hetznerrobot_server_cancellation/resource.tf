data "hetznerrobot_server_cancellation" "old" {
  server_number = 421
}

# Cancels the server to its earliest possible date. Destroying this resource
# withdraws the cancellation again, as long as the date has not passed.
resource "hetznerrobot_server_cancellation" "old" {
  server_number       = 421
  cancellation_date   = data.hetznerrobot_server_cancellation.old.earliest_cancellation_date
  cancellation_reason = "Upgrade to a new server"
  reserve_location    = false
}
