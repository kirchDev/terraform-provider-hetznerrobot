# Daily traffic of a server's main IP and its IPv6 subnet in September 2026.
data "hetznerrobot_traffic" "september" {
  type          = "month"
  from          = "2026-09-01"
  to            = "2026-09-30"
  ips           = ["123.123.123.123"]
  subnets       = ["2a01:4f8:111:4221::"]
  single_values = true
}

output "september_total_gb" {
  value = sum(data.hetznerrobot_traffic.september.entries[*].sum)
}
