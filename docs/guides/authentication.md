---
page_title: "Authentication"
subcategory: ""
---

# Authentication

The `teleportconnect` provider authenticates to your Teleport cluster using
the Teleport API client. Exactly one authentication method must be
configured on the provider block.

## Local profile (`use_local_profile`)

Reuses the credentials from your local `~/.tsh` profile, exactly as if you
had run `tsh login`. Most convenient for interactive use on a workstation.

```hcl
provider "teleportconnect" {
  proxy_address     = "teleport.example.com:443"
  use_local_profile = true
}
```

Run `tsh login --proxy teleport.example.com:443` before running Terraform.
The profile is matched by the host portion of `proxy_address`.

## Identity file (`identity_file_path` / `identity_file_data`)

Best for non-interactive runners (CI, HCP Terraform, Spacelift, etc.). An
identity file bundles a user/bot certificate, its private key, and the
cluster CAs into a single PEM file.

### Producing an identity file with `tctl auth sign`

```sh
tctl auth sign --user ci-bot --out ./identity --ttl 8h --format file
```

```hcl
provider "teleportconnect" {
  proxy_address      = "teleport.example.com:443"
  identity_file_path = "./identity"
}
```

### Producing an identity file with `tbot`

For long-running automation, run Machine ID (`tbot`) as a sidecar that
continuously renews an identity file on disk, then point the provider at it:

```hcl
provider "teleportconnect" {
  proxy_address      = "teleport.example.com:443"
  identity_file_path = "/var/run/teleport/identity"
}
```

### Inline identity data

Pass the identity contents directly (for example from a secrets manager)
using `identity_file_data`. The value is marked sensitive.

```hcl
provider "teleportconnect" {
  proxy_address      = "teleport.example.com:443"
  identity_file_data = var.teleport_identity # sensitive
}
```

## Delegated join methods

Native delegated Machine ID join methods (`iam`, `github`, `gcp`,
`spacelift`, `kubernetes`, ...) are **not yet implemented** in this provider.
Until they are, run `tbot` with the appropriate join method as a sidecar and
point `identity_file_path` at the identity it writes.

## Insecure mode

`insecure = true` skips verification of the proxy TLS certificate (equivalent
to `tsh --insecure`). Only appropriate for local testing against a
self-signed cluster; never use it in production.
