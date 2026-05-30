output "host" {
  value       = ephemeral.teleportconnect_db_certificate.this.host
  description = "Proxy hostname the database provider should connect to."
  ephemeral   = true
}

output "port" {
  value       = ephemeral.teleportconnect_db_certificate.this.port
  description = "Proxy port the database provider should connect to."
  ephemeral   = true
}

output "sslrootcert" {
  value       = local_file.ca.filename
  description = "Path to the written cluster CA bundle, for the provider's sslrootcert."
}

output "db_user" {
  value       = var.db_user
  description = "Database user (echoed for convenience when wiring the provider)."
}

output "db_name" {
  value       = var.db_name
  description = "Database name (echoed for convenience when wiring the provider)."
}

output "certificate" {
  value       = ephemeral.teleportconnect_db_certificate.this.certificate
  description = "PEM-encoded TLS client certificate. Pass inline via clientcert.sslinline = true."
  ephemeral   = true
  sensitive   = true
}

output "private_key" {
  value       = ephemeral.teleportconnect_db_certificate.this.private_key
  description = "PEM-encoded TLS client private key. Pass inline via clientcert.sslinline = true."
  ephemeral   = true
  sensitive   = true
}
