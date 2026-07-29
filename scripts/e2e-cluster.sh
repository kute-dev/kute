#!/usr/bin/env bash
# e2e-cluster.sh — provision the kind cluster the end-to-end suite runs against.
#
# kind, not kwok, is the primary substrate: it has a real kubelet, so pod logs
# (§5b), exec (§10a) and port-forward (§13a) are genuinely exercisable rather
# than simulated, and it runs on GitHub Actions runners unchanged. kwok stays
# the opt-in substrate for the scale row (scripts/e2e-scale-cluster.sh), which
# kind cannot reach.
#
# Usage:
#   scripts/e2e-cluster.sh up            create (or top up) the cluster + fixtures
#   scripts/e2e-cluster.sh down          delete the cluster and both kubeconfigs
#   scripts/e2e-cluster.sh recreate      down, then up
#   scripts/e2e-cluster.sh fixtures      re-apply the fixtures to a running cluster
#   scripts/e2e-cluster.sh kubeconfig    (re)write the admin kubeconfig only
#   scripts/e2e-cluster.sh restricted-kubeconfig
#                                        (re)mint the kute-restricted token kubeconfig
#
# Env:
#   K8S_VERSION                     1.35 (default) or 1.36 — picks kind/config-<v>.yaml
#   KUTE_E2E_KUBECONFIG             default <repo>/.kube/e2e.config
#   KUTE_E2E_RESTRICTED_KUBECONFIG  default <repo>/.kube/e2e-restricted.config
#
# Both default *outside* .kube/config, which mise.toml points KUBECONFIG at and
# which holds the developer's own contexts: an e2e run must never rewrite the
# file the developer's own `go run ./cmd/kute` reads.

set -euo pipefail

K8S_VERSION="${K8S_VERSION:-1.35}"
NAMESPACE=kute-e2e
RESTRICTED_SA=kute-restricted

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
KIND_CONFIG="${REPO_ROOT}/kind/config-${K8S_VERSION}.yaml"
FIXTURE_DIR="${REPO_ROOT}/test/e2e/fixtures"

KUTE_E2E_KUBECONFIG="${KUTE_E2E_KUBECONFIG:-${REPO_ROOT}/.kube/e2e.config}"
KUTE_E2E_RESTRICTED_KUBECONFIG="${KUTE_E2E_RESTRICTED_KUBECONFIG:-${REPO_ROOT}/.kube/e2e-restricted.config}"

log() { printf '\033[1;34m==>\033[0m %s\n' "$*" >&2; }
die() { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }

[[ -f "$KIND_CONFIG" ]] || die "no kind config for K8S_VERSION=${K8S_VERSION} (${KIND_CONFIG})"

# The cluster name lives in the kind config rather than being passed on the
# command line, so the two stay in step when a version is added.
CLUSTER_NAME="$(awk '/^name:/ {print $2; exit}' "$KIND_CONFIG")"
[[ -n "$CLUSTER_NAME" ]] || die "${KIND_CONFIG} has no top-level name:"
CONTEXT="kind-${CLUSTER_NAME}"

# kubectl always against the e2e kubeconfig, never the ambient KUBECONFIG.
kc() { kubectl --kubeconfig "$KUTE_E2E_KUBECONFIG" --context "$CONTEXT" "$@"; }

cluster_exists() { kind get clusters 2>/dev/null | grep -qx "$CLUSTER_NAME"; }

write_kubeconfig() {
  mkdir -p "$(dirname "$KUTE_E2E_KUBECONFIG")"
  kind export kubeconfig --name "$CLUSTER_NAME" --kubeconfig "$KUTE_E2E_KUBECONFIG" >/dev/null
  log "kubeconfig → ${KUTE_E2E_KUBECONFIG} (context ${CONTEXT})"
}

