# Robot generates the password when none is set; read it from the sensitive
# `password` attribute.
resource "hetznerrobot_storagebox_subaccount" "db" {
  storagebox_id  = hetznerrobot_storagebox.backup.id
  home_directory = "backups/db"
  ssh            = true
  readonly       = false
  comment        = "database dumps"
}
