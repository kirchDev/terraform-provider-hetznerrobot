# A separate MAC for an additional IP, e.g. to bridge it to a VM.
resource "hetznerrobot_ip_mac" "vm" {
  ip = "123.123.123.125"
}

output "vm_mac" {
  value = hetznerrobot_ip_mac.vm.mac
}
