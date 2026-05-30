# Local integration testing

A single-node Teleport cluster (plus a Postgres target) you can run locally to
exercise the `teleportconnect` provider's acceptance tests without a shared
cluster.

Verified against `public.ecr.aws/gravitational/teleport-distroless:18`
(v18.8.2), which matches the Teleport API module pinned in `go.mod`.

> **Warning**: For local testing only. It uses a self-signed certificate and a
> static join token. Never expose it to a network or reuse its tokens.

## Requirements

- Docker (with `docker compose`)
- Go (to run the acceptance tests)

## Quick start

```sh
# from the repo root
make testacc-local
```

`run.sh` will:

1. `docker compose up -d` the Teleport + Postgres containers.
2. Run `bootstrap.sh` to create a `tc-tester` role/user and sign an identity
   file into `./out/identity`.
3. Export the `TC_*` variables and run the acceptance suite.
4. Tear the cluster down on exit.

## What the bundled cluster provides

- A registered Postgres database `appdb-postgres` (labels `env=it`,
  `engine=postgres`) — drives the database data source, db credentials, and db
  tunnel tests.
- The single node's SSH service, labeled `role=bastion`, `env=it` — drives the
  node data source tests.

## Verified results

Against the bundled cluster, the following pass end-to-end:

- `TestAccDataDatabase_byName` / `_byLabels`
- `TestAccDataNode_byHostname` / `_byLabels`
- `TestAccEphemeralDBCredentials_basic` (confirms the auth server accepts the
  provider's ECDSA P-256 keys)
- `TestAccEphemeralDBTunnel_basic`

`TestAccEphemeralSSHTunnel_basic` is **opt-in**. The SSH tunnel works under real
Terraform (see `test/local-smoke`), but the proxy transport's `DialHost` returns
a 403 when driven through the in-process `terraform-plugin-testing` harness; the
interaction is environment-specific and still being root-caused. Set
`TC_SSH_TUNNEL_ACCTEST=true` to attempt it.

## Running tests manually

```sh
docker compose -f test/integration/docker-compose.yml up -d
./test/integration/bootstrap.sh

export TF_ACC=1
export TC_PROXY_ADDRESS=localhost:3080
export TC_IDENTITY_FILE_PATH=$(pwd)/test/integration/out/identity
export TC_ALPN_CONN_UPGRADE=no
export TC_INSECURE=true
export TC_DATABASE_NAME=appdb-postgres
export TC_DATABASE_USER=dbuser
export TC_DATABASE_LABEL_KEY=engine
export TC_DATABASE_LABEL_VALUE=postgres
export TC_NODE_HOSTNAME=teleportconnect-it
export TC_NODE_LABEL_KEY=role
export TC_NODE_LABEL_VALUE=bastion

go test ./internal/provider/... -run TestAcc -v
```

## Acceptance test environment variables

| Variable                                                                             | Purpose                                                          |
| ------------------------------------------------------------------------------------ | ---------------------------------------------------------------- |
| `TC_PROXY_ADDRESS`                                                                   | Proxy `host:port`. Required to run any acceptance test.          |
| `TC_IDENTITY_FILE_PATH`                                                              | Path to an identity file (preferred for CI).                     |
| `TC_IDENTITY_FILE_DATA`                                                              | Inline identity file contents (alternative).                     |
| `TC_ALPN_CONN_UPGRADE`                                                               | `auto` / `yes` / `no`. Defaults to `auto`.                       |
| `TC_INSECURE`                                                                        | `true` to skip proxy TLS verification (self-signed dev cluster). |
| `TC_DATABASE_NAME`                                                                   | Registered database name for the database tests.                 |
| `TC_DATABASE_USER`                                                                   | Database user for credential/tunnel tests.                       |
| `TC_DATABASE_LABEL_KEY` / `TC_DATABASE_LABEL_VALUE`                                  | Label selector for the by-labels database test.                  |
| `TC_NODE_HOSTNAME`                                                                   | Node hostname for the node test.                                 |
| `TC_NODE_LABEL_KEY` / `TC_NODE_LABEL_VALUE`                                          | Label selector for the by-labels node test.                      |
| `TC_SSH_GATEWAY_NODE` / `TC_SSH_LOGIN` / `TC_SSH_TARGET_HOST` / `TC_SSH_TARGET_PORT` | SSH tunnel test parameters.                                      |
| `TC_SSH_TUNNEL_ACCTEST`                                                              | `true` to opt in to the SSH tunnel acceptance test.              |

Tests whose required variables are unset are skipped, so a partial environment
still runs a useful subset.
