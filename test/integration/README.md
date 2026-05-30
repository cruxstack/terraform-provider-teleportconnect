# Local integration testing

This directory contains a single-node Teleport cluster you can run locally to
exercise the `teleportconnect` provider's acceptance tests without a shared
cluster.

> **Warning**: This setup is for local testing only. It uses a self-signed
> certificate and a static join token. Never expose it to a network or reuse its
> tokens.

## Requirements

- Docker (with `docker compose`)
- Go (to run the acceptance tests)
- Teleport **v15+** image (the compose file pins a v16 distroless image)

## Quick start

```sh
# from the repo root
make testacc-local
```

`run.sh` will:

1. `docker compose up -d` the Teleport container.
2. Run `bootstrap.sh` to create a test user/role and sign an identity file into
   `./out/identity`.
3. Export the `TC_*` environment variables and run the acceptance suite.
4. Tear the cluster down on exit.

## Running tests manually

```sh
docker compose -f test/integration/docker-compose.yml up -d
./test/integration/bootstrap.sh

export TF_ACC=1
export TC_PROXY_ADDRESS=localhost:3080
export TC_IDENTITY_FILE_PATH=$(pwd)/test/integration/out/identity
export TC_ALPN_CONN_UPGRADE=no

go test ./internal/provider/... -v
```

## Acceptance test environment variables

| Variable                                                                             | Purpose                                                 |
| ------------------------------------------------------------------------------------ | ------------------------------------------------------- |
| `TC_PROXY_ADDRESS`                                                                   | Proxy `host:port`. Required to run any acceptance test. |
| `TC_IDENTITY_FILE_PATH`                                                              | Path to an identity file (preferred for CI).            |
| `TC_IDENTITY_FILE_DATA`                                                              | Inline identity file contents (alternative).            |
| `TC_ALPN_CONN_UPGRADE`                                                               | `auto` / `yes` / `no`. Defaults to `auto`.              |
| `TC_DATABASE_NAME`                                                                   | Registered database name for the database tests.        |
| `TC_DATABASE_USER`                                                                   | Database user for credential/tunnel tests.              |
| `TC_DATABASE_LABEL_KEY` / `TC_DATABASE_LABEL_VALUE`                                  | Label selector for the by-labels database test.         |
| `TC_NODE_HOSTNAME`                                                                   | Node hostname for the node test.                        |
| `TC_NODE_LABEL_KEY` / `TC_NODE_LABEL_VALUE`                                          | Label selector for the by-labels node test.             |
| `TC_SSH_GATEWAY_NODE` / `TC_SSH_LOGIN` / `TC_SSH_TARGET_HOST` / `TC_SSH_TARGET_PORT` | SSH tunnel test parameters.                             |

Tests for which the required variables are unset are skipped, so a partial
environment still runs a useful subset.

## Notes

- The out-of-the-box cluster has no registered databases. To run the database
  data-source / credential / tunnel tests you must register a database with
  Teleport (e.g. add a `db_service` to `teleport.yaml` pointing at a local
  Postgres container) and set the `TC_DATABASE_*` variables.
- The SSH service in the bundled config is labeled `role=bastion`, `env=it`, so
  the node data-source tests can target it by label.
