variable "database" {
  type        = string
  description = "Teleport database service name (matches `tsh db ls`)."
}

variable "db_user" {
  type        = string
  description = "Database user to embed in the issued certificate."
}

variable "db_name" {
  type        = string
  description = "Database name to connect to."
}

variable "ttl" {
  type        = string
  default     = "1h"
  description = "Certificate validity (Go duration, e.g. 1h, 30m)."
}

variable "ca_output_dir" {
  type        = string
  default     = null
  description = "Directory to write the cluster CA bundle into. Defaults to <root>/.terraform/teleport-ca."
}

variable "ca_filename" {
  type        = string
  default     = "teleport-ca.pem"
  description = "Filename for the written CA bundle."
}
