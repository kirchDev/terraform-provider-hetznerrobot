# The inclusive BX10 Storage Box of a dedicated server. Robot cannot create a
# box, so the resource adopts it by id; destroying it only forgets it.
resource "hetznerrobot_storagebox" "backup" {
  id                    = 123456
  name                  = "backup"
  ssh                   = true
  samba                 = false
  webdav                = false
  external_reachability = true
  password              = var.storagebox_password
}

variable "storagebox_password" {
  type      = string
  sensitive = true
}
