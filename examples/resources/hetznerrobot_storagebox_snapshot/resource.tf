resource "hetznerrobot_storagebox_snapshot" "before_upgrade" {
  storagebox_id = hetznerrobot_storagebox.backup.id
  comment       = "before the database upgrade"
}
