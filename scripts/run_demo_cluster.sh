#!/usr/bin/env bash

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BINARY="${REPO_ROOT}/dreddit"
DEMO_DIR="/tmp/dreddit_demo_cluster"

mkdir -p "${DEMO_DIR}"/{node1,node2,node3,node4,node5}

cd "$REPO_ROOT"
go build -o dreddit ./cmd/dreddit/

pkill -f "dreddit -id demo-node" 2>/dev/null || true

"$BINARY" -id demo-node1 -addr :8091 -gossip-port 11101 -data-dir "${DEMO_DIR}/node1" > "${DEMO_DIR}/node1.log" 2>&1 &
"$BINARY" -id demo-node2 -addr :8092 -gossip-port 11102 -peers 127.0.0.1:11101 -data-dir "${DEMO_DIR}/node2" > "${DEMO_DIR}/node2.log" 2>&1 &
"$BINARY" -id demo-node3 -addr :8093 -gossip-port 11103 -peers 127.0.0.1:11101,127.0.0.1:11102 -data-dir "${DEMO_DIR}/node3" > "${DEMO_DIR}/node3.log" 2>&1 &
"$BINARY" -id demo-node4 -addr :8094 -gossip-port 11104 -peers 127.0.0.1:11101,127.0.0.1:11102,127.0.0.1:11103 -data-dir "${DEMO_DIR}/node4" > "${DEMO_DIR}/node4.log" 2>&1 &
"$BINARY" -id demo-node5 -addr :8095 -gossip-port 11105 -peers 127.0.0.1:11101,127.0.0.1:11102,127.0.0.1:11103,127.0.0.1:11104 -data-dir "${DEMO_DIR}/node5" > "${DEMO_DIR}/node5.log" 2>&1 &

cat <<EOF

DReddit demo cluster started.

UI URLs:
  http://localhost:8091
  http://localhost:8092
  http://localhost:8093
  http://localhost:8094
  http://localhost:8095

Logs:
  ${DEMO_DIR}/node1.log
  ${DEMO_DIR}/node2.log
  ${DEMO_DIR}/node3.log
  ${DEMO_DIR}/node4.log
  ${DEMO_DIR}/node5.log

To stop the demo cluster:
  pkill -f "dreddit -id demo-node"

EOF