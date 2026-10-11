# Every entry of one server; leave server_ip out for the whole account.
data "hetznerrobot_rdns_entries" "web" {
  server_ip = "123.123.123.123"
}
