---
page_title: "Delegated join methods"
subcategory: ""
---

# Delegated join methods

The provider can authenticate to Teleport using a **delegated Machine ID
join**: it fetches the CI platform's OIDC/JWT identity token and exchanges it
with the Teleport proxy's JoinService for short-lived certificates, entirely
in-process. There is no identity file to manage and no `tbot` sidecar to run.

```hcl
provider "teleportconnect" {
  proxy_address = "teleport.example.com:443"
  join_method   = "github" # github | gitlab | kubernetes | spacelift
  join_token    = "teleportconnect-ci"

  # Optional. Defaults to the proxy host. Override only if the join token
  # expects a different audience claim than the proxy hostname.
  # join_audience = "teleport.example.com"
}
```

Supported methods: `github`, `gitlab`, `kubernetes`, `spacelift`.

The certificates issued by the join are held only in memory for the duration
of the Terraform run. Nothing is written to disk or to Terraform state.

## How the audience works

Every OIDC join validates an `aud` (audience) claim on the identity token
against the join token's configuration. This provider defaults `join_audience`
to the proxy host (e.g. `teleport.example.com`). The token must be minted with
a matching audience:

- **GitHub** — the provider requests the token with the audience as a query
  parameter, so it is set automatically from `join_audience`.
- **GitLab / Kubernetes / Spacelift** — the audience is fixed when the token
  is minted (in the `id_tokens` block, the projected service-account token, or
  by Spacelift), so configure the Teleport join token to expect that value and
  set `join_audience` to match if it is not the proxy host.

A mismatched audience is the most common cause of a failed join; the error
surfaced by the provider includes the audience it used.

## GitHub Actions

Requires `permissions: id-token: write` on the job. The provider reads
`ACTIONS_ID_TOKEN_REQUEST_URL` / `ACTIONS_ID_TOKEN_REQUEST_TOKEN` (set by the
runner) and requests a token with the configured audience.

```yaml
permissions:
  id-token: write
  contents: read
```

```hcl
provider "teleportconnect" {
  proxy_address = "teleport.example.com:443"
  join_method   = "github"
  join_token    = "teleportconnect-ci"
}
```

Example Teleport join token (configure the `allow` rules to match your repo):

```yaml
kind: token
version: v2
metadata:
  name: teleportconnect-ci
spec:
  roles: [Bot]
  join_method: github
  bot_name: terraform-ci
  github:
    allow:
      - repository: my-org/my-repo
        ref: refs/heads/main
```

## GitLab CI/CD

Declare an `id_tokens` entry on the job with `aud` set to the join token's
audience. The provider reads `$TELEPORT_ID_TOKEN` by default; override the
variable name with `TELEPORT_GITLAB_ID_TOKEN_ENV` if you use a different name.

```yaml
terraform:
  id_tokens:
    TELEPORT_ID_TOKEN:
      aud: https://teleport.example.com
  script:
    - terraform apply -auto-approve
```

```hcl
provider "teleportconnect" {
  proxy_address = "teleport.example.com:443"
  join_method   = "gitlab"
  join_token    = "teleportconnect-ci"
  join_audience = "https://teleport.example.com"
}
```

## Kubernetes

Mount a projected service-account token whose audience matches the join token,
and point the provider at it with `TELEPORT_KUBERNETES_TOKEN_PATH`. The default
pod token (audience = API server) only works with in-cluster Kubernetes join;
for JWKS/static join use a projected token with the correct audience.

```yaml
volumes:
  - name: teleport-token
    projected:
      sources:
        - serviceAccountToken:
            path: token
            audience: teleport.example.com
            expirationSeconds: 600
```

```hcl
provider "teleportconnect" {
  proxy_address = "teleport.example.com:443"
  join_method   = "kubernetes"
  join_token    = "teleportconnect-ci"
}
```

Set `TELEPORT_KUBERNETES_TOKEN_PATH=/var/run/secrets/teleport/token` (or
wherever you mounted the projected token).

## Spacelift

Spacelift injects `$SPACELIFT_OIDC_TOKEN` into every run. Its audience is the
Spacelift account hostname (e.g. `<account>.app.spacelift.io`), so set
`join_audience` accordingly and configure the Teleport spacelift join token to
expect it.

```hcl
provider "teleportconnect" {
  proxy_address = "teleport.example.com:443"
  join_method   = "spacelift"
  join_token    = "teleportconnect-ci"
  join_audience = "myaccount.app.spacelift.io"
}
```

## RBAC

The join token's `bot_name` must map to a Teleport bot whose role grants the
access the pipeline needs (read `db_server` / `node`, issue db/ssh certs for
the target users). See the [Teleport RBAC guide](./teleport-rbac.md).

## Certificate lifetime

The join issues certificates with roughly a one-hour TTL (the auth server may
clamp this to the join token's configured maximum). This covers typical CI
runs. A multi-hour `terraform apply` that outlives the certificate is not yet
handled (no in-process renewal); split very long runs or raise the token TTL.

## Troubleshooting

- **`token validation failed` / audience errors** — the `aud` claim does not
  match the join token. Confirm `join_audience` and how the token is minted.
- **GitHub: `ACTIONS_ID_TOKEN_REQUEST_URL not set`** — add
  `permissions: id-token: write` to the job.
- **GitLab: no token found** — declare the `id_tokens` block and, if it is not
  named `TELEPORT_ID_TOKEN`, set `TELEPORT_GITLAB_ID_TOKEN_ENV`.
- **`certificate is valid for ... not <proxy>` (auth server cert)** — the
  post-join auth client reached the auth server directly instead of routing
  through the proxy. Set `auth_alpn_conn_upgrade = "yes"` so the post-join
  connection is ALPN-routed through the proxy.
- **`cannot validate certificate for <ip> ... no IP SANs`** — the join
  handshake's connection upgrade verified the proxy's resolved private IP
  (common behind an L4 load balancer with a private endpoint). Set
  `join_alpn_conn_upgrade = "no"`.
- The join handshake (`join_alpn_conn_upgrade`), the post-join auth dial
  (`auth_alpn_conn_upgrade`), and the tunnels (`alpn_conn_upgrade`) each have
  an independent upgrade knob because some topologies need different values
  for each. On an L4 LB + private endpoint, a working combination is
  `join_alpn_conn_upgrade = "no"`, `auth_alpn_conn_upgrade = "yes"`, and
  `alpn_conn_upgrade = "yes"` (the last only if you also use db tunnels).
- Run with `TF_LOG=DEBUG` to see the join method and audience the provider
  used.
