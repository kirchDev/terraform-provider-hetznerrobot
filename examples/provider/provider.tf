terraform {
  required_providers {
    hetznerrobot = {
      source = "kirchdev/hetznerrobot"
    }
  }
}

# Credentials of a Robot webservice user (Robot → Settings → Web service and
# app settings). They may also come from HETZNER_ROBOT_USER and
# HETZNER_ROBOT_PASSWORD.
provider "hetznerrobot" {
  username = var.robot_user
  password = var.robot_password
}

variable "robot_user" {
  type = string
}

variable "robot_password" {
  type      = string
  sensitive = true
}
