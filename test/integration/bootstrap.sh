#!/usr/bin/env bash
# Bootstraps the local single-node Teleport cluster for acceptance testing:
#   1. waits for the auth service to be healthy
#   2. creates a test user/role with permission to issue db/ssh certs
#   3. signs an identity file the provider can consume
#
# Output: ./test/integration/out/identity (identity file)
#
# This is a helper for local testing only. It assumes the cluster from
# docker-compose.yml is running.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUT="${HERE}/out"
CONTAINER="teleportconnect-it"
USER_NAME="tc-tester"
ROLE_NAME="tc-tester"

mkdir -p "${OUT}"

echo "==> waiting for teleport auth to be ready"
for _ in $(seq 1 30); do
  if docker exec "${CONTAINER}" tctl status >/dev/null 2>&1; then
    break
  fi
  sleep 2
done

echo "==> creating role ${ROLE_NAME}"
docker exec -i "${CONTAINER}" tctl create -f <<'EOF'
kind: role
version: v7
metadata:
  name: tc-tester
spec:
  allow:
    db_labels:
      '*': '*'
    db_users: ['*']
    db_names: ['*']
    node_labels:
      '*': '*'
    logins: ['root', 'ec2-user']
    rules:
      - resources: [db_server, node, db]
        verbs: [read, list]
EOF

echo "==> creating user ${USER_NAME}"
docker exec "${CONTAINER}" tctl users add "${USER_NAME}" --roles "${ROLE_NAME}" || true

echo "==> signing identity file -> ${OUT}/identity"
docker exec "${CONTAINER}" tctl auth sign \
  --user "${USER_NAME}" \
  --out /tmp/identity \
  --ttl 8h \
  --format file
docker cp "${CONTAINER}:/tmp/identity" "${OUT}/identity"

echo "==> done. Identity file at ${OUT}/identity"
echo "    export TC_PROXY_ADDRESS=localhost:3080"
echo "    export TC_IDENTITY_FILE_PATH=${OUT}/identity"
echo "    export TC_ALPN_CONN_UPGRADE=no"
