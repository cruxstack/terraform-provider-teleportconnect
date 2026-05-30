terraform {
  required_version = ">= 1.12.0"

  required_providers {
    teleportconnect = {
      source  = "cruxstack/teleportconnect"
      version = ">= 0.1.0"
    }
    local = {
      source  = "hashicorp/local"
      version = ">= 2.4.0"
    }
  }
}
