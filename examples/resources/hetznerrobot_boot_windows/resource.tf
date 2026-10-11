# Needs the Windows addon ordered for the server; wipes the server on the next reboot.
resource "hetznerrobot_boot_windows" "windows" {
  server_number = 321
  os            = "Windows Server 2022 Standard Edition"
  lang          = "en"
}
