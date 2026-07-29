#!/usr/bin/env bash
# e2e-scale-cluster.sh — a 5,000-pod cluster on kwok, for the scale row of the
# end-to-end suite.
#
# kwok rather than kind, and this is the one place that choice is forced: kwok
# schedules pods onto fake nodes and marks them Running without pulling an
# image or starting a container, so 5k pods cost roughly 5k API objects and
# nothing else. kind would need 5k real containers.
#
# What it makes measurable is what only shows up at size: connect time with
# three eager informers filling against a large cluster, and the heap those
# caches occupy. Nothing here has a kubelet, so logs/exec/port-forward stay
# with scripts/e2e-cluster.sh.
#
# Usage:
#   scripts/e2e-scale-cluster.sh up        create (or top up) the cluster
#   scripts/e2e-scale-cluster.sh down      tear it down
#   scripts/e2e-scale-cluster.sh recreate  down, then up
#
# Env:
#   KUTE_E2E_SCALE_KUBECONFIG  default <repo>/.kube/e2e-scale.config
#   SCALE_PODS                 total pods to create (default 5000)
#   SCALE_NODES                fake nodes to spread them over (default 50)
#   SCALE_NAMESPACES           namespaces to spread them over (default 10)
#   KWOK_VERSION / KWOK_RUNTIME  as in kwok-prod-cluster.sh

set -euo pipefail

CLUSTER_NAME="${CLUSTER_NAME:-kute-scale}"
KWOK_VERSION="${KWOK_VERSION:-v0.8.0}"
KWOK_RUNTIME="${KWOK_RUNTIME:-binary}"
CTX="kwok-${CLUSTER_NAME}"

SCALE_PODS="${SCALE_PODS:-5000}"
SCALE_NODES="${SCALE_NODES:-50}"
SCALE_NAMESPACES="${SCALE_NAMESPACES:-10}"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN_DIR="${REPO_ROOT}/scripts/.bin"
KUTE_E2E_SCALE_KUBECONFIG="${KUTE_E2E_SCALE_KUBECONFIG:-${REPO_ROOT}/.kube/e2e-scale.config}"

log() { printf '\033[1;34m==>\033[0m %s\n' "$*" >&2; }
die() { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }

# --- kwokctl bootstrap --------------------------------------------------------
# Deliberately the same bootstrap as kwok-prod-cluster.sh rather than a second
# copy of the logic: the mise "kwokctl" package actually ships the `kwok`
# controller binary, so a kwokctl on PATH can't be trusted without checking.

is_real_kwokctl() { "$1" --help 2>/dev/null | head -n1 | grep -q '^kwokctl'; }

resolve_kwokctl() {
  local cand
  if [[ -x "${BIN_DIR}/kwokctl" ]]; then
    echo "${BIN_DIR}/kwokctl"
    return
  fi
  if cand="$(command -v kwokctl)" && is_real_kwokctl "$cand"; then
    echo "$cand"
    return
  fi
  local arch
  case "$(uname -m)" in
    x86_64) arch=amd64 ;;
    aarch64 | arm64) arch=arm64 ;;
    *) die "unsupported arch: $(uname -m)" ;;
  esac
  log "kwokctl on PATH is not the real CLI; downloading kwokctl ${KWOK_VERSION} to scripts/.bin/"
  mkdir -p "$BIN_DIR"
  curl -fsSL --retry 3 -o "${BIN_DIR}/kwokctl.tmp" \
    "https://github.com/kubernetes-sigs/kwok/releases/download/${KWOK_VERSION}/kwokctl-linux-${arch}"
  chmod +x "${BIN_DIR}/kwokctl.tmp"
  mv "${BIN_DIR}/kwokctl.tmp" "${BIN_DIR}/kwokctl"
  echo "${BIN_DIR}/kwokctl"
}

KWOKCTL="$(resolve_kwokctl)"

kc() { kubectl --kubeconfig "$KUTE_E2E_SCALE_KUBECONFIG" --context "$CTX" "$@"; }

cluster_exists() { "$KWOKCTL" get clusters 2>/dev/null | grep -qx "$CLUSTER_NAME"; }

write_kubeconfig() {
  mkdir -p "$(dirname "$KUTE_E2E_SCALE_KUBECONFIG")"
  "$KWOKCTL" get kubeconfig --name "$CLUSTER_NAME" > "$KUTE_E2E_SCALE_KUBECONFIG"
  log "kubeconfig → ${KUTE_E2E_SCALE_KUBECONFIG} (context ${CTX})"
}

