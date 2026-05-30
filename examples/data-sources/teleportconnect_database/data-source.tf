# Resolve a Teleport database by exact name.
data "teleportconnect_database" "by_name" {
  name = "mycorp-postgres"
}

# Resolve a Teleport database by labels (all must match).
data "teleportconnect_database" "by_labels" {
  labels = {
    env  = "prod"
    team = "platform"
  }
}

output "database_protocol" {
  value = data.teleportconnect_database.by_name.protocol
}
