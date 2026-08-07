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
#                                        (re)mint both restricted token kubeconfigs
#
# Env:
#   K8S_VERSION                     1.35 (default) or 1.36 — picks kind/config-<v>.yaml
#   KUTE_E2E_KUBECONFIG             default <repo>/.kube/e2e.config
#   KUTE_E2E_RESTRICTED_KUBECONFIG  default <repo>/.kube/e2e-restricted.config
#   KUTE_E2E_PARTIAL_KUBECONFIG  default <repo>/.kube/e2e-partial.config
#
# Both default *outside* .kube/config, which mise.toml points KUBECONFIG at and
# which holds the developer's own contexts: an e2e run must never rewrite the
# file the developer's own `go run ./cmd/kute` reads.

set -euo pipefail

K8S_VERSION="${K8S_VERSION:-1.35}"
NAMESPACE=kute-e2e
RESTRICTED_SA=kute-restricted
PARTIAL_SA=kute-partial

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
KIND_CONFIG="${REPO_ROOT}/kind/config-${K8S_VERSION}.yaml"
FIXTURE_DIR="${REPO_ROOT}/test/e2e/fixtures"

KUTE_E2E_KUBECONFIG="${KUTE_E2E_KUBECONFIG:-${REPO_ROOT}/.kube/e2e.config}"
KUTE_E2E_RESTRICTED_KUBECONFIG="${KUTE_E2E_RESTRICTED_KUBECONFIG:-${REPO_ROOT}/.kube/e2e-restricted.config}"
KUTE_E2E_PARTIAL_KUBECONFIG="${KUTE_E2E_PARTIAL_KUBECONFIG:-${REPO_ROOT}/.kube/e2e-partial.config}"

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
  # A CRD has to be Established before its own custom resources can be
  # applied, so the instance fixtures are deliberately not in the first
  # sweep.
  for f in "${FIXTURE_DIR}"/*.yaml; do
    case "$(basename "$f")" in
      51-widgets.yaml|53-flux-objects.yaml|55-argocd-objects.yaml) continue ;;
    esac
    kc apply -f "$f" >/dev/null
  done
  kc wait --for=condition=Established --timeout=90s crd/widgets.kute.dev >/dev/null
  kc apply -f "${FIXTURE_DIR}/51-widgets.yaml" >/dev/null

  # Flux's CRDs are installed without any Flux controller — nothing
  # reconciles the objects, which is what keeps their hand-written status
  # (suspended, failing, reconciling) permanent instead of transient.
  for crd in kustomizations.kustomize.toolkit.fluxcd.io \
             helmreleases.helm.toolkit.fluxcd.io \
             gitrepositories.source.toolkit.fluxcd.io; do
    kc wait --for=condition=Established --timeout=90s "crd/${crd}" >/dev/null
  done
  kc apply -f "${FIXTURE_DIR}/53-flux-objects.yaml" >/dev/null

  # Argo CD's CRDs, same reasoning: no application-controller runs against
  # this cluster, so the hand-written sync/health status in
  # 55-argocd-objects.yaml stays put.
  for crd in applications.argoproj.io appprojects.argoproj.io; do
    kc wait --for=condition=Established --timeout=90s "crd/${crd}" >/dev/null
  done
  kc apply -f "${FIXTURE_DIR}/55-argocd-objects.yaml" >/dev/null

  log "waiting for the api rollout"
  kc -n "$NAMESPACE" rollout status deployment/api --timeout=180s >/dev/null

  # worker is *meant* to crash, so there is no rollout to wait on — wait for
  # the crash itself instead, which is what the status-derivation tests read.
  #
  # Gate on the restart count, not on state.waiting.reason=CrashLoopBackOff.
  # The waiting reason is transient in the same way test/e2e/harness.go warns
  # about: a busy runner can leave the container reported as Terminated for the
  # whole backoff, so a poll for the word misses it on every tick while the pod
  # is demonstrably restarting. The count only ever goes up.
  log "waiting for worker to start restarting"
  local deadline=$((SECONDS + 180)) restarts
  until restarts="$(kc -n "$NAMESPACE" get pods -l app=worker \
    -o jsonpath='{.items[0].status.containerStatuses[0].restartCount}' 2>/dev/null)"
    [[ "${restarts:-0}" -ge 2 ]]; do
    [[ $SECONDS -lt $deadline ]] || die "worker never restarted (count=${restarts:-none})"
    sleep 3
  done
}

# mint_sa_kubeconfig <serviceaccount> <destination>
mint_sa_kubeconfig() {
  local sa="$1" dest="$2"
  log "minting a token kubeconfig for ${NAMESPACE}/${sa}"
  local server ca token
  server="$(kc config view --minify -o jsonpath='{.clusters[0].cluster.server}')"
  ca="$(kc config view --raw --minify -o jsonpath='{.clusters[0].cluster.certificate-authority-data}')"
  token="$(kc -n "$NAMESPACE" create token "$sa" --duration=24h)"

  mkdir -p "$(dirname "$dest")"
  cat >"$dest" <<EOF
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
      user: ${sa}
current-context: ${CONTEXT}
users:
  - name: ${sa}
    user:
      token: ${token}
EOF
  chmod 600 "$dest"
  log "${sa} kubeconfig → ${dest}"
}

mint_restricted_kubeconfigs() {
  mint_sa_kubeconfig "$RESTRICTED_SA" "$KUTE_E2E_RESTRICTED_KUBECONFIG"
  mint_sa_kubeconfig "$PARTIAL_SA" "$KUTE_E2E_PARTIAL_KUBECONFIG"
}

# preflight_inotify warns before a create that is likely to fail for a reason
# the resulting error never names.
#
# Each kind node consumes several inotify instances, and the kernel default of
# 128 per user is not enough for two multi-node clusters at once. The second
# create dies inside `kubeadm init` with "cannot obtain client without
# bootstrap … client rate limiter Wait returned an error: context deadline
# exceeded", which reads like a slow machine and is nothing of the sort. This
# is exactly what the verification steps in docs/e2e-plan.md walk into: they
# ask you to bring up 1.36 while 1.35 is still running.
preflight_inotify() {
  local others limit
  others="$(kind get clusters 2>/dev/null | grep -vx "$CLUSTER_NAME" | grep -c . || true)"
  [[ "$others" -gt 0 ]] || return 0
  limit="$(sysctl -n fs.inotify.max_user_instances 2>/dev/null || echo 0)"
  [[ "$limit" -gt 0 && "$limit" -lt 512 ]] || return 0

  log "warning: ${others} other kind cluster(s) are running and fs.inotify.max_user_instances is ${limit}."
  log "         kubeadm is likely to time out bootstrapping the control plane. Either:"
  log "           sudo sysctl -w fs.inotify.max_user_instances=512   # kind's own recommendation"
  log "         or delete the other cluster(s) first."
}

up() {
  if cluster_exists; then
    log "kind cluster ${CLUSTER_NAME} already exists — topping it up"
  else
    preflight_inotify
    log "creating kind cluster ${CLUSTER_NAME} (k8s ${K8S_VERSION})"
    kind create cluster --config "$KIND_CONFIG" --wait 180s
  fi
  write_kubeconfig
  apply_fixtures
  mint_restricted_kubeconfigs
  log "ready — go test -tags e2e ./test/e2e/... ./internal/kube/..."
}

down() {
  if cluster_exists; then
    log "deleting kind cluster ${CLUSTER_NAME}"
    kind delete cluster --name "$CLUSTER_NAME"
  else
    log "no kind cluster ${CLUSTER_NAME} to delete"
  fi
  rm -f "$KUTE_E2E_KUBECONFIG" "$KUTE_E2E_RESTRICTED_KUBECONFIG" "$KUTE_E2E_PARTIAL_KUBECONFIG"
}

case "${1:-up}" in
  up) up ;;
  down) down ;;
  recreate) down; up ;;
  fixtures) apply_fixtures ;;
  kubeconfig) write_kubeconfig ;;
  restricted-kubeconfig) mint_restricted_kubeconfigs ;;
  *) die "unknown command: $1 (up|down|recreate|fixtures|kubeconfig|restricted-kubeconfig)" ;;
esac
