data "hetznerrobot_currency" "account" {}

output "currency" {
  value = data.hetznerrobot_currency.account.currency
}
