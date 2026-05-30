terraform {
  required_version = ">= 1.10.0"
  required_providers {
    teleportconnect = {
      source = "cruxstack/teleportconnect"
    }
  }
}

provider "teleportconnect" {
  proxy_address     = "use1-common-teleport.tools.myprize.io:443"
  use_local_profile = true

  # The proxy sits behind an AWS L7 LB that terminates TLS with a
  # Let's Encrypt cert and forwards to the proxy. The library's
  # auto-probe is fooled by it, so explicitly enable upgrade.
  alpn_conn_upgrade = "yes"
}

# Step-7 data source: resolve a Teleport database resource by name (and/or
# labels). Hands off the resolved name to the credentials and tunnel
# resources so we don't repeat the literal in three places.
data "teleportconnect_database" "telemetry" {
  name = "mpz-plat-use1-dev-telemetry-rds-cluster"
}

# Step-3 resource: issue a short-lived TLS db cert + the cluster CA bundle.
# Protocol omitted - the resource looks it up via GetDatabaseServers.
ephemeral "teleportconnect_db_credentials" "telemetry" {
  database = data.teleportconnect_database.telemetry.matched_name
  db_user  = "dbviewer"
  db_name  = "postgres"
}

# Step-4 resource: open a local TCP listener that proxies to the database
# through the Teleport proxy via TLS routing. Same auto-protocol behavior.
ephemeral "teleportconnect_db_tunnel" "telemetry_dbmaster" {
  database = data.teleportconnect_database.telemetry.matched_name
  db_user  = "dbmaster"
  db_name  = "postgres"
}

output "telemetry_db" {
  value = {
    name      = data.teleportconnect_database.telemetry.matched_name
    protocol  = data.teleportconnect_database.telemetry.protocol
    uri       = data.teleportconnect_database.telemetry.uri
    label_env = data.teleportconnect_database.telemetry.all_labels["env"]
  }
}

# Smoke test: connect through the new tunnel as dbmaster with no password
# and run a real query.
resource "terraform_data" "tunnel_check" {
  triggers_replace = {
    every_apply = timestamp()
  }

  provisioner "local-exec" {
    command = <<-EOT
      set -euo pipefail
      out="/tmp/teleportconnect-smoke/tunnel-output.log"
      mkdir -p "$(dirname "$out")"
      {
        echo "tunnel: $TP_LOCAL_HOST:$TP_LOCAL_PORT  user=$TP_DB_USER db=$TP_DB_NAME"
        psql "postgres://$TP_DB_USER@$TP_LOCAL_HOST:$TP_LOCAL_PORT/$TP_DB_NAME?sslmode=disable" \
          -c "select current_user, current_database(), version();"
      } | tee "$out"
    EOT

    environment = {
      TP_LOCAL_HOST = ephemeral.teleportconnect_db_tunnel.telemetry_dbmaster.local_host
      TP_LOCAL_PORT = ephemeral.teleportconnect_db_tunnel.telemetry_dbmaster.local_port
      TP_DB_USER    = "dbmaster"
      TP_DB_NAME    = "postgres"
    }
  }
}

resource "terraform_data" "psql_check" {
  triggers_replace = {
    every_apply = timestamp()
  }

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
      } | tee "$tmp/output.log"
    EOT

    environment = {
      TP_HOST    = ephemeral.teleportconnect_db_credentials.telemetry.host
      TP_PORT    = ephemeral.teleportconnect_db_credentials.telemetry.port
      TP_CA      = ephemeral.teleportconnect_db_credentials.telemetry.ca
      TP_CERT    = ephemeral.teleportconnect_db_credentials.telemetry.cert
      TP_KEY     = ephemeral.teleportconnect_db_credentials.telemetry.key
      TP_DB_USER = "dbviewer"
      TP_DB_NAME = "postgres"
    }
  }
}
