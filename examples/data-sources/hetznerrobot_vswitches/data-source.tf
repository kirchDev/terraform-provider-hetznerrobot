data "hetznerrobot_vswitches" "all" {}

output "active_vlans" {
  value = [for v in data.hetznerrobot_vswitches.all.vswitches : v.vlan if !v.cancelled]
}
