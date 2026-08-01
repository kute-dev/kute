#!/usr/bin/env bash
# One-shot end-to-end run: bring the kind cluster up, run the e2e suite
# against it, tear it down again — the three commands CLAUDE.md documents
# separately, wrapped so a failing test still takes the cluster down.
#
# Usage:
#   scripts/e2e-run.sh                       # whole suite
#   scripts/e2e-run.sh -run TestFluxScreens  # extra args go to `go test`
#
# Env:
#   KUTE_E2E_KEEP=1   leave the cluster running (for iterating on a failure)
#   KUTE_E2E_REUSE=1  skip the `up` step when a cluster is already provisioned;
#                     a cluster this script did not create is never torn down
#   K8S_VERSION, KUTE_E2E_KUBECONFIG  as scripts/e2e-cluster.sh documents them
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

KUTE_E2E_KUBECONFIG="${KUTE_E2E_KUBECONFIG:-${root}/.kube/e2e.config}"

log() { printf '\033[1;34m==>\033[0m %s\n' "$*" >&2; }

created=0
reused=0

# Only ever tears down a cluster this script created: KUTE_E2E_REUSE means the
# cluster was already there, and destroying someone else's is not this script's
# call to make.
cleanup() {
  local status=$?
  trap - EXIT INT TERM
  if [[ $created -eq 1 && "${KUTE_E2E_KEEP:-0}" != "1" ]]; then
    log "tearing down the e2e cluster"
    scripts/e2e-cluster.sh down || true
  elif [[ $created -eq 1 || $reused -eq 1 ]]; then
    log "leaving the cluster up (scripts/e2e-cluster.sh down to remove it)"
  fi
  exit "$status"
}
trap cleanup EXIT INT TERM

if [[ "${KUTE_E2E_REUSE:-0}" == "1" && -f "$KUTE_E2E_KUBECONFIG" ]]; then
  log "KUTE_E2E_REUSE=1 — using the existing cluster"
  reused=1
else
  log "bringing the e2e cluster up"
  scripts/e2e-cluster.sh up
  created=1
fi

# ./internal/kube/... carries e2e tests of its own (e2e_lazy_test.go needs the
# unexported kindInformers map), so it runs alongside ./test/e2e/... — the pair
# CLAUDE.md documents. Extra args land ahead of the packages, where `go test`
# wants its flags.
log "running the e2e suite"
status=0
go test -tags e2e -count=1 -timeout 15m "$@" ./test/e2e/... ./internal/kube/... || status=$?
if [[ $status -ne 0 && "${KUTE_E2E_KEEP:-0}" != "1" ]]; then
  log "KUTE_E2E_KEEP=1 keeps the cluster up for the next run"
fi
exit "$status"