apply_fixtures() {
  log "applying fixtures to ${NAMESPACE}"
  # The CRD has to be Established before its own custom resources can be
  # applied, so 51-widgets.yaml is deliberately not in the first sweep.
  for f in "${FIXTURE_DIR}"/*.yaml; do
    [[ "$(basename "$f")" == "51-widgets.yaml" ]] && continue
    kc apply -f "$f" >/dev/null
  done
  kc wait --for=condition=Established --timeout=90s crd/widgets.kute.dev >/dev/null
  kc apply -f "${FIXTURE_DIR}/51-widgets.yaml" >/dev/null

  log "waiting for the api rollout"
  kc -n "$NAMESPACE" rollout status deployment/api --timeout=180s >/dev/null

  # worker is *meant* to crash, so there is no rollout to wait on — wait for
  # the crash itself instead, which is what the status-derivation tests read.
  log "waiting for worker to reach CrashLoopBackOff"
  local deadline=$((SECONDS + 180))
  until kc -n "$NAMESPACE" get pods -l app=worker \
    -o jsonpath='{.items[*].status.containerStatuses[*].state.waiting.reason}' 2>/dev/null |
    grep -q CrashLoopBackOff; do
    [[ $SECONDS -lt $deadline ]] || die "worker never reached CrashLoopBackOff"
    sleep 3
  done
}

mint_restricted_kubeconfig() {
  log "minting a token kubeconfig for ${NAMESPACE}/${RESTRICTED_SA}"
  local server ca token
  server="$(kc config view --minify -o jsonpath='{.clusters[0].cluster.server}')"
  ca="$(kc config view --raw --minify -o jsonpath='{.clusters[0].cluster.certificate-authority-data}')"
  token="$(kc -n "$NAMESPACE" create token "$RESTRICTED_SA" --duration=24h)"

  mkdir -p "$(dirname "$KUTE_E2E_RESTRICTED_KUBECONFIG")"
  cat >"$KUTE_E2E_RESTRICTED_KUBECONFIG" <<EOF
apiVersion: v1
kind: Config
clusters:
  - name: ${CLUSTER_NAME}
    cluster:
      server: ${server}
      certificate-authority-data: ${ca}
contexts:
  - name: ${CONTEXT}
    context:
      cluster: ${CLUSTER_NAME}
      namespace: ${NAMESPACE}
      user: ${RESTRICTED_SA}
current-context: ${CONTEXT}
users:
  - name: ${RESTRICTED_SA}
    user:
      token: ${token}
EOF
  chmod 600 "$KUTE_E2E_RESTRICTED_KUBECONFIG"
  log "restricted kubeconfig → ${KUTE_E2E_RESTRICTED_KUBECONFIG}"
}

up() {
  if cluster_exists; then
    log "kind cluster ${CLUSTER_NAME} already exists — topping it up"
  else
    log "creating kind cluster ${CLUSTER_NAME} (k8s ${K8S_VERSION})"
    kind create cluster --config "$KIND_CONFIG" --wait 180s
  fi
  write_kubeconfig
  apply_fixtures
  mint_restricted_kubeconfig
  log "ready — go test -tags e2e ./test/e2e/... ./internal/kube/..."
}

down() {
  if cluster_exists; then
    log "deleting kind cluster ${CLUSTER_NAME}"
    kind delete cluster --name "$CLUSTER_NAME"
  else
    log "no kind cluster ${CLUSTER_NAME} to delete"
  fi
  rm -f "$KUTE_E2E_KUBECONFIG" "$KUTE_E2E_RESTRICTED_KUBECONFIG"
}

case "${1:-up}" in
  up) up ;;
  down) down ;;
  recreate) down; up ;;
  fixtures) apply_fixtures ;;
  kubeconfig) write_kubeconfig ;;
  restricted-kubeconfig) mint_restricted_kubeconfig ;;
  *) die "unknown command: $1 (up|down|recreate|fixtures|kubeconfig|restricted-kubeconfig)" ;;
esac
