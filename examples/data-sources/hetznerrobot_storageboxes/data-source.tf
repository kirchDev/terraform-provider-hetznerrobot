# The boxes of one server; leave linked_server out for the whole account.
data "hetznerrobot_storageboxes" "web" {
  linked_server = 321
}
