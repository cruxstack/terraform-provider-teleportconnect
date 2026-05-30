# Read cluster metadata, including the TLS CA bundle. The CA is the same for
# every database in the cluster, so write it once and reuse the path.
data "teleportconnect_cluster" "this" {}

# Write the (public) CA bundle to a file for a downstream database provider's
# sslrootcert. Lives under .terraform/, which Terraform already ignores.
resource "local_file" "teleport_ca" {
  filename = "${path.root}/.terraform/teleport-ca/teleport-ca.pem"
  content  = data.teleportconnect_cluster.this.ca_certificate
}

output "cluster_name" {
  value = data.teleportconnect_cluster.this.cluster_name
}

output "server_version" {
  value = data.teleportconnect_cluster.this.server_version
}
