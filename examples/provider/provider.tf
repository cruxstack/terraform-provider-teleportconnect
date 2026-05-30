terraform {
  required_version = ">= 1.12.0"
  required_providers {
    teleportconnect = {
      source  = "cruxstack/teleportconnect"
      version = "~> 0.1"
    }
  }
}

# Authenticate using the local ~/.tsh profile (mirrors `tsh login`).
provider "teleportconnect" {
  proxy_address     = "teleport.example.com:443"
  use_local_profile = true
}

# Alternatively, authenticate with an identity file produced by
# `tctl auth sign` or a `tbot` sidecar:
#
# provider "teleportconnect" {
#   proxy_address      = "teleport.example.com:443"
#   identity_file_path = "/var/run/teleport/identity"
#
#   # Set to "yes" when the proxy is fronted by an L7 load balancer (AWS ALB)
#   # that terminates TLS with its own certificate.
#   alpn_conn_upgrade = "auto"
# }
