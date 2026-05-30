# Terraform Provider: teleportconnect

`teleportconnect` provides Terraform-native, Teleport-mediated access to remote
resources — database client credentials, database tunnels, and SSH tunnels —
**without** requiring `tsh`, `jq`, or `bash` on the machine running Terraform.

It is the in-process equivalent of common `tsh` workflows:

| This provider                              | `tsh` equivalent                     |
| ------------------------------------------ | ------------------------------------ |
| `ephemeral.teleportconnect_db_credentials` | `tsh db login` + `tsh db config`     |
| `ephemeral.teleportconnect_db_tunnel`      | `tsh proxy db --tunnel`              |
| `ephemeral.teleportconnect_ssh_tunnel`     | `tsh ssh -N -L LOCAL:TARGET GATEWAY` |
| `data.teleportconnect_database`            | `tsh db ls`                          |
| `data.teleportconnect_node`                | `tsh ls`                             |

Because credentials and tunnels are modeled as
[ephemeral resources](https://developer.hashicorp.com/terraform/language/resources/ephemeral),
the issued certificates and private keys are never written to Terraform state.

## Requirements

- [Terraform](https://developer.hashicorp.com/terraform/downloads) >= 1.12
  (ephemeral resources are required)
- A reachable [Teleport](https://goteleport.com) cluster, **v15 or newer** (the
  provider requests ECDSA P-256 user certificates, which require a modern auth
  server)
- Credentials for that cluster: a `tsh` profile, or an identity file from
  `tctl auth sign` / `tbot`

## Using the provider

```hcl
terraform {
  required_version = ">= 1.12.0"
  required_providers {
    teleportconnect = {
      source  = "cruxstack/teleportconnect"
      version = "~> 0.1"
    }
  }
}

provider "teleportconnect" {
  proxy_address     = "teleport.example.com:443"
  use_local_profile = true
}
```

### Authentication

Exactly one authentication method must be configured:

| Attribute            | Description                                                                 |
| -------------------- | --------------------------------------------------------------------------- |
| `use_local_profile`  | Reuse the local `~/.tsh` profile for `proxy_address` (mirrors `tsh login`). |
| `identity_file_path` | Path to an identity file produced by `tctl auth sign` or `tbot`.            |
| `identity_file_data` | Inline identity file contents (PEM bundle). Marked sensitive.               |

For non-interactive runners (CI, Spacelift, etc.), run `tbot` as a sidecar that
writes an identity file and point `identity_file_path` at it. Delegated Machine
ID join methods (`iam`, `github`, ...) are not yet implemented in this provider;
see the roadmap below.

### Example: issue database credentials

```hcl
data "teleportconnect_database" "main" {
  name = "mycorp-postgres"
}

ephemeral "teleportconnect_db_credentials" "main" {
  database = data.teleportconnect_database.main.matched_name
  db_user  = "readonly"
  db_name  = "appdb"
}
```

### Example: open a database tunnel

```hcl
ephemeral "teleportconnect_db_tunnel" "main" {
  database = data.teleportconnect_database.main.matched_name
  db_user  = "readonly"
  db_name  = "appdb"
}

provider "postgresql" {
  host     = ephemeral.teleportconnect_db_tunnel.main.local_host
  port     = ephemeral.teleportconnect_db_tunnel.main.local_port
  database = "appdb"
  username = "readonly"
  sslmode  = "disable" # the tunnel terminates TLS to Teleport for you
}
```

### Example: open an SSH tunnel

```hcl
data "teleportconnect_node" "bastion" {
  labels = { role = "bastion" }
}

ephemeral "teleportconnect_ssh_tunnel" "db" {
  gateway_node = data.teleportconnect_node.bastion.matched_hostname
  ssh_login    = "ec2-user"
  target_host  = "internal-db.vpc.local"
  target_port  = 5432
}
```

See [`docs/`](./docs) for full reference documentation and the
[`examples/`](./examples) directory for runnable configurations.

## ALPN connection upgrade

When the Teleport proxy sits behind an L7 load balancer (e.g. AWS ALB) that
terminates TLS with its own certificate, Teleport's automatic ALPN probe can be
fooled into using direct TLS routing, which then fails. Set
`alpn_conn_upgrade = "yes"` to force the HTTPS connection upgrade in that case.
The default (`auto`) probes the proxy; `no` forces direct TLS routing.

## Teleport RBAC

The credentials used by the provider need a role that allows, at minimum:

- reading `db_server` and `node` resources (for the data sources and protocol
  lookup)
- issuing user certificates scoped to the target databases / nodes and logins

See [`docs/guides/teleport-rbac.md`](./docs/guides/teleport-rbac.md) for a
sample role.

## Developing the provider

```sh
# build
make build

# install into the local Go bin for dev_overrides
make install

# format, lint, vet
make fmt
make lint

# unit tests
make test

# acceptance tests (requires a live cluster + TF_ACC=1 + env vars)
make testacc

# regenerate docs from schema + examples
make generate
```

### Running against a local cluster

A single-node Teleport cluster for local acceptance/integration testing is
provided under [`test/integration/`](./test/integration). See its README for the
bootstrap steps.

## Roadmap

- Delegated Machine ID join methods (`iam`, `github`, `gcp`, ...) implemented
  directly against the Teleport JoinService, removing the need for a `tbot`
  sidecar.
- Configurable key algorithm (RSA / ECDSA / Ed25519).

## License

[MPL-2.0](./LICENSE)
