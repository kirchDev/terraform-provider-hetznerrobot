# Nightly at 03:30 UTC, keeping the last seven snapshots.
resource "hetznerrobot_storagebox_snapshot_plan" "nightly" {
  storagebox_id = hetznerrobot_storagebox.backup.id
  status        = "enabled"
  minute        = 30
  hour          = 3
  max_snapshots = 7
}
