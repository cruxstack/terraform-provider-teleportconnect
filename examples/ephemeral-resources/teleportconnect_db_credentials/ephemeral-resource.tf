data "teleportconnect_database" "main" {
  name = "mycorp-postgres"
}

# Issue a short-lived TLS client certificate for the database. The cert, key,
# and CA never touch Terraform state.
ephemeral "teleportconnect_db_credentials" "main" {
  database = data.teleportconnect_database.main.matched_name
  db_user  = "readonly"
  db_name  = "appdb"
  ttl      = "30m"
}

# Feed the issued material into a downstream database provider that connects
# through the Teleport proxy via TLS routing. The provider connects to
# host:port using verify-full TLS with the issued client cert.
#
# provider "postgresql" {
#   host            = ephemeral.teleportconnect_db_credentials.main.host
#   port            = ephemeral.teleportconnect_db_credentials.main.port
#   database        = "appdb"
#   username        = "readonly"
#   sslmode         = "verify-full"
#   clientcert {
#     cert = ephemeral.teleportconnect_db_credentials.main.cert
#     key  = ephemeral.teleportconnect_db_credentials.main.key
#   }
# }
