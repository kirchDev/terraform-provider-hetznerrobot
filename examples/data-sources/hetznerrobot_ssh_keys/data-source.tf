data "hetznerrobot_ssh_keys" "all" {}

output "fingerprints" {
  value = data.hetznerrobot_ssh_keys.all.keys[*].fingerprint
}