emit_nodes() {
  local i zones=(a b c) zone
  for ((i = 0; i < SCALE_NODES; i++)); do
    zone="${zones[i % 3]}"
    cat <<EOF
---
apiVersion: v1
kind: Node
metadata:
  name: scale-node-$(printf '%03d' "$i")
  annotations:
    kwok.x-k8s.io/node: fake
    node.alpha.kubernetes.io/ttl: "0"
  labels:
    kubernetes.io/arch: amd64
    kubernetes.io/os: linux
    kubernetes.io/hostname: scale-node-$(printf '%03d' "$i")
    node-role.kubernetes.io/worker: ""
    node.kubernetes.io/instance-type: m6i.4xlarge
    topology.kubernetes.io/region: eu-central-1
    topology.kubernetes.io/zone: eu-central-1${zone}
spec: {}
status:
  capacity:
    cpu: "16"
    memory: 64Gi
    pods: "200"
  allocatable:
    cpu: "15500m"
    memory: 62Gi
    pods: "200"
  phase: Running
EOF
  done
}

# Deployments with high replica counts, rather than 5,000 individual Pod
# objects: it is one apply per workload, and it exercises the same read paths
# the app takes (a pod list plus its owning ReplicaSet) instead of a flat pile
# of orphan pods no real cluster would have.
emit_workloads() {
  local per_ns=$((SCALE_PODS / SCALE_NAMESPACES))
  local ns i
  for ((i = 0; i < SCALE_NAMESPACES; i++)); do
    ns="scale-$(printf '%02d' "$i")"
    cat <<EOF
---
apiVersion: v1
kind: Namespace
metadata:
  name: ${ns}
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
  namespace: ${ns}
spec:
  replicas: ${per_ns}
  selector:
    matchLabels:
      app: web
  template:
    metadata:
      labels:
        app: web
      annotations:
        kwok.x-k8s.io/usage-cpu: 120m
        kwok.x-k8s.io/usage-memory: 256Mi
    spec:
      affinity:
        nodeAffinity:
          requiredDuringSchedulingIgnoredDuringExecution:
            nodeSelectorTerms:
              - matchExpressions:
                  - key: node-role.kubernetes.io/worker
                    operator: Exists
      tolerations:
        - key: kwok.x-k8s.io/node
          operator: Exists
          effect: NoSchedule
      containers:
        - name: web
          image: registry.k8s.io/pause:3.10
          resources:
            requests:
              cpu: 100m
              memory: 192Mi
EOF
  done
}

up() {
  if cluster_exists; then
    log "kwok cluster ${CLUSTER_NAME} already exists — topping it up"
  else
    log "creating kwok cluster ${CLUSTER_NAME}"
    "$KWOKCTL" create cluster --name "$CLUSTER_NAME" --runtime "$KWOK_RUNTIME" --wait 5m
  fi
  write_kubeconfig

  log "creating ${SCALE_NODES} fake nodes"
  emit_nodes | kc apply -f - >/dev/null

  log "creating ${SCALE_PODS} pods across ${SCALE_NAMESPACES} namespaces"
  emit_workloads | kc apply -f - >/dev/null

  log "waiting for pods to be scheduled and marked Running"
  local deadline=$((SECONDS + 900)) running
  while :; do
    running="$(kc get pods -A --field-selector=status.phase=Running -o name 2>/dev/null | wc -l)"
    log "  ${running}/${SCALE_PODS} running"
    [[ "$running" -ge "$SCALE_PODS" ]] && break
    [[ $SECONDS -lt $deadline ]] || die "only ${running}/${SCALE_PODS} pods reached Running"
    sleep 10
  done
  log "ready — go test -tags 'e2e e2e_scale' ./test/e2e/..."
}

down() {
  if cluster_exists; then
    log "deleting kwok cluster ${CLUSTER_NAME}"
    "$KWOKCTL" delete cluster --name "$CLUSTER_NAME"
  else
    log "no kwok cluster ${CLUSTER_NAME} to delete"
  fi
  rm -f "$KUTE_E2E_SCALE_KUBECONFIG"
}

case "${1:-up}" in
  up) up ;;
  down) down ;;
  recreate) down; up ;;
  kubeconfig) write_kubeconfig ;;
  *) die "unknown command: $1 (up|down|recreate|kubeconfig)" ;;
esac
