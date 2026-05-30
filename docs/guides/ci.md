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
`tsh`, no `tbot` system install, no `psql`. The `teleportconnect` provider
does the Teleport authentication and TLS routing in-process.

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
  host     = ephemeral.teleportconnect_db_tunnel.main.local_host
  port     = ephemeral.teleportconnect_db_tunnel.main.local_port
  database = "appdb"
  username = "ci"
  sslmode  = "disable" # the tunnel terminates TLS to Teleport for you
  superuser = false
}
```

Why the tunnel rather than the certificate resource? The `cyrilgdn/postgresql`
provider's `clientcert.cert`, `clientcert.key`, and `sslrootcert` arguments
expect **file paths**, so the certificate path forces you to materialize PEM
material to disk during the run. The tunnel avoids that entirely: Terraform
connects to `localhost`, the certificate never leaves the provider process,
and nothing sensitive is written to disk or to Terraform state.

(If you specifically need end-to-end verify-full TLS, see the
[certificate-based path](#appendix-certificate-based-path) appendix.)

## One-time prerequisites (off the runner)

These are done once by an administrator against your Teleport cluster, not on
the CI runner.

1. Create a least-privilege role for CI. See the
   [Teleport RBAC guide](./teleport-rbac.md) for the role spec; at minimum it
   needs to read `db_server` and issue certificates for the target
   `db_users` / `db_names`.
2. Create a user (or bot) with that role:

   ```sh
   tctl users add terraform-ci --roles terraform-ci
   ```

3. Choose an authentication recipe below.

## Authentication recipes

Both recipes set `identity_file_path` on the provider. Pick one.

### Recipe A — identity file from a GitHub Actions secret

An administrator pre-signs an identity file and stores it as an encrypted
Actions secret. The workflow writes it to a temp file at runtime.

Sign the identity (off the runner) and copy its contents into a repository or
organization secret named `TELEPORT_IDENTITY`:

```sh
tctl auth sign --user terraform-ci --ttl 24h --format file --out ./identity
# paste the contents of ./identity into the TELEPORT_IDENTITY secret
```

* **Pros**: zero binary downloads on the runner; works on the most locked-down
  images.
* **Cons**: the identity has a fixed TTL and must be re-signed and the secret
  updated before it expires (see [rotation](#identity-file-rotation)).

### Recipe B — tbot as a workflow pre-step

Download the single `tbot` binary at runtime and use a GitHub join token
(GitHub OIDC) to write a fresh identity each run. Nothing is installed
system-wide and there is no long-lived secret to rotate.

This requires a one-time bot + `github` join token configured on the cluster
(see the Teleport Machine ID docs). The workflow step downloads `tbot`,
runs it once, and points the provider at the identity it writes.

* **Pros**: fresh, short-lived identity each run; no static secret to rotate;
  aligns with the delegated-join roadmap.
* **Cons**: downloads a `tbot` binary each run (cache it with `actions/cache`
  if you want); requires a join token configured on the cluster.

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
  sslmode   = "disable"
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

### Recipe B workflow (tbot pre-step)

```yaml
name: terraform

on:
  push:
    branches: [main]

permissions:
  id-token: write # required for the GitHub join method
  contents: read

jobs:
  apply:
    runs-on: [self-hosted, linux, x64]
    steps:
      - uses: actions/checkout@v4

      - uses: hashicorp/setup-terraform@v3
        with:
          terraform_version: "1.12.2"

      - name: Install tbot
        run: |
          umask 077
          ver="16.4.0" # pin to your cluster's major version
          curl -fsSL "https://cdn.teleport.dev/teleport-v${ver}-linux-amd64-bin.tar.gz" \
            | tar -xz -C "${RUNNER_TEMP}" teleport/tbot
          echo "${RUNNER_TEMP}/teleport" >> "$GITHUB_PATH"

      - name: Write Teleport identity with tbot
        id: identity
        run: |
          umask 077
          path="${RUNNER_TEMP}/teleport-identity"
          tbot start \
            --oneshot \
            --destination-dir "${RUNNER_TEMP}/tbot" \
            --join-method github \
            --token terraform-ci-github \
            --proxy-server teleport.example.com:443
          cp "${RUNNER_TEMP}/tbot/identity" "$path"
          echo "path=$path" >> "$GITHUB_OUTPUT"

      - name: Terraform init
        run: terraform init

      - name: Terraform apply
        run: terraform apply -auto-approve
        env:
          TF_VAR_identity_file_path: ${{ steps.identity.outputs.path }}

      - name: Clean up identity
        if: always()
        run: rm -rf "${RUNNER_TEMP}/teleport-identity" "${RUNNER_TEMP}/tbot"
