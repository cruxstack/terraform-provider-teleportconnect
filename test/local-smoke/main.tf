# Local smoke test for the teleportconnect provider. This is NOT part of the
# published examples; it drives a real database through both the credentials
# and tunnel resources and runs psql to confirm connectivity.
#
# Configure it via variables (no hardcoded hostnames or db names):
#
#   export TF_VAR_proxy_address="teleport.example.com:443"
#   export TF_VAR_database_name="mycorp-postgres"
#   terraform plan
#
# Use a .terraformrc with dev_overrides pointing at your local build:
#
#   provider_installation {
#     dev_overrides {
#       "cruxstack/teleportconnect" = "/path/to/repo/build"
#     }
#     direct {}
#   }

terraform {
  required_version = ">= 1.12.0"
  required_providers {
    teleportconnect = {
      source = "cruxstack/teleportconnect"
    }
  }
}

variable "proxy_address" {
  type        = string
  description = "Teleport proxy host:port."
}

variable "database_name" {
  type        = string
  description = "Teleport database service name to smoke test."
}

variable "db_user" {
  type        = string
  default     = "readonly"
  description = "Database user to embed in the issued certificate."
}

variable "db_name" {
  type        = string
  default     = "postgres"
  description = "Database name to connect to."
}

variable "alpn_conn_upgrade" {
  type        = string
  default     = "auto"
  description = "auto/yes/no. Set to yes when the proxy is behind an L7 LB."
}

provider "teleportconnect" {
  proxy_address     = var.proxy_address
  use_local_profile = true
  alpn_conn_upgrade = var.alpn_conn_upgrade
}

data "teleportconnect_database" "smoke" {
  name = var.database_name
}

ephemeral "teleportconnect_db_certificate" "smoke" {
  database = data.teleportconnect_database.smoke.matched_name
  db_user  = var.db_user
  db_name  = var.db_name
}

ephemeral "teleportconnect_db_tunnel" "smoke" {
  database = data.teleportconnect_database.smoke.matched_name
  db_user  = var.db_user
  db_name  = var.db_name
}

output "database" {
  value = {
    name     = data.teleportconnect_database.smoke.matched_name
    protocol = data.teleportconnect_database.smoke.protocol
    uri      = data.teleportconnect_database.smoke.uri
  }
}

# Smoke check: connect through the tunnel and run a real query.
resource "terraform_data" "tunnel_check" {
  triggers_replace = { every_apply = timestamp() }

  provisioner "local-exec" {
    command = <<-EOT
      set -euo pipefail
      out="/tmp/teleportconnect-smoke/tunnel-output.log"
      mkdir -p "$(dirname "$out")"
      {
        echo "tunnel: $TP_LOCAL_HOST:$TP_LOCAL_PORT user=$TP_DB_USER db=$TP_DB_NAME"
        psql "postgres://$TP_DB_USER@$TP_LOCAL_HOST:$TP_LOCAL_PORT/$TP_DB_NAME?sslmode=disable" \
          -c "select current_user, current_database(), version();"
      } | tee "$out"
    EOT

    environment = {
      TP_LOCAL_HOST = ephemeral.teleportconnect_db_tunnel.smoke.local_host
      TP_LOCAL_PORT = ephemeral.teleportconnect_db_tunnel.smoke.local_port
      TP_DB_USER    = var.db_user
      TP_DB_NAME    = var.db_name
    }
  }
}

# Smoke check: connect directly with the issued cert/key/CA via verify-full.
resource "terraform_data" "creds_check" {
  triggers_replace = { every_apply = timestamp() }

  provisioner "local-exec" {
    command = <<-EOT
      set -euo pipefail
      tmp="/tmp/teleportconnect-smoke"
      mkdir -p "$tmp"
      printf '%s' "$TP_CA"   > "$tmp/ca.pem"
      printf '%s' "$TP_CERT" > "$tmp/cert.pem"
      printf '%s' "$TP_KEY"  > "$tmp/key.pem"
      chmod 600 "$tmp/key.pem"
      {
        echo "host=$TP_HOST port=$TP_PORT user=$TP_DB_USER db=$TP_DB_NAME"
        psql \
          "postgres://$TP_DB_USER@$TP_HOST:$TP_PORT/$TP_DB_NAME?sslrootcert=$tmp/ca.pem&sslcert=$tmp/cert.pem&sslkey=$tmp/key.pem&sslmode=verify-full" \
          -c "select current_user, current_database(), version();"
      } | tee "$tmp/creds-output.log"
    EOT

    environment = {
      TP_HOST    = ephemeral.teleportconnect_db_certificate.smoke.host
      TP_PORT    = ephemeral.teleportconnect_db_certificate.smoke.port
      TP_CA      = ephemeral.teleportconnect_db_certificate.smoke.ca_certificate
      TP_CERT    = ephemeral.teleportconnect_db_certificate.smoke.certificate
      TP_KEY     = ephemeral.teleportconnect_db_certificate.smoke.private_key
      TP_DB_USER = var.db_user
      TP_DB_NAME = var.db_name
    }
  }
}
