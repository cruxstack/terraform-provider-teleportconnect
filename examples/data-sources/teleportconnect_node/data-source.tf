# Resolve a Teleport SSH node by hostname.
data "teleportconnect_node" "by_hostname" {
  hostname = "bastion-1"
}

# Resolve a Teleport SSH node by labels.
data "teleportconnect_node" "bastion" {
  labels = {
    role = "bastion"
  }
}

output "bastion_hostname" {
  value = data.teleportconnect_node.bastion.matched_hostname
}