```

## ALPN connection upgrade

If your proxy sits behind an L7 load balancer (such as an AWS ALB) that
terminates TLS with its own certificate, set `alpn_conn_upgrade = "yes"` on
the provider. The default `auto` probes the proxy but is unreliable for some
load balancers. See the
[ALPN connection upgrade guide](./alpn-conn-upgrade.md).

## RBAC

The CI identity should have a narrowly scoped role. See the
[Teleport RBAC guide](./teleport-rbac.md) for a sample role limited to the
specific databases, users, and names the pipeline needs.

## Identity file rotation

This applies to **Recipe A** only. The TTL on the signed identity must cover
the time between rotations. The simplest model is calendar-based: an
administrator periodically re-runs `tctl auth sign` and updates the
`TELEPORT_IDENTITY` secret. Recipe B avoids rotation entirely by issuing a
fresh, short-lived identity on every run.

## Debugging

Run with `TF_LOG=DEBUG` to surface the provider's structured logs, including
certificate issuance and the local tunnel address:

```sh
TF_LOG=DEBUG terraform apply
```

Common failures:

* **`tls: failed to verify certificate`** — the proxy presented a cert your
  runner does not trust. Confirm `alpn_conn_upgrade`, and only use
  `insecure = true` against a self-signed dev cluster.
* **`403` / `connection error` when opening a tunnel** — the CI role likely
  does not permit the requested database/user, or `route_to_cluster` does not
  match. Check the role and the `cluster` argument.
* **The `postgresql` provider hangs or resets mid-apply** — ensure nothing
  closes the tunnel early; it stays open for the provider's lifetime within a
  single `terraform` invocation. If you split `plan` and `apply` across jobs,
  each opens its own tunnel.

## Appendix: certificate-based path

If you need end-to-end verify-full TLS (rather than the tunnel), issue a
certificate and materialize it to disk for `cyrilgdn/postgresql`:

```hcl
ephemeral "teleportconnect_db_certificate" "main" {
  database = data.teleportconnect_database.main.matched_name
  db_user  = "ci"
  db_name  = "appdb"
}

resource "local_sensitive_file" "ca" {
  filename = "${path.module}/.tls/ca.pem"
  content  = ephemeral.teleportconnect_db_certificate.main.ca_certificate
}

resource "local_sensitive_file" "cert" {
  filename = "${path.module}/.tls/cert.pem"
  content  = ephemeral.teleportconnect_db_certificate.main.certificate
}

resource "local_sensitive_file" "key" {
  filename = "${path.module}/.tls/key.pem"
  content  = ephemeral.teleportconnect_db_certificate.main.private_key
}

provider "postgresql" {
  host        = ephemeral.teleportconnect_db_certificate.main.host
  port        = ephemeral.teleportconnect_db_certificate.main.port
  database    = "appdb"
  username    = "ci"
  sslmode     = "verify-full"
  sslrootcert = local_sensitive_file.ca.filename

  clientcert {
    cert = local_sensitive_file.cert.filename
    key  = local_sensitive_file.key.filename
  }
}
```

This writes the cert/key/CA to the working directory. Add a cleanup step to
your workflow (`rm -rf .tls`) and ensure the path is never committed or
cached.
