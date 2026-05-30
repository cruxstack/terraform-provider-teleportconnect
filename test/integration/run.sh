#!/usr/bin/env bash
# Brings up the local Teleport cluster + Postgres, bootstraps an identity,
# runs the acceptance suite against it, then tears everything down.
#
# Verified against teleport-distroless:18 (v18.8.2). The bundled cluster
# registers a Postgres database (appdb-postgres) and an SSH node labeled
# role=bastion, so the database/node data sources, db credentials, and db
# tunnel acceptance tests run end-to-end.
#
# The SSH tunnel acceptance test is opt-in (see its skip note); set
# TC_SSH_TUNNEL_ACCTEST=true to attempt it.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "${HERE}/../.." && pwd)"
COMPOSE="docker compose -f ${HERE}/docker-compose.yml"

cleanup() {
  echo "==> tearing down"
  ${COMPOSE} down -v || true
}
trap cleanup EXIT

echo "==> starting teleport + postgres"
${COMPOSE} up -d

"${HERE}/bootstrap.sh"

export TF_ACC=1
export TC_PROXY_ADDRESS="localhost:3080"
export TC_IDENTITY_FILE_PATH="${HERE}/out/identity"
export TC_ALPN_CONN_UPGRADE="no"
export TC_INSECURE="true"

# Resources registered by teleport.yaml.
export TC_DATABASE_NAME="appdb-postgres"
export TC_DATABASE_USER="dbuser"
export TC_DATABASE_LABEL_KEY="engine"
export TC_DATABASE_LABEL_VALUE="postgres"
export TC_NODE_HOSTNAME="teleportconnect-it"
export TC_NODE_LABEL_KEY="role"
export TC_NODE_LABEL_VALUE="bastion"

# SSH tunnel target (opt-in).
export TC_SSH_GATEWAY_NODE="teleportconnect-it"
export TC_SSH_LOGIN="root"
export TC_SSH_TARGET_HOST="postgres"
export TC_SSH_TARGET_PORT="5432"

echo "==> running acceptance tests"
( cd "${ROOT}" && go test ./internal/provider/... -run TestAcc -v -count=1 -timeout 30m )
