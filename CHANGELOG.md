# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed

- Delegated-join (bot) identities now issue database and SSH certificates that
  carry the bot's actual access roles, so the database/SSH agent authorizes the
  connection. A bot authenticates as `bot-<name>` whose only role is a wrapper
  that grants access via role impersonation; issuing certs keyed on the bot user
  alone produced certificates without the access roles and the agent denied the
  connection (`access to db denied`). The provider now discovers the wrapper
  role's impersonated roles and requests them (`RoleRequests` +
  `UseRoleRequests`), matching how `tsh`/`tbot` issue resource certs. Normal
  user identities are unaffected.
  ([#5](https://github.com/cruxstack/terraform-provider-teleportconnect/issues/5))

### Changed

- The delegated-join post-join auth client now connects through the proxy using
  the same path as `tsh`/`tbot` (`api/client/proxy.Client.ClientConfig`),
  instead of a hand-built dialer. The join now also requests an SSH certificate
  so the proxy client can be constructed. This makes the provider's behavior on
  proxy-fronted clusters (TLS routing, L4/L7 load balancers, PrivateLink) match
  the official Teleport clients.
  ([#5](https://github.com/cruxstack/terraform-provider-teleportconnect/issues/5))

### Added

- `join_alpn_conn_upgrade` and `auth_alpn_conn_upgrade` provider attributes
  (both default `auto`) give the delegated-join handshake and the post-join auth
  client independent HTTPS connection-upgrade controls, separate from
  `alpn_conn_upgrade` (which controls tunnels). Some topologies need different
  values for each dial: behind an L4 load balancer with a private endpoint the
  join handshake must not upgrade (it would verify the proxy's resolved private
  IP and fail with a no-IP-SANs error) while the post-join auth client must be
  ALPN-routed through the proxy. A working combination there is
  `join_alpn_conn_upgrade = "no"` and `auth_alpn_conn_upgrade = "yes"`.
  ([#5](https://github.com/cruxstack/terraform-provider-teleportconnect/issues/5))

## [0.2.4] - 2026-05-31

### Fixed

- Delegated join: the post-join API client is now pinned to an explicit dialer
  that routes through the proxy, instead of being given the proxy as an `Addrs`
  entry. With `Addrs`, the Teleport SDK could fall back to dialing the auth
  server directly, which fails (or is unreachable) on proxy-only topologies
  where the auth service is internal — surfacing as a TLS error against the auth
  server's internal certificate (`*.teleport.cluster.local`). The pinned dialer
  removes that fallback and honors `alpn_conn_upgrade`.
  ([#5](https://github.com/cruxstack/terraform-provider-teleportconnect/issues/5))

### Known limitations

- On proxies behind an L7 load balancer that also require PrivateLink (so the
  proxy hostname resolves to a private IP), forcing `alpn_conn_upgrade = "yes"`
  can fail the join with an IP-SAN error: the upstream SDK's connection-upgrade
  step infers the TLS server name from the resolved address. For an L4 NLB the
  upgrade is unnecessary, so `alpn_conn_upgrade = "no"` is the correct setting
  there.

## [0.2.3] - 2026-05-31

### Fixed

- Delegated join now honors the provider's `alpn_conn_upgrade` setting for the
  join and post-join auth dials. Previously these paths always used the
  `IsALPNConnUpgradeRequired` probe, which is unreliable behind some L7 load
  balancers (e.g. AWS NLB + PrivateLink): with `alpn_conn_upgrade = "yes"` the
  probe could still return false, the HTTPS upgrade was skipped, and the client
  fell back to dialing the auth server directly and failed against its internal
  certificate. The setting now applies to the join/auth path the same way it
  already applies to tunnels (`yes`/`no`/`auto`).
  ([#5](https://github.com/cruxstack/terraform-provider-teleportconnect/issues/5))

## [0.2.2] - 2026-05-31

### Fixed

- Delegated join: the post-join API client now embeds the auth ALPN route and
  proxy SNI directly in its TLS config so the connection terminates at the
  proxy's public certificate and routes to auth via TLS routing. The v0.2.1 fix
  relied on the SDK's `ALPNSNIAuthDialClusterName`, which on some proxy-fronted
  topologies (e.g. an NLB + PrivateLink proxy with a separate auth load
  balancer) still dialed the auth server directly and failed against the auth
  server's internal certificate. This mirrors the working join-service and
  database-tunnel dial paths.
  ([#5](https://github.com/cruxstack/terraform-provider-teleportconnect/issues/5))

## [0.2.1] - 2026-05-31

### Fixed

- Delegated join (`join_method` + `join_token`) now routes the post-join API
  client through the proxy via ALPN-SNI auth dial, so TLS verifies against the
  proxy's public certificate instead of the cluster identity. Previously the
  join handshake succeeded but the subsequent client connection failed against
  proxy-fronted clusters (TLS routing, public proxy cert) with an SNI mismatch
  (`certificate is valid for <proxy>, not <cluster>`). The cluster name is
  derived automatically from the issued certificate; no new configuration is
  required.
  ([#5](https://github.com/cruxstack/terraform-provider-teleportconnect/issues/5))

## [0.2.0] - 2026-05-30

### Added

- Delegated Machine ID join auth mode: set `join_method` + `join_token` (and
  optional `join_audience`) on the provider to join the cluster in-process from
  CI, with no identity file and no `tbot` sidecar. Supported methods: `github`,
  `gitlab`, `kubernetes`, `spacelift`. Implemented against the Apache-2.0 `api/`
  module only.
- `data.teleportconnect_cluster` — exposes the cluster name, server version, and
  the cluster TLS CA bundle (`ca_certificate`). The CA is cluster-scoped, so it
  can be written to a single file and reused as `sslrootcert` across many
  database configurations.
- `examples/modules/teleport-postgresql` — a reusable module that wires the
  cluster CA, a `local_file`, and an ephemeral database certificate together for
  the `cyrilgdn/postgresql` provider (verify-full TLS).

### Changed

- CI guide certificate path rewritten to pass the client certificate and key
  inline via `clientcert.sslinline = true`, leaving only the public CA bundle on
  disk (one `local_file` instead of three `local_sensitive_file`s).

### Upgrade notes

- No breaking changes. The existing `use_local_profile`, `identity_file_path`,
  and `identity_file_data` modes are unchanged; `join_method` is purely additive
  and mutually exclusive with them.

## [0.1.0] - 2026-05-30

Initial public release.

### Added

- Provider configuration with `use_local_profile`, `identity_file_path`, and
  `identity_file_data` authentication modes.
- `data.teleportconnect_database` — look up a Teleport database by name and/or
  labels.
- `data.teleportconnect_node` — look up a Teleport SSH node by hostname and/or
  labels.
- `ephemeral.teleportconnect_db_certificate` — issue a short-lived database
  client certificate (`certificate`, `private_key`, `ca_certificate`) and the
  proxy host/port.
- `ephemeral.teleportconnect_db_tunnel` — open a local TCP listener proxied to a
  Teleport-protected database via TLS routing.
- `ephemeral.teleportconnect_ssh_tunnel` — open a local TCP listener proxied
  through a Teleport-managed SSH gateway node.
- `alpn_conn_upgrade` option to control HTTPS connection upgrade for proxies
  behind L7 load balancers.
- CI usage guide covering minimal self-hosted GitHub Actions runners,
  identity-file vs. `tbot` authentication, and integration with the
  `cyrilgdn/postgresql` provider via the `db_tunnel` ephemeral resource.

### Fixed

- The `insecure` provider flag is now honored by the SSH tunnel's proxy
  transport (it was previously only applied to the API client and DB tunnel), so
  self-signed dev clusters work end-to-end.
- The SSH tunnel now resolves a concrete cluster name for the proxy `DialHost`
  transport when no leaf cluster is set, fixing a 403 against single-cluster
  proxies.

### Security

- User certificates use ECDSA P-256 keys (requires Teleport v15+).
- Tunnels force-close in-flight connections on shutdown so no sockets leak past
  `terraform apply`.
- SSH host keys are verified against the cluster SSH host CAs (no TOFU).

[0.1.0]: https://github.com/cruxstack/terraform-provider-teleportconnect/releases/tag/v0.1.0
[0.2.0]: https://github.com/cruxstack/terraform-provider-teleportconnect/compare/v0.1.0...v0.2.0
[0.2.1]: https://github.com/cruxstack/terraform-provider-teleportconnect/compare/v0.2.0...v0.2.1
[0.2.2]: https://github.com/cruxstack/terraform-provider-teleportconnect/compare/v0.2.1...v0.2.2
[0.2.3]: https://github.com/cruxstack/terraform-provider-teleportconnect/compare/v0.2.2...v0.2.3
[0.2.4]: https://github.com/cruxstack/terraform-provider-teleportconnect/compare/v0.2.3...v0.2.4
[unreleased]: https://github.com/cruxstack/terraform-provider-teleportconnect/compare/v0.2.4...HEAD
