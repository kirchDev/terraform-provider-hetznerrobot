# The subnets of one server; leave server_ip out for the whole account.
data "hetznerrobot_subnets" "web" {
  server_ip = "123.123.123.123"
}
