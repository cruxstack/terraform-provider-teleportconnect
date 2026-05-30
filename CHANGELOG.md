# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0] - Unreleased

Initial public release.

### Added

- Provider configuration with `use_local_profile`, `identity_file_path`, and
  `identity_file_data` authentication modes.
- `data.teleportconnect_database` — look up a Teleport database by name and/or
  labels.
- `data.teleportconnect_node` — look up a Teleport SSH node by hostname and/or
  labels.
- `ephemeral.teleportconnect_db_credentials` — issue a short-lived database
  client certificate and the cluster CA bundle.
- `ephemeral.teleportconnect_db_tunnel` — open a local TCP listener proxied to a
  Teleport-protected database via TLS routing.
- `ephemeral.teleportconnect_ssh_tunnel` — open a local TCP listener proxied
  through a Teleport-managed SSH gateway node.
- `alpn_conn_upgrade` option to control HTTPS connection upgrade for proxies
  behind L7 load balancers.

### Security

- User certificates use ECDSA P-256 keys (requires Teleport v15+).
- Tunnels force-close in-flight connections on shutdown so no sockets leak past
  `terraform apply`.
- SSH host keys are verified against the cluster SSH host CAs (no TOFU).

[0.1.0]: https://github.com/cruxstack/terraform-provider-teleportconnect/releases/tag/v0.1.0
[unreleased]: https://github.com/cruxstack/terraform-provider-teleportconnect/compare/v0.1.0...HEAD
