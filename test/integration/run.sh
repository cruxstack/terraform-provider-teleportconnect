#!/usr/bin/env bash
# Brings up the local Teleport cluster, bootstraps an identity, runs the
# acceptance test suite against it, then tears everything down.
#
# Note: the data-source and ephemeral acceptance tests need a registered
# database and/or SSH target to assert against. Out of the box this harness
# only exercises the provider plumbing (configure + ping). Register a sample
# database in teleport.yaml and export the TC_DATABASE_* vars to run the
# full suite.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
COMPOSE="docker compose -f ${HERE}/docker-compose.yml"

cleanup() {
  echo "==> tearing down"
  ${COMPOSE} down -v || true
}
trap cleanup EXIT

echo "==> starting teleport"
${COMPOSE} up -d

"${HERE}/bootstrap.sh"

export TF_ACC=1
export TC_PROXY_ADDRESS="localhost:3080"
export TC_IDENTITY_FILE_PATH="${HERE}/out/identity"
export TC_ALPN_CONN_UPGRADE="no"

echo "==> running acceptance tests"
( cd "${HERE}/../.." && go test ./internal/provider/... -v -count=1 -timeout 30m )
