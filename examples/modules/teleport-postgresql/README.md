# teleport-postgresql module

Wraps the Teleport plumbing needed to point the
[`cyrilgdn/postgresql`](https://registry.terraform.io/providers/cyrilgdn/postgresql/latest/docs)
provider at a Teleport-protected database using **verify-full TLS**.

It bundles:

- `data.teleportconnect_cluster` — fetches the cluster CA bundle once.
- `local_file` — writes the (public) CA bundle to disk for the provider's
  `sslrootcert` (which only accepts a file path).
- `ephemeral.teleportconnect_db_certificate` — issues a short-lived client
  certificate that never touches disk or state.

The client certificate and key are returned as **ephemeral** outputs and are
passed to the `postgresql` provider inline via `clientcert.sslinline = true`,
so the only thing written to disk is the public CA bundle.

> Prefer the in-process tunnel (`teleportconnect_db_tunnel` +
> `sslmode = "disable"`) when you don't need end-to-end verify-full TLS; it
> writes nothing to disk at all. Use this module when policy requires
> verify-full.

## Usage

```hcl
module "pg_appdb" {
  source = "./modules/teleport-postgresql"

  database = "mycorp-postgres"
  db_user  = "ci"
  db_name  = "appdb"
}

provider "postgresql" {
  host        = module.pg_appdb.host
  port        = module.pg_appdb.port
  database    = module.pg_appdb.db_name
  username    = module.pg_appdb.db_user
  sslmode     = "verify-full"
  sslrootcert = module.pg_appdb.sslrootcert

  clientcert {
    cert      = module.pg_appdb.certificate
    key       = module.pg_appdb.private_key
    sslinline = true
  }
}

resource "postgresql_database" "app" {
  name = "app"
}
```

## Inputs

| Name | Type | Default | Description |
| --- | --- | --- | --- |
| `database` | string | (required) | Teleport database service name. |
| `db_user` | string | (required) | Database user embedded in the certificate. |
| `db_name` | string | (required) | Database name to connect to. |
| `ttl` | string | `"1h"` | Certificate validity (Go duration). |
| `ca_output_dir` | string | `${path.root}/.terraform/teleport-ca` | Directory for the CA file. |
| `ca_filename` | string | `"teleport-ca.pem"` | CA filename. |

## Outputs

| Name | Ephemeral | Description |
| --- | --- | --- |
| `host` | no | Proxy hostname. |
| `port` | no | Proxy port. |
| `sslrootcert` | no | Path to the written CA bundle. |
| `db_user` | no | Database user (echoed). |
| `db_name` | no | Database name (echoed). |
| `certificate` | yes | PEM client certificate (pass inline). |
| `private_key` | yes | PEM client private key (pass inline). |

## Notes

- The CA file lives under `.terraform/` by default, which Terraform already
  ignores. Override with `ca_output_dir` if you need it elsewhere.
- Requires Terraform >= 1.12 (ephemeral resources and ephemeral module
  outputs).
