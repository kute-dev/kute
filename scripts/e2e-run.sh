#!/usr/bin/env bash
# One-shot end-to-end run: bring the kind cluster up, run the e2e suite
# against it, tear it down again — the preferred CLAUDE.md workflow, with
# cleanup guaranteed even when the test command fails.
#
# Usage:
#   scripts/e2e-run.sh                       # the PR-gate suite (tag: e2e)
#   scripts/e2e-run.sh -run TestFluxScreens  # extra args go to `go test`
#   KUTE_E2E_TAGS=e2e_soak scripts/e2e-run.sh -run TestEventStorm
#
# Env:
#   KUTE_E2E_KEEP=1   leave the cluster running (for iterating on a failure)
#   KUTE_E2E_REUSE=1  skip the `up` step when a cluster is already provisioned;
#                     a cluster this script did not create is never torn down
#   KUTE_E2E_TAGS     extra build tags on top of `e2e`, space- or comma-
#                     separated. The nightly-only suites each sit behind one
#                     (e2e_soak, e2e_scale, e2e_auth, e2e_pty); without it
#                     their files are not compiled at all, so a -run naming
#                     one of their tests would otherwise match nothing and
#                     exit 0 — a pass for a suite that never ran. See
#                     docs/e2e-testing.md.
#   K8S_VERSION, KUTE_E2E_KUBECONFIG  as scripts/e2e-cluster.sh documents them
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

KUTE_E2E_KUBECONFIG="${KUTE_E2E_KUBECONFIG:-${root}/.kube/e2e.config}"

log() { printf '\033[1;34m==>\033[0m %s\n' "$*" >&2; }

created=0
reused=0

# `go test`'s output is teed here so the run stays live on the terminal while
# still being greppable for the "no tests to run" case below.
outfile="$(mktemp)"

# Only ever tears down a cluster this script created: KUTE_E2E_REUSE means the
# cluster was already there, and destroying someone else's is not this script's
# call to make.
cleanup() {
  local status=$?
  trap - EXIT INT TERM
  rm -f "$outfile"
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

# Build tags: always `e2e`, plus whatever KUTE_E2E_TAGS names. Commas are
# accepted as a separator because that is how `go test -tags` itself has
# always spelled a list, and getting it wrong would silently compile fewer
# files rather than erroring.
tags="e2e"
if [[ -n "${KUTE_E2E_TAGS:-}" ]]; then
  tags="e2e ${KUTE_E2E_TAGS//,/ }"
fi

# ./internal/kube/... carries e2e tests of its own (e2e_lazy_test.go needs the
# unexported kindInformers map), so it runs alongside ./test/e2e/... — the pair
# CLAUDE.md documents. Extra args land ahead of the packages, where `go test`
# wants its flags.
log "running the e2e suite (tags: ${tags})"
status=0
go test -tags "$tags" -count=1 -timeout 15m "$@" ./test/e2e/... ./internal/kube/... \
  | tee "$outfile" || status=${PIPESTATUS[0]}

# A -run that matches nothing is not a pass. Every nightly suite lives behind
# its own build tag, so `scripts/e2e-run.sh -run TestEventStorm` compiles a
# package that does not contain storm_test.go at all: `go test` reports
# "[no tests to run]" and exits 0, which reads as a green suite to anyone who
# believed they had just run it. Turn that into the failure it is, and name
# the tag they most likely meant.
#
# The condition is "*no* package ran anything", not "some package reported
# no tests to run" — the two packages here are always filtered together, so
# a perfectly ordinary `-run TestFluxScreens` leaves ./internal/kube/... with
# nothing to run and must stay green.
if [[ $status -eq 0 ]]; then
  ran="$(grep -c '^ok' "$outfile" || true)"
  skipped="$(grep -c '^ok.*no tests to run' "$outfile" || true)"
  if [[ "$ran" -gt 0 && "$ran" -eq "$skipped" ]]; then
    log "FAIL: every package reported \"no tests to run\" — nothing matched"
    log "the nightly suites need their build tag, e.g. KUTE_E2E_TAGS=e2e_soak (also: e2e_scale, e2e_auth, e2e_pty)"
    status=1
  fi
fi

if [[ $status -ne 0 && "${KUTE_E2E_KEEP:-0}" != "1" ]]; then
  log "KUTE_E2E_KEEP=1 keeps the cluster up for the next run"
fi
exit "$status"
