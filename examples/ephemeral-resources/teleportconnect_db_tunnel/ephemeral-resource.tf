data "teleportconnect_database" "main" {
  name = "mycorp-postgres"
}

# Open a local TCP listener proxied to the database through the Teleport
# proxy. Downstream providers connect to local_host:local_port with no TLS
# and no client certs; the tunnel handles all Teleport authentication.
ephemeral "teleportconnect_db_tunnel" "main" {
  database = data.teleportconnect_database.main.matched_name
  db_user  = "readonly"
  db_name  = "appdb"
}

# provider "postgresql" {
#   host     = ephemeral.teleportconnect_db_tunnel.main.local_host
#   port     = ephemeral.teleportconnect_db_tunnel.main.local_port
#   database = "appdb"
#   username = "readonly"
#   sslmode  = "disable"
# }
