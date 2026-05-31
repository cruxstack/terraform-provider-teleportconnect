---
page_title: "Using teleportconnect in CI"
subcategory: ""
---

# Using teleportconnect in CI

This guide shows how to run Terraform on a **minimal Linux GitHub Actions
runner** (self-hosted on EC2 or GitHub-hosted) to manage PostgreSQL resources
behind a Teleport-protected database with the upstream
[`cyrilgdn/postgresql`](https://registry.terraform.io/providers/cyrilgdn/postgresql/latest/docs)
provider.

The goal is **no prerequisites on the runner** beyond Terraform itself: no
`tsh`, no `tbot` system install, no `psql`. The `teleportconnect` provider does
the Teleport authentication and TLS routing in-process.

## Recommended pattern: db_tunnel + sslmode=disable

Use the `teleportconnect_db_tunnel` ephemeral resource to open a local TCP
listener, and point the `postgresql` provider at `localhost`:

```hcl
ephemeral "teleportconnect_db_tunnel" "main" {
  database = data.teleportconnect_database.main.matched_name
  db_user  = "ci"
  db_name  = "appdb"
}

provider "postgresql" {
  host      = ephemeral.teleportconnect_db_tunnel.main.local_host
  port      = ephemeral.teleportconnect_db_tunnel.main.local_port
  database  = "appdb"
  username  = "ci"
  sslmode   = "disable" # loopback hop only; Teleport encrypts host->proxy->db
  superuser = false
}
```

~> **`sslmode = "disable"` does not mean the connection is unencrypted.** It
applies only to the **loopback hop** from the `postgresql` client to
`127.0.0.1:<local_port>`, which never leaves the host. The tunnel then wraps
that traffic in Teleport's mutually-authenticated TLS for the host → proxy →
database hops, so the bytes on the wire are encrypted and authenticated by
Teleport end-to-end. Adding TLS on the loopback hop would just be redundant
encryption on a connection that already never leaves the machine.

Why the tunnel rather than the certificate resource? The tunnel writes
**nothing** to disk — Terraform connects to `localhost` and Teleport does the
TLS termination in-process — and the `postgresql` provider connects with
`sslmode = "disable"` (loopback only; see the note above), so there is no CA
file to manage. The certificate path (below) needs one file on disk for the CA
bundle.

(If you specifically need the `postgresql` provider itself to perform
verify-full TLS — for example to satisfy a policy that mandates it at the client
— see the [certificate path](#certificate-path-verify-full-tls) section.)

### Auditing

The tunnel does not bypass Teleport's audit log. Both `tsh db login` (direct
certificate) and the tunnel route the actual database traffic through the
Teleport Database Service, which authenticates the per-session certificate and
emits the same audit events — session start/end and, for protocols Teleport
parses at the statement level (PostgreSQL, MySQL, etc.), per-query events
containing the SQL. Events are attributed to the identity the certificate was
issued for, along with `db_user`, `db_name`, and the database service name.

In other words, queries your CI pipeline runs through the tunnel are audited
exactly as if they had been run via `tsh db login`. Audit granularity (full
statements vs. session-level) is a property of the Database Service's protocol
support, not of whether you use a tunnel.

## One-time prerequisites (off the runner)

These are done once by an administrator against your Teleport cluster, not on
the CI runner.

1. Create a least-privilege role for CI. See the
   [Teleport RBAC guide](./teleport-rbac.md) for the role spec; at minimum it
   needs to read `db_server` and issue certificates for the target `db_users` /
   `db_names`.

2. Create a user (or bot) with that role:

   ```sh
   tctl users add terraform-ci --roles terraform-ci
   ```

3. Choose an authentication recipe below.

## Authentication recipes

Pick one. Recipe B (native delegated join) is the simplest for CI.

### Recipe A — identity file from a GitHub Actions secret

An administrator pre-signs an identity file and stores it as an encrypted
Actions secret. The workflow writes it to a temp file at runtime.

Sign the identity (off the runner) and copy its contents into a repository or
organization secret named `TELEPORT_IDENTITY`:

```sh
tctl auth sign --user terraform-ci --ttl 24h --format file --out ./identity
# paste the contents of ./identity into the TELEPORT_IDENTITY secret
```

- **Pros**: zero binary downloads on the runner; works on the most locked-down
  images.
- **Cons**: the identity has a fixed TTL and must be re-signed and the secret
  updated before it expires (see [rotation](#identity-file-rotation)).

### Recipe B — native delegated join (recommended)

Set `join_method` + `join_token` on the provider. It fetches the GitHub OIDC
token in-process and joins the cluster directly — no binary to download, no
identity file, no secret to rotate.

```hcl
provider "teleportconnect" {
  proxy_address = "teleport.example.com:443"
  join_method   = "github"
  join_token    = "teleportconnect-ci"
}
```

This requires a one-time `github` join token on the cluster (see the
[join methods guide](./join-methods.md) for the token resource and the other
supported platforms: gitlab, kubernetes, spacelift).

- **Pros**: fresh, short-lived credentials each run; no static secret to
  rotate; nothing written to disk; smallest workflow.
- **Cons**: requires a join token configured on the cluster.

## Sample Terraform configuration

```hcl
terraform {
  required_version = ">= 1.12.0"

  required_providers {
    teleportconnect = {
      source  = "cruxstack/teleportconnect"
      version = "~> 0.1"
    }
    postgresql = {
      source  = "cyrilgdn/postgresql"
      version = "~> 1.22"
    }
  }

  # Configure a remote backend (S3 + DynamoDB, HCP Terraform, etc.) per your
  # org's conventions; omitted here for brevity. The provider + ephemeral
  # resources work the same regardless of backend.
}

variable "identity_file_path" {
  type        = string
  description = "Path to the Teleport identity file written by the workflow."
}

provider "teleportconnect" {
  proxy_address      = "teleport.example.com:443"
  identity_file_path = var.identity_file_path

  # Defaults to "auto". Set to "yes" if your proxy is fronted by an L7 load
  # balancer (AWS ALB, etc.). See the alpn-conn-upgrade guide.
  alpn_conn_upgrade = "auto"
}

data "teleportconnect_database" "main" {
  name = "mycorp-postgres"
}

ephemeral "teleportconnect_db_tunnel" "main" {
  database = data.teleportconnect_database.main.matched_name
  db_user  = "ci"
  db_name  = "appdb"
}

provider "postgresql" {
  host      = ephemeral.teleportconnect_db_tunnel.main.local_host
  port      = ephemeral.teleportconnect_db_tunnel.main.local_port
  database  = "appdb"
  username  = "ci"
  sslmode   = "disable" # loopback hop only; Teleport encrypts host->proxy->db
  superuser = false
}

resource "postgresql_database" "app" {
  name = "app"
}

resource "postgresql_role" "app" {
  name  = "app"
  login = true
}
```

## Sample workflow

### Recipe A workflow (identity file from secret)

```yaml
name: terraform

on:
  push:
    branches: [main]

jobs:
  apply:
    runs-on: [self-hosted, linux, x64]
    steps:
      - uses: actions/checkout@v4

      - uses: hashicorp/setup-terraform@v3
        with:
          terraform_version: "1.12.2"

      - name: Write Teleport identity file
        id: identity
        env:
          TELEPORT_IDENTITY: ${{ secrets.TELEPORT_IDENTITY }}
        run: |
          umask 077
          path="${RUNNER_TEMP}/teleport-identity"
          printf '%s' "$TELEPORT_IDENTITY" > "$path"
          echo "path=$path" >> "$GITHUB_OUTPUT"

      - name: Terraform init
        run: terraform init

      - name: Terraform apply
        run: terraform apply -auto-approve
        env:
          TF_VAR_identity_file_path: ${{ steps.identity.outputs.path }}

      - name: Clean up identity file
        if: always()
        run: rm -f "${RUNNER_TEMP}/teleport-identity"
```

### Recipe B workflow (native delegated join)

No tbot download, no identity file, no cleanup step. The provider config sets
`join_method = "github"` + `join_token`; the workflow just needs the
`id-token: write` permission.

```yaml
name: terraform

on:
  push:
    branches: [main]

permissions:
  id-token: write # lets the provider fetch the GitHub OIDC token
  contents: read

jobs:
  apply:
    runs-on: [self-hosted, linux, x64]
    steps:
      - uses: actions/checkout@v4

      - uses: hashicorp/setup-terraform@v3
        with:
          terraform_version: "1.12.2"

      - name: Terraform init
        run: terraform init

      - name: Terraform apply
        run: terraform apply -auto-approve
```

## ALPN connection upgrade

If your proxy sits behind an L7 load balancer (such as an AWS ALB) that
terminates TLS with its own certificate, set `alpn_conn_upgrade = "yes"` on the
provider. The default `auto` probes the proxy but is unreliable for some load
balancers. See the [ALPN connection upgrade guide](./alpn-conn-upgrade.md).

There are three independent connection-upgrade knobs because some topologies
need different values for each dial:

- `alpn_conn_upgrade` — the db/SSH tunnels.
- `join_alpn_conn_upgrade` — the delegated-join handshake.
- `auth_alpn_conn_upgrade` — the post-join auth client.

All default to `auto`. On an L4 load balancer with a private endpoint, the join
handshake must not upgrade (it would verify the proxy's resolved private IP and
fail), while the post-join auth client must route through the proxy. A working
combination there is `join_alpn_conn_upgrade = "no"` and
`auth_alpn_conn_upgrade = "yes"` (plus `alpn_conn_upgrade = "yes"` if you also
use db tunnels).

## RBAC

The CI identity should have a narrowly scoped role. See the
[Teleport RBAC guide](./teleport-rbac.md) for a sample role limited to the
specific databases, users, and names the pipeline needs.

## Identity file rotation

This applies to **Recipe A** only. The TTL on the signed identity must cover the
time between rotations. The simplest model is calendar-based: an administrator
periodically re-runs `tctl auth sign` and updates the `TELEPORT_IDENTITY`
secret. Recipe B avoids rotation entirely by issuing a fresh, short-lived
identity on every run.

## Debugging

Run with `TF_LOG=DEBUG` to surface the provider's structured logs, including
certificate issuance and the local tunnel address:

```sh
TF_LOG=DEBUG terraform apply
```

Common failures:

- **`tls: failed to verify certificate`** — the proxy presented a cert your
  runner does not trust. Confirm `alpn_conn_upgrade`, and only use
  `insecure = true` against a self-signed dev cluster.
- **`403` / `connection error` when opening a tunnel** — the CI role likely does
  not permit the requested database/user, or `route_to_cluster` does not match.
  Check the role and the `cluster` argument.
- **The `postgresql` provider hangs or resets mid-apply** — ensure nothing
  closes the tunnel early; it stays open for the provider's lifetime within a
  single `terraform` invocation. If you split `plan` and `apply` across jobs,
  each opens its own tunnel.

## Certificate path (verify-full TLS)

When policy requires end-to-end verify-full TLS rather than the in-process
tunnel, issue a certificate instead. The `cyrilgdn/postgresql` provider can take
the client **certificate and key inline** (`clientcert.sslinline = true`), so
the only thing you write to disk is the **public CA bundle** for `sslrootcert`
(which is path-only). Nothing sensitive lands on disk.

### Bare-bones pattern

```hcl
data "teleportconnect_cluster" "this" {}

data "teleportconnect_database" "main" {
  name = "mycorp-postgres"
}

ephemeral "teleportconnect_db_certificate" "main" {
  database = data.teleportconnect_database.main.matched_name
  db_user  = "ci"
  db_name  = "appdb"
}

# Only the public CA bundle is written to disk. It lives under .terraform/,
# which Terraform already ignores. The CA is cluster-scoped, so fetch it from
# the cluster data source once and reuse the path for every database.
resource "local_file" "teleport_ca" {
  filename = "${path.root}/.terraform/teleport-ca/teleport-ca.pem"
  content  = data.teleportconnect_cluster.this.ca_certificate
}

provider "postgresql" {
  host        = ephemeral.teleportconnect_db_certificate.main.host
  port        = ephemeral.teleportconnect_db_certificate.main.port
  database    = "appdb"
  username    = "ci"
  sslmode     = "verify-full"
  sslrootcert = local_file.teleport_ca.filename

  clientcert {
    cert      = ephemeral.teleportconnect_db_certificate.main.certificate
    key       = ephemeral.teleportconnect_db_certificate.main.private_key
    sslinline = true
  }
}
```

### Module pattern

The repo ships an `examples/modules/teleport-postgresql` module that bundles the
cluster data source, the CA `local_file`, and the ephemeral certificate behind a
small interface:

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
```

### Why doesn't the provider write the CA file itself?

A provider *can* technically write files from an ephemeral resource's `Open`
(the framework does not sandbox the filesystem), but
`teleportconnect_db_certificate` deliberately does not, because it would not
actually reduce the on-disk footprint and it weakens the lifecycle guarantees:

- **Cleanup is worse, not better.** An ephemeral resource's `Open` is guaranteed
  to run, but its `Close` is best-effort — a cancelled job, a `SIGKILL`, or a
  crash skips it, leaving the file behind. A `local_file` resource is tracked by
  Terraform and removed on `destroy`, so it cleans up more reliably than a
  provider-side write would.
- **The provider can't know where to write.** `path.root` / `path.module` are
  resolved by Terraform before the value ever reaches the provider, and the
  provider's working directory is sandboxed or redirected under HCP Terraform,
  `terraform -chdir`, Atlantis, and similar. You would have to pass an explicit
  path anyway — at which point a file exists on disk either way.

So the file is unavoidable *if* you use the certificate path, because
`sslrootcert` is path-only. Writing it from `local_file` keeps the path explicit
and Terraform-managed. The CA is public trust material, so `local_file` (not
`local_sensitive_file`) is correct; the secret cert/key never touch disk — they
stay in memory and are passed inline via `sslinline = true`.

**Want zero files on disk?** Use the
[tunnel pattern](#recommended-pattern-db_tunnel--sslmodedisable) instead. With
`teleportconnect_db_tunnel` the `postgresql` provider connects to `localhost`
with `sslmode = "disable"`, so there is no CA file and no `local_file` resource
at all — nothing is written to the runner. On an ephemeral CI runner that is the
cleanest option; reserve the certificate path for when policy mandates
end-to-end verify-full TLS.
