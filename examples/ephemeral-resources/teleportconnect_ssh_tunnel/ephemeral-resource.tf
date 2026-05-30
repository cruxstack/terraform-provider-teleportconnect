data "teleportconnect_node" "bastion" {
  labels = {
    role = "bastion"
  }
}

# Open a local TCP listener proxied through a Teleport-managed SSH gateway
# node to an arbitrary host:port reachable from that gateway. Equivalent to
# `tsh ssh -N -L LOCAL:internal-db.vpc.local:5432 bastion`.
ephemeral "teleportconnect_ssh_tunnel" "db" {
  gateway_node = data.teleportconnect_node.bastion.matched_hostname
  ssh_login    = "ec2-user"
  target_host  = "internal-db.vpc.local"
  target_port  = 5432
}

# provider "postgresql" {
#   host     = ephemeral.teleportconnect_ssh_tunnel.db.local_host
#   port     = ephemeral.teleportconnect_ssh_tunnel.db.local_port
#   database = "appdb"
#   username = "readonly"
#   sslmode  = "disable"
# }
