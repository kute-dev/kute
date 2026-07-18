#!/usr/bin/env bash
# kwok-prod-cluster-start.sh — (re)start the prod-sim kwok cluster's components.
#
# kwokctl's --runtime binary mode (see kwok-prod-cluster.sh) runs etcd/
# apiserver/scheduler/controller-manager/kwok-controller/metrics-server as
# plain host processes with no restart policy, so they don't survive a reboot
# or a killed session. `kwok-prod-cluster.sh` only checks that the cluster
# exists, not that it's running, so re-running it after that fails with
# "connection refused" against the apiserver port.
#
# This just starts (or no-ops, if already up) those component processes.
#
# Usage:
#   scripts/kwok-prod-cluster-start.sh
#
# Env overrides: CLUSTER_NAME (default prod-sim)

set -euo pipefail

CLUSTER_NAME="${CLUSTER_NAME:-prod-sim}"
CTX="kwok-${CLUSTER_NAME}"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN_DIR="${REPO_ROOT}/scripts/.bin"

log() { printf '\033[1;34m==>\033[0m %s\n' "$*" >&2; }

if [[ -x "${BIN_DIR}/kwokctl" ]]; then
  KWOKCTL="${BIN_DIR}/kwokctl"
elif command -v kwokctl >/dev/null && kwokctl --help 2>/dev/null | head -n1 | grep -q '^kwokctl'; then
  KWOKCTL="$(command -v kwokctl)"
else
  echo "kwokctl not found; run scripts/kwok-prod-cluster.sh first to bootstrap it" >&2
  exit 1
fi

if ! "$KWOKCTL" get clusters 2>/dev/null | grep -qx "$CLUSTER_NAME"; then
  echo "cluster ${CLUSTER_NAME} does not exist; run scripts/kwok-prod-cluster.sh first" >&2
  exit 1
fi

log "Starting cluster ${CLUSTER_NAME} (no-op if already running)"
"$KWOKCTL" --name "$CLUSTER_NAME" start cluster --wait 60s

log "Cluster ${CTX} is up"
kubectl --context "$CTX" get nodes 2>/dev/null | awk '{print "  " $0}'
