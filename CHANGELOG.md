# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

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
[unreleased]: https://github.com/cruxstack/terraform-provider-teleportconnect/compare/v0.1.0...HEAD
