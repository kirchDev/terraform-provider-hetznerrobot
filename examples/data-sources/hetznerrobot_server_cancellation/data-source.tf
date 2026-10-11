data "hetznerrobot_server_cancellation" "web" {
  server_number = 321
}

output "earliest_cancellation" {
  value = data.hetznerrobot_server_cancellation.web.earliest_cancellation_date
}
