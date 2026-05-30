#!/usr/bin/env bash
# Bootstraps the local single-node Teleport cluster for acceptance testing:
#   1. waits for the auth service to be healthy
#   2. creates a test role/user with permission to issue db/ssh certs
#   3. signs an identity file the provider can consume
#
# Output: ./test/integration/out/identity
#
# Local testing helper only. Assumes the cluster from docker-compose.yml is
# running. Verified against teleport-distroless:18 (v18.8.2).
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUT="${HERE}/out"
CONTAINER="teleportconnect-it"
USER_NAME="tc-tester"
ROLE_NAME="tc-tester"

mkdir -p "${OUT}"

echo "==> waiting for teleport auth to be ready"
for _ in $(seq 1 30); do
  if docker exec "${CONTAINER}" /usr/local/bin/tctl status >/dev/null 2>&1; then
    break
  fi
  sleep 2
done

echo "==> creating role ${ROLE_NAME}"
docker exec -i "${CONTAINER}" /usr/local/bin/tctl create -f <<'EOF'
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
    logins: ['root']
    rules:
      - resources: [db_server, node, db]
        verbs: [read, list]
EOF

echo "==> creating user ${USER_NAME}"
docker exec "${CONTAINER}" /usr/local/bin/tctl users add "${USER_NAME}" \
  --roles "${ROLE_NAME}" --logins root >/dev/null 2>&1 || true

echo "==> signing identity file -> ${OUT}/identity"
docker exec "${CONTAINER}" /usr/local/bin/tctl auth sign \
  --user "${USER_NAME}" \
  --out /tmp/identity \
  --ttl 8h \
  --format file
docker cp "${CONTAINER}:/tmp/identity" "${OUT}/identity"

echo "==> done. Identity file at ${OUT}/identity"
