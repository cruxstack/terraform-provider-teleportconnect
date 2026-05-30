locals {
  ca_dir  = coalesce(var.ca_output_dir, "${path.root}/.terraform/teleport-ca")
  ca_path = "${local.ca_dir}/${var.ca_filename}"
}

# Cluster CA bundle (public material), fetched once. The same CA signs the
# proxy serving certificate for every database in the cluster.
data "teleportconnect_cluster" "this" {}

# Write the CA to a file so a database provider's sslrootcert (path-only) can
# reference it. The cert/key themselves stay in memory (ephemeral) and are
# passed inline via clientcert.sslinline = true at the call site.
resource "local_file" "ca" {
  filename = local.ca_path
  content  = data.teleportconnect_cluster.this.ca_certificate
}

# Short-lived TLS client certificate for the specific database/user/name.
# Never written to disk; never stored in Terraform state.
ephemeral "teleportconnect_db_certificate" "this" {
  database = var.database
  db_user  = var.db_user
  db_name  = var.db_name
  ttl      = var.ttl
}
