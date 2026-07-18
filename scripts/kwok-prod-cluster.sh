#!/usr/bin/env bash
# kwok-prod-cluster.sh — spin up a production-looking fake Kubernetes cluster with kwok.
#
# What you get:
#   * 3 control-plane + 6 worker nodes (mixed instance types, 3 zones, one cordoned)
#   * monitoring (prometheus, alertmanager, grafana, node-exporter, kube-state-metrics)
#   * cert-manager (controller/webhook/cainjector + CRDs + Certificates/ClusterIssuers)
#   * traefik gateway (deployment + CRDs + IngressRoutes/Middlewares)
#   * logging (fluent-bit, loki), a kafka/zookeeper data platform
#   * app namespaces (shop-frontend/checkout/inventory) with HPAs, PDBs, quotas,
#     secrets, configmaps, cronjobs, pre-bound PVs, and a deliberately Pending
#     GPU deployment for status variety
#
# Everything is fake: kwok schedules pods onto fake nodes and marks them Running;
# no images are ever pulled. Only the control plane (apiserver/etcd/scheduler/
# controller-manager) runs for real, as containers.
#
# Usage:
#   scripts/kwok-prod-cluster.sh             create, or top up an existing cluster
#   scripts/kwok-prod-cluster.sh --recreate  delete first, then create from scratch
#   scripts/kwok-prod-cluster.sh --delete    tear down
#
# Env overrides: CLUSTER_NAME (default prod-sim), KWOK_VERSION (default v0.8.0),
# KWOK_RUNTIME (default binary — this box's proxy blocks registry.k8s.io image
# pulls, but the binary runtime's sources, dl.k8s.io and github.com, are fine)
#
# The kubeconfig context "kwok-<name>" is merged into $KUBECONFIG (mise.toml
# points that at <repo>/.kube/config, the same file the app reads), so `go run
# ./cmd/kute` picks the cluster up directly.

set -euo pipefail

CLUSTER_NAME="${CLUSTER_NAME:-prod-sim}"
KWOK_VERSION="${KWOK_VERSION:-v0.8.0}"
KWOK_RUNTIME="${KWOK_RUNTIME:-binary}"
CTX="kwok-${CLUSTER_NAME}"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN_DIR="${REPO_ROOT}/scripts/.bin"

log() { printf '\033[1;34m==>\033[0m %s\n' "$*" >&2; }

# --- kwokctl bootstrap --------------------------------------------------------
# The mise "kwokctl" package actually ships the `kwok` controller binary, so a
# kwokctl on PATH can't be trusted; verify it and fall back to downloading the
# real CLI from the kwok release page into scripts/.bin/.

is_real_kwokctl() { "$1" --help 2>/dev/null | head -n1 | grep -q '^kwokctl'; }

resolve_kwokctl() {
  local cand
  # Our own download is placed atomically (curl to .tmp, then mv), so its
  # presence alone means it's complete — don't re-execute it to verify, since
  # that check is unreliable under sandboxed shells and triggers pointless
  # re-downloads.
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
    *) echo "unsupported arch: $(uname -m)" >&2; exit 1 ;;
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

k() { kubectl --context "$CTX" "$@"; }

cluster_exists() { "$KWOKCTL" get clusters 2>/dev/null | grep -qx "$CLUSTER_NAME"; }

case "${1:-}" in
  --delete)
    log "Deleting cluster ${CLUSTER_NAME}"
    "$KWOKCTL" delete cluster --name "$CLUSTER_NAME"
    exit 0
    ;;
  --recreate)
    if cluster_exists; then
      log "Deleting existing cluster ${CLUSTER_NAME}"
      "$KWOKCTL" delete cluster --name "$CLUSTER_NAME"
    fi
    ;;
  "") ;;
  *)
    echo "unknown argument: $1 (expected --recreate or --delete)" >&2
    exit 1
    ;;
esac

# --- cluster ------------------------------------------------------------------

if cluster_exists; then
  log "Cluster ${CLUSTER_NAME} already exists — re-applying manifests (use --recreate for a clean slate)"
else
  log "Creating kwok cluster ${CLUSTER_NAME}"
  # metrics-server (real, scraping kwok's simulated kubelets) feeds the
  # metrics.k8s.io API; the ClusterResourceUsage CRD lets us drive per-pod
  # usage from the kwok.x-k8s.io/usage-* annotations on the workloads below.
  "$KWOKCTL" create cluster --name "$CLUSTER_NAME" --runtime "$KWOK_RUNTIME" \
    --enable metrics-server \
    --enable-crds Metric \
    --enable-crds ClusterResourceUsage \
    --wait 5m
fi

# kwokctl only merges its kubeconfig entry into $KUBECONFIG at `create` time,
# so if $KUBECONFIG points somewhere new since then (a fresh checkout, a
# changed mise.toml) the "already exists" branch above would silently leave
# the context missing. Always (re-)merge so this script is idempotent no
# matter which kubeconfig file is currently targeted.
KUBECONFIG_TARGET="${KUBECONFIG:-$HOME/.kube/config}"
if ! KUBECONFIG="$KUBECONFIG_TARGET" kubectl config get-contexts "$CTX" >/dev/null 2>&1; then
  log "Merging ${CTX} kubeconfig entry into ${KUBECONFIG_TARGET}"
  mkdir -p "$(dirname "$KUBECONFIG_TARGET")"
  touch "$KUBECONFIG_TARGET"
  kwok_kubeconfig="$(mktemp)"
  "$KWOKCTL" get kubeconfig --name "$CLUSTER_NAME" > "$kwok_kubeconfig"
  KUBECONFIG="${kwok_kubeconfig}:${KUBECONFIG_TARGET}" kubectl config view --flatten > "${KUBECONFIG_TARGET}.tmp"
  mv "${KUBECONFIG_TARGET}.tmp" "$KUBECONFIG_TARGET"
  rm -f "$kwok_kubeconfig"
fi

# --- simulated resource usage ---------------------------------------------------
# Every pod's usage comes from its kwok.x-k8s.io/usage-cpu/-memory annotations
# (set on all workload templates below), surfaced through metrics-server as
# metrics.k8s.io — `kubectl top` and the app's CPU/MEM bars work.

if k get crd clusterresourceusages.kwok.x-k8s.io >/dev/null 2>&1; then
  log "Configuring simulated pod usage from annotations"
  # kwok's stock metrics bundle (vendored from the v0.8.0 release asset
  # metrics-usage.yaml): the Metric object makes kwok serve kubelet-style
  # /metrics/nodes/{node}/metrics/resource endpoints (without it metrics-server
  # scrapes 404), and the ClusterResourceUsage maps each pod's
  # kwok.x-k8s.io/usage-* annotations to its reported usage. The nodes below
  # carry the metrics.k8s.io/resource-metrics-path annotation that points
  # metrics-server at the per-node path.
  k apply -f "${REPO_ROOT}/scripts/kwok-metrics-usage.yaml"
else
  log "ClusterResourceUsage CRD not present (cluster predates metrics support) — skipping usage config; --recreate to enable"
fi

# --- nodes --------------------------------------------------------------------
# kwok manages any node carrying the kwok.x-k8s.io/node=fake annotation and
# keeps it Ready; capacity comes from the applied status.

emit_node() { # name role zone instance cpu mem alloc_cpu alloc_mem
  local name=$1 role=$2 zone=$3 instance=$4 cpu=$5 mem=$6 acpu=$7 amem=$8
  local role_label spec_block="  {}"
  if [[ $role == control-plane ]]; then
    role_label="node-role.kubernetes.io/control-plane"
    spec_block=$'  taints:\n    - key: node-role.kubernetes.io/control-plane\n      effect: NoSchedule'
  else
    role_label="node-role.kubernetes.io/worker"
  fi
  cat <<EOF
---
apiVersion: v1
kind: Node
metadata:
  name: ${name}
  annotations:
    kwok.x-k8s.io/node: fake
    node.alpha.kubernetes.io/ttl: "0"
    metrics.k8s.io/resource-metrics-path: /metrics/nodes/${name}/metrics/resource
  labels:
    kubernetes.io/arch: amd64
    kubernetes.io/os: linux
    kubernetes.io/hostname: ${name}
    ${role_label}: ""
    node.kubernetes.io/instance-type: ${instance}
    topology.kubernetes.io/region: eu-central-1
    topology.kubernetes.io/zone: eu-central-1${zone}
spec:
${spec_block}
status:
  capacity:
    cpu: "${cpu}"
    memory: ${mem}
    ephemeral-storage: 200Gi
    pods: "110"
  allocatable:
    cpu: "${acpu}"
    memory: ${amem}
    ephemeral-storage: 180Gi
    pods: "110"
  nodeInfo:
    architecture: amd64
    operatingSystem: linux
    osImage: Bottlerocket OS 1.26.1 (aws-k8s-1.31)
    kernelVersion: 6.1.112
    containerRuntimeVersion: containerd://1.7.22
    kubeletVersion: v1.31.4-eks-2d5f260
    kubeProxyVersion: v1.31.4-eks-2d5f260
EOF
}

log "Creating nodes"
{
  emit_node cp-1 control-plane a m6i.xlarge 4 16Gi 3800m 14Gi
  emit_node cp-2 control-plane b m6i.xlarge 4 16Gi 3800m 14Gi
  emit_node cp-3 control-plane c m6i.xlarge 4 16Gi 3800m 14Gi
  emit_node worker-1 worker a c6i.4xlarge 16 32Gi 15600m 29Gi
  emit_node worker-2 worker b m6i.2xlarge 8 32Gi 7800m 29Gi
  emit_node worker-3 worker c m6i.2xlarge 8 32Gi 7800m 29Gi
  emit_node worker-4 worker a r6i.2xlarge 8 64Gi 7800m 59Gi
  emit_node worker-5 worker b c6i.4xlarge 16 32Gi 15600m 29Gi
  emit_node worker-6 worker c m6i.2xlarge 8 32Gi 7800m 29Gi
} | k apply -f -

k cordon worker-6 >/dev/null
log "Cordoned worker-6 (maintenance realism)"

# --- CRDs ---------------------------------------------------------------------
# Minimal structural stand-ins for the real cert-manager / prometheus-operator /
# traefik CRDs: enough for discovery, printer columns, and Ready-style status
# conditions, with open schemas so the sample CRs apply untouched.

emit_crd() { # group plural kind [shortnames-json] [scope] [ready-jsonpath]
  local group=$1 plural=$2 kind=$3 short=${4:-[]} scope=${5:-Namespaced}
  local ready_path=${6:-'.status.conditions[?(@.type=="Ready")].status'}
  # *.k8s.io is a protected API group: the apiserver refuses to establish the
  # CRD without this annotation (see kubernetes/enhancements#1111).
  local annotations=""
  [[ $group == *.k8s.io ]] && annotations=$'  annotations:\n    api-approved.kubernetes.io: "https://github.com/kubernetes/enhancements/pull/1111"'
  cat <<EOF
---
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: ${plural}.${group}
${annotations}
spec:
  group: ${group}
  scope: ${scope}
  names:
    kind: ${kind}
    listKind: ${kind}List
    plural: ${plural}
    singular: $(tr '[:upper:]' '[:lower:]' <<<"$kind")
    shortNames: ${short}
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          x-kubernetes-preserve-unknown-fields: true
      additionalPrinterColumns:
        - name: Ready
          type: string
          jsonPath: ${ready_path}
        - name: Age
          type: date
          jsonPath: .metadata.creationTimestamp
EOF
}

log "Installing CRDs (cert-manager, prometheus-operator, traefik, gateway-api)"
{
  emit_crd cert-manager.io certificates Certificate '[cert, certs]'
  emit_crd cert-manager.io clusterissuers ClusterIssuer '[]' Cluster
  emit_crd monitoring.coreos.com servicemonitors ServiceMonitor '[smon]'
  emit_crd monitoring.coreos.com prometheusrules PrometheusRule '[promrule]'
  emit_crd traefik.io ingressroutes IngressRoute '[]'
  emit_crd traefik.io middlewares Middleware '[]'
  emit_crd gateway.networking.k8s.io gatewayclasses GatewayClass '[gc]' Cluster
  emit_crd gateway.networking.k8s.io gateways Gateway '[gtw]'
  emit_crd gateway.networking.k8s.io httproutes HTTPRoute '[]' Namespaced \
    '.status.parents[0].conditions[?(@.type=="Ready")].status'
} | k apply -f -
k wait --for=condition=Established crd --all --timeout=60s >/dev/null

# --- namespaces, quotas, storage ------------------------------------------------

log "Creating namespaces, quotas, storage classes, PVs"
k apply -f - <<'EOF'
---
apiVersion: v1
kind: Namespace
metadata:
  name: monitoring
  labels: {team: platform, environment: production}
---
apiVersion: v1
kind: Namespace
metadata:
  name: cert-manager
  labels: {team: platform, environment: production}
---
apiVersion: v1
kind: Namespace
metadata:
  name: traefik
  labels: {team: platform, environment: production}
---
apiVersion: v1
kind: Namespace
metadata:
  name: logging
  labels: {team: platform, environment: production}
---
apiVersion: v1
kind: Namespace
metadata:
  name: data-platform
  labels: {team: data, environment: production}
---
apiVersion: v1
kind: Namespace
metadata:
  name: shop-frontend
  labels: {team: storefront, environment: production}
---
apiVersion: v1
kind: Namespace
metadata:
  name: shop-checkout
  labels: {team: payments, environment: production}
---
apiVersion: v1
kind: Namespace
metadata:
  name: shop-inventory
  labels: {team: supply-chain, environment: production}
---
apiVersion: v1
kind: Namespace
metadata:
  name: ml-serving
  labels: {team: ml, environment: production}
---
apiVersion: v1
kind: Namespace
metadata:
  name: batch-jobs
  labels: {team: data, environment: production}
---
apiVersion: scheduling.k8s.io/v1
kind: PriorityClass
metadata:
  name: business-critical
value: 100000
description: Revenue-path workloads.
---
apiVersion: v1
kind: ResourceQuota
metadata:
  name: compute-quota
  namespace: shop-checkout
spec:
  hard:
    requests.cpu: "24"
    requests.memory: 48Gi
    pods: "60"
---
apiVersion: v1
kind: ResourceQuota
metadata:
  name: compute-quota
  namespace: shop-frontend
spec:
  hard:
    requests.cpu: "16"
    requests.memory: 32Gi
    pods: "40"
---
apiVersion: v1
kind: LimitRange
metadata:
  name: defaults
  namespace: shop-frontend
spec:
  limits:
    - type: Container
      defaultRequest: {cpu: 100m, memory: 128Mi}
      default: {cpu: 500m, memory: 512Mi}
---
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: gp3
  annotations:
    storageclass.kubernetes.io/is-default-class: "true"
provisioner: ebs.csi.aws.com
reclaimPolicy: Delete
volumeBindingMode: WaitForFirstConsumer
allowVolumeExpansion: true
EOF

# Pre-bound PVs so statefulset volumeClaimTemplates come up Bound without a
# real provisioner. PVC names follow <template>-<sts>-<ordinal> with template
# "data" everywhere.
emit_pv() { # ns sts ordinal size
  local ns=$1 sts=$2 i=$3 size=$4
  cat <<EOF
---
apiVersion: v1
kind: PersistentVolume
metadata:
  name: pv-${ns}-${sts}-${i}
spec:
  capacity: {storage: ${size}}
  accessModes: [ReadWriteOnce]
  storageClassName: gp3
  persistentVolumeReclaimPolicy: Retain
  claimRef:
    namespace: ${ns}
    name: data-${sts}-${i}
  csi:
    driver: ebs.csi.aws.com
    volumeHandle: vol-${ns}-${sts}-${i}
EOF
}

{
  for spec in \
    "monitoring prometheus 2 50Gi" \
    "monitoring alertmanager 3 10Gi" \
    "logging loki 2 100Gi" \
    "data-platform kafka 3 200Gi" \
    "data-platform zookeeper 3 20Gi" \
    "shop-inventory postgres 1 100Gi" \
    "shop-checkout redis 3 10Gi"; do
    read -r ns sts replicas size <<<"$spec"
    for ((i = 0; i < replicas; i++)); do emit_pv "$ns" "$sts" "$i" "$size"; done
  done
} | k apply -f -

# --- workload helpers -----------------------------------------------------------
# Usage annotations (kwok.x-k8s.io/usage-*) are inert today but let a kwok
# ResourceUsage config feed simulated pod metrics later without touching the
# manifests.

emit_deploy() { # ns name image replicas cpu_req mem_req port component partof [extra_podspec_yaml]
  local ns=$1 name=$2 image=$3 replicas=$4 cpu=$5 mem=$6 port=$7 component=$8 partof=$9 extra=${10:-}
  cat <<EOF
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ${name}
  namespace: ${ns}
  labels:
    app.kubernetes.io/name: ${name}
    app.kubernetes.io/component: ${component}
    app.kubernetes.io/part-of: ${partof}
spec:
  replicas: ${replicas}
  selector:
    matchLabels:
      app.kubernetes.io/name: ${name}
  template:
    metadata:
      labels:
        app.kubernetes.io/name: ${name}
        app.kubernetes.io/component: ${component}
        app.kubernetes.io/part-of: ${partof}
      annotations:
        kwok.x-k8s.io/usage-cpu: "${cpu}"
        kwok.x-k8s.io/usage-memory: "${mem}"
    spec:
${extra}
      containers:
        - name: ${name}
          image: ${image}
          ports:
            - containerPort: ${port}
              name: http
          resources:
            requests: {cpu: ${cpu}, memory: ${mem}}
            limits: {memory: ${mem}}
EOF
}

emit_sts() { # ns name image replicas cpu_req mem_req port size component partof
  local ns=$1 name=$2 image=$3 replicas=$4 cpu=$5 mem=$6 port=$7 size=$8 component=$9 partof=${10}
  cat <<EOF
---
apiVersion: v1
kind: Service
metadata:
  name: ${name}
  namespace: ${ns}
  labels:
    app.kubernetes.io/name: ${name}
spec:
  clusterIP: None
  selector:
    app.kubernetes.io/name: ${name}
  ports:
    - port: ${port}
      name: tcp
---
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: ${name}
  namespace: ${ns}
  labels:
    app.kubernetes.io/name: ${name}
    app.kubernetes.io/component: ${component}
    app.kubernetes.io/part-of: ${partof}
spec:
  serviceName: ${name}
  replicas: ${replicas}
  selector:
    matchLabels:
      app.kubernetes.io/name: ${name}
  template:
    metadata:
      labels:
        app.kubernetes.io/name: ${name}
        app.kubernetes.io/component: ${component}
        app.kubernetes.io/part-of: ${partof}
      annotations:
        kwok.x-k8s.io/usage-cpu: "${cpu}"
        kwok.x-k8s.io/usage-memory: "${mem}"
    spec:
      containers:
        - name: ${name}
          image: ${image}
          ports:
            - containerPort: ${port}
          resources:
            requests: {cpu: ${cpu}, memory: ${mem}}
            limits: {memory: ${mem}}
          volumeMounts:
            - name: data
              mountPath: /data
  volumeClaimTemplates:
    - metadata:
        name: data
      spec:
        accessModes: [ReadWriteOnce]
        storageClassName: gp3
        resources:
          requests: {storage: ${size}}
EOF
}

emit_ds() { # ns name image cpu_req mem_req component partof
  local ns=$1 name=$2 image=$3 cpu=$4 mem=$5 component=$6 partof=$7
  cat <<EOF
---
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: ${name}
  namespace: ${ns}
  labels:
    app.kubernetes.io/name: ${name}
    app.kubernetes.io/component: ${component}
    app.kubernetes.io/part-of: ${partof}
spec:
  selector:
    matchLabels:
      app.kubernetes.io/name: ${name}
  template:
    metadata:
      labels:
        app.kubernetes.io/name: ${name}
        app.kubernetes.io/component: ${component}
        app.kubernetes.io/part-of: ${partof}
      annotations:
        kwok.x-k8s.io/usage-cpu: "${cpu}"
        kwok.x-k8s.io/usage-memory: "${mem}"
    spec:
      tolerations:
        - operator: Exists
      containers:
        - name: ${name}
          image: ${image}
          resources:
            requests: {cpu: ${cpu}, memory: ${mem}}
            limits: {memory: ${mem}}
EOF
}

emit_svc() { # ns name port [type]
  local ns=$1 name=$2 port=$3 type=${4:-ClusterIP}
  cat <<EOF
---
apiVersion: v1
kind: Service
metadata:
  name: ${name}
  namespace: ${ns}
  labels:
    app.kubernetes.io/name: ${name}
spec:
  type: ${type}
  selector:
    app.kubernetes.io/name: ${name}
  ports:
    - port: ${port}
      targetPort: ${port}
      name: http
EOF
}

# --- kube-system look-alikes -----------------------------------------------------

log "Populating kube-system"
{
  emit_deploy kube-system coredns registry.k8s.io/coredns/coredns:v1.11.3 2 100m 70Mi 53 dns kubernetes
  emit_deploy kube-system metrics-server registry.k8s.io/metrics-server/metrics-server:v0.7.2 1 100m 200Mi 10250 metrics kubernetes
  emit_ds kube-system kube-proxy registry.k8s.io/kube-proxy:v1.31.4 100m 128Mi proxy kubernetes
  emit_ds kube-system aws-node public.ecr.aws/amazon-k8s-cni:v1.19.0 25m 64Mi cni kubernetes
  emit_svc kube-system kube-dns 53
} | k apply -f -

# --- monitoring ------------------------------------------------------------------

log "Populating monitoring"
{
  emit_sts monitoring prometheus quay.io/prometheus/prometheus:v2.53.1 2 1 4Gi 9090 50Gi tsdb prometheus
  emit_sts monitoring alertmanager quay.io/prometheus/alertmanager:v0.27.0 3 100m 256Mi 9093 10Gi alerting prometheus
  emit_deploy monitoring grafana grafana/grafana:11.1.0 1 250m 512Mi 3000 dashboards prometheus
  emit_deploy monitoring kube-state-metrics registry.k8s.io/kube-state-metrics/kube-state-metrics:v2.13.0 1 100m 190Mi 8080 exporter prometheus
  emit_deploy monitoring prometheus-operator quay.io/prometheus-operator/prometheus-operator:v0.75.1 1 100m 200Mi 8080 controller prometheus
  emit_ds monitoring node-exporter quay.io/prometheus/node-exporter:v1.8.2 100m 64Mi exporter prometheus
  emit_svc monitoring grafana 3000
} | k apply -f -

k apply -f - <<'EOF'
---
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: node-exporter
  namespace: monitoring
spec:
  selector:
    matchLabels: {app.kubernetes.io/name: node-exporter}
  endpoints:
    - port: metrics
      interval: 30s
---
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: traefik
  namespace: monitoring
spec:
  namespaceSelector:
    matchNames: [traefik]
  selector:
    matchLabels: {app.kubernetes.io/name: traefik}
  endpoints:
    - port: metrics
      interval: 30s
---
apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: platform-alerts
  namespace: monitoring
spec:
  groups:
    - name: node.rules
      rules:
        - alert: NodeMemoryPressure
          expr: node_memory_working_set_bytes / node_memory_total_bytes > 0.9
          for: 10m
          labels: {severity: warning}
EOF

# --- cert-manager -----------------------------------------------------------------

log "Populating cert-manager"
{
  emit_deploy cert-manager cert-manager quay.io/jetstack/cert-manager-controller:v1.15.3 1 100m 256Mi 9402 controller cert-manager
  emit_deploy cert-manager cert-manager-webhook quay.io/jetstack/cert-manager-webhook:v1.15.3 1 50m 128Mi 10250 webhook cert-manager
  emit_deploy cert-manager cert-manager-cainjector quay.io/jetstack/cert-manager-cainjector:v1.15.3 1 50m 128Mi 9402 cainjector cert-manager
} | k apply -f -

k apply -f - <<'EOF'
---
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: letsencrypt-prod
spec:
  acme:
    server: https://acme-v02.api.letsencrypt.org/directory
    email: platform@example.com
    privateKeySecretRef: {name: letsencrypt-prod-key}
    solvers:
      - http01:
          ingress: {class: traefik}
status:
  conditions:
    - type: Ready
      status: "True"
      reason: ACMEAccountRegistered
      message: The ACME account was registered with the ACME server
---
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: selfsigned
spec:
  selfSigned: {}
status:
  conditions:
    - type: Ready
      status: "True"
      reason: IsReady
      message: ""
---
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: shop-example-com
  namespace: shop-frontend
spec:
  secretName: shop-example-com-tls
  issuerRef: {name: letsencrypt-prod, kind: ClusterIssuer}
  dnsNames: [shop.example.com, www.shop.example.com]
status:
  conditions:
    - type: Ready
      status: "True"
      reason: Ready
      message: Certificate is up to date and has not expired
  notAfter: "2026-10-02T00:00:00Z"
---
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: checkout-example-com
  namespace: shop-checkout
spec:
  secretName: checkout-example-com-tls
  issuerRef: {name: letsencrypt-prod, kind: ClusterIssuer}
  dnsNames: [checkout.example.com]
status:
  conditions:
    - type: Ready
      status: "True"
      reason: Ready
      message: Certificate is up to date and has not expired
  notAfter: "2026-09-18T00:00:00Z"
---
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: grafana-internal
  namespace: monitoring
spec:
  secretName: grafana-internal-tls
  issuerRef: {name: letsencrypt-prod, kind: ClusterIssuer}
  dnsNames: [grafana.internal.example.com]
status:
  conditions:
    - type: Ready
      status: "False"
      reason: DoesNotExist
      message: Issuing certificate as Secret does not exist
EOF

# --- traefik gateway ---------------------------------------------------------------

log "Populating traefik"
{
  emit_deploy traefik traefik traefik:v3.1.4 3 300m 256Mi 8000 gateway traefik
  emit_svc traefik traefik 80 LoadBalancer
} | k apply -f -

k apply -f - <<'EOF'
---
apiVersion: traefik.io/v1
kind: Middleware
metadata:
  name: security-headers
  namespace: traefik
spec:
  headers:
    stsSeconds: 31536000
    frameDeny: true
---
apiVersion: traefik.io/v1
kind: IngressRoute
metadata:
  name: shop-frontend
  namespace: shop-frontend
spec:
  entryPoints: [websecure]
  routes:
    - match: Host(`shop.example.com`)
      kind: Rule
      services:
        - name: web
          port: 8080
      middlewares:
        - name: security-headers
          namespace: traefik
  tls:
    secretName: shop-example-com-tls
---
apiVersion: traefik.io/v1
kind: IngressRoute
metadata:
  name: checkout-api
  namespace: shop-checkout
spec:
  entryPoints: [websecure]
  routes:
    - match: Host(`checkout.example.com`) && PathPrefix(`/api`)
      kind: Rule
      services:
        - name: checkout-api
          port: 8080
  tls:
    secretName: checkout-example-com-tls
---
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: grafana
  namespace: monitoring
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt-prod
spec:
  ingressClassName: traefik
  rules:
    - host: grafana.internal.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: grafana
                port: {number: 3000}
  tls:
    - hosts: [grafana.internal.example.com]
      secretName: grafana-internal-tls
---
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: web
  namespace: shop-frontend
spec:
  ingressClassName: traefik
  rules:
    - host: shop.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: web
                port: {number: 8080}
  tls:
    - hosts: [shop.example.com]
      secretName: shop-example-com-tls
---
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: checkout-api
  namespace: shop-checkout
spec:
  ingressClassName: traefik
  rules:
    - host: checkout.example.com
      http:
        paths:
          - path: /api
            pathType: Prefix
            backend:
              service:
                name: checkout-api
                port: {number: 8080}
  tls:
    - hosts: [checkout.example.com]
      secretName: checkout-example-com-tls
EOF

# --- gateway API (HTTPRoute) ---------------------------------------------------------
# Structural stand-ins (see emit_crd above): a GatewayClass/Gateway "public" plus
# HTTPRoutes attached to it from shop-frontend and shop-checkout, and one
# deliberately unattached route (valid but not accepted) — the #1 Gateway API
# footgun per docs/design/README.md's 23b spec.

log "Populating gateway API (GatewayClass/Gateway/HTTPRoute)"
k apply -f - <<'EOF'
---
apiVersion: gateway.networking.k8s.io/v1
kind: GatewayClass
metadata:
  name: traefik
spec:
  controllerName: traefik.io/gateway-controller
status:
  conditions:
    - type: Ready
      status: "True"
      reason: Accepted
      message: Handled by traefik.io/gateway-controller
---
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: public
  namespace: traefik
spec:
  gatewayClassName: traefik
  listeners:
    - name: https
      protocol: HTTPS
      port: 443
      hostname: "*.example.com"
      tls:
        mode: Terminate
        certificateRefs:
          - name: shop-example-com-tls
status:
  addresses:
    - type: IPAddress
      value: 203.0.113.10
  conditions:
    - type: Ready
      status: "True"
      reason: ListenersValid
      message: Gateway is ready to route traffic
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: web-route
  namespace: shop-frontend
spec:
  parentRefs:
    - name: public
      namespace: traefik
  hostnames: [shop.example.com]
  rules:
    - backendRefs:
        - name: web
          port: 8080
status:
  parents:
    - parentRef: {name: public, namespace: traefik}
      controllerName: traefik.io/gateway-controller
      conditions:
        - type: Ready
          status: "True"
          reason: Accepted
          message: Route was valid and accepted
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: checkout-route
  namespace: shop-checkout
spec:
  parentRefs:
    - name: public
      namespace: traefik
  hostnames: [checkout.example.com]
  rules:
    - backendRefs:
        - name: checkout-api
          port: 8080
status:
  parents:
    - parentRef: {name: public, namespace: traefik}
      controllerName: traefik.io/gateway-controller
      conditions:
        - type: Ready
          status: "True"
          reason: Accepted
          message: Route was valid and accepted
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: canary-route
  namespace: shop-frontend
spec:
  parentRefs:
    - name: public
      namespace: traefik
  hostnames: [canary.shop.example.com]
  rules:
    - backendRefs:
        - name: web
          port: 8080
status:
  parents:
    - parentRef: {name: public, namespace: traefik}
      controllerName: traefik.io/gateway-controller
      conditions:
        - type: Ready
          status: "False"
          reason: NoMatchingListenerHostname
          message: Listener hostname "*.example.com" does not match route hostname "canary.shop.example.com"
EOF

# --- logging & data platform ---------------------------------------------------------

log "Populating logging and data-platform"
{
  emit_ds logging fluent-bit cr.fluentbit.io/fluent/fluent-bit:3.1.9 100m 128Mi collector logging
  emit_sts logging loki grafana/loki:3.1.1 2 500m 1Gi 3100 100Gi storage logging
  emit_sts data-platform kafka confluentinc/cp-kafka:7.7.1 3 1 4Gi 9092 200Gi broker kafka
  emit_sts data-platform zookeeper confluentinc/cp-zookeeper:7.7.1 3 250m 1Gi 2181 20Gi coordination kafka
  emit_deploy data-platform kafka-connect confluentinc/cp-kafka-connect:7.7.1 2 500m 2Gi 8083 connect kafka
  emit_deploy data-platform schema-registry confluentinc/cp-schema-registry:7.7.1 2 250m 512Mi 8081 schemas kafka
} | k apply -f -

# --- application namespaces ------------------------------------------------------------

log "Populating shop-frontend / shop-checkout / shop-inventory / ml-serving"
{
  emit_deploy shop-frontend web ghcr.io/acme/storefront-web:5.12.0 6 250m 512Mi 8080 frontend storefront
  emit_deploy shop-frontend assets nginx:1.27.1 2 100m 128Mi 8080 static-assets storefront
  emit_svc shop-frontend web 8080
  emit_svc shop-frontend assets 8080

  emit_deploy shop-checkout checkout-api ghcr.io/acme/checkout-api:2.31.4 4 500m 1Gi 8080 api checkout "      priorityClassName: business-critical"
  emit_deploy shop-checkout payment-gateway ghcr.io/acme/payment-gateway:1.19.2 3 500m 768Mi 8443 gateway checkout "      priorityClassName: business-critical"
  emit_deploy shop-checkout order-worker ghcr.io/acme/order-worker:2.31.4 2 250m 512Mi 8080 worker checkout
  emit_sts shop-checkout redis redis:7.4.0 3 250m 1Gi 6379 10Gi cache checkout
  emit_svc shop-checkout checkout-api 8080
  emit_svc shop-checkout payment-gateway 8443

  emit_deploy shop-inventory inventory-api ghcr.io/acme/inventory-api:3.4.1 3 250m 512Mi 8080 api inventory
  emit_deploy shop-inventory stock-sync ghcr.io/acme/stock-sync:3.4.1 2 250m 512Mi 8080 sync inventory
  emit_sts shop-inventory postgres postgres:16.4 1 1 4Gi 5432 100Gi database inventory
  emit_svc shop-inventory inventory-api 8080

  # No node carries the accelerator label, so these stay Pending — deliberate
  # status variety for the console.
  emit_deploy ml-serving llm-inference ghcr.io/acme/llm-inference:0.9.1 2 4 16Gi 8000 inference ml "      nodeSelector:
        accelerator: nvidia-a100"
  emit_deploy ml-serving feature-store ghcr.io/acme/feature-store:1.2.0 2 500m 1Gi 8500 features ml
} | k apply -f -

k apply -f - <<'EOF'
---
# The one deliberately multi-container pod spec — exec pickers and per-container
# UIs need at least one of these to have something to show.
apiVersion: apps/v1
kind: Deployment
metadata:
  name: edge-proxy
  namespace: shop-frontend
  labels:
    app.kubernetes.io/name: edge-proxy
    app.kubernetes.io/component: proxy
    app.kubernetes.io/part-of: storefront
spec:
  replicas: 2
  selector:
    matchLabels:
      app.kubernetes.io/name: edge-proxy
  template:
    metadata:
      labels:
        app.kubernetes.io/name: edge-proxy
        app.kubernetes.io/component: proxy
        app.kubernetes.io/part-of: storefront
      annotations:
        kwok.x-k8s.io/usage-cpu: 150m
        kwok.x-k8s.io/usage-memory: 192Mi
    spec:
      containers:
        - name: app
          image: ghcr.io/acme/edge-proxy:2.4.0
          ports:
            - containerPort: 8080
          resources:
            requests: {cpu: 100m, memory: 128Mi}
            limits: {memory: 128Mi}
        - name: envoy
          image: envoyproxy/envoy:v1.31.0
          ports:
            - containerPort: 9901
          resources:
            requests: {cpu: 100m, memory: 128Mi}
            limits: {memory: 128Mi}
---
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: web
  namespace: shop-frontend
spec:
  scaleTargetRef: {apiVersion: apps/v1, kind: Deployment, name: web}
  minReplicas: 6
  maxReplicas: 20
  metrics:
    - type: Resource
      resource:
        name: cpu
        target: {type: Utilization, averageUtilization: 70}
---
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: checkout-api
  namespace: shop-checkout
spec:
  scaleTargetRef: {apiVersion: apps/v1, kind: Deployment, name: checkout-api}
  minReplicas: 4
  maxReplicas: 16
  metrics:
    - type: Resource
      resource:
        name: cpu
        target: {type: Utilization, averageUtilization: 65}
---
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: web
  namespace: shop-frontend
spec:
  minAvailable: 4
  selector:
    matchLabels: {app.kubernetes.io/name: web}
---
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: checkout-api
  namespace: shop-checkout
spec:
  minAvailable: 3
  selector:
    matchLabels: {app.kubernetes.io/name: checkout-api}
---
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: kafka
  namespace: data-platform
spec:
  maxUnavailable: 1
  selector:
    matchLabels: {app.kubernetes.io/name: kafka}
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: payment-gateway-lockdown
  namespace: shop-checkout
spec:
  podSelector:
    matchLabels: {app.kubernetes.io/name: payment-gateway}
  policyTypes: [Ingress]
  ingress:
    - from:
        - podSelector:
            matchLabels: {app.kubernetes.io/name: checkout-api}
      ports:
        - port: 8443
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: web-config
  namespace: shop-frontend
data:
  FEATURE_FLAGS: "new-cart=true,recs-v2=false"
  CDN_BASE_URL: https://cdn.example.com
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: checkout-config
  namespace: shop-checkout
data:
  PAYMENT_TIMEOUT_MS: "3000"
  RETRY_LIMIT: "3"
---
apiVersion: v1
kind: Secret
metadata:
  name: payment-provider-credentials
  namespace: shop-checkout
type: Opaque
stringData:
  api-key: not-a-real-key
  webhook-secret: not-a-real-secret
---
apiVersion: v1
kind: Secret
metadata:
  name: postgres-credentials
  namespace: shop-inventory
type: Opaque
stringData:
  username: inventory
  password: not-a-real-password
---
apiVersion: v1
kind: Secret
metadata:
  name: shop-example-com-tls
  namespace: shop-frontend
type: kubernetes.io/tls
stringData:
  tls.crt: "-----BEGIN CERTIFICATE-----\nfake\n-----END CERTIFICATE-----"
  tls.key: "-----BEGIN PRIVATE KEY-----\nfake\n-----END PRIVATE KEY-----"
EOF

# --- batch ------------------------------------------------------------------------------

log "Populating batch-jobs"
k apply -f - <<'EOF'
---
apiVersion: batch/v1
kind: CronJob
metadata:
  name: nightly-etl
  namespace: batch-jobs
spec:
  schedule: "0 2 * * *"
  concurrencyPolicy: Forbid
  jobTemplate:
    spec:
      template:
        metadata:
          annotations:
            kwok.x-k8s.io/usage-cpu: "2"
            kwok.x-k8s.io/usage-memory: 4Gi
        spec:
          restartPolicy: Never
          containers:
            - name: etl
              image: ghcr.io/acme/etl-runner:4.2.0
              resources:
                requests: {cpu: "2", memory: 4Gi}
---
apiVersion: batch/v1
kind: CronJob
metadata:
  name: report-generator
  namespace: batch-jobs
spec:
  schedule: "0 6 * * 1"
  suspend: true
  jobTemplate:
    spec:
      template:
        spec:
          restartPolicy: Never
          containers:
            - name: reports
              image: ghcr.io/acme/report-generator:1.8.3
              resources:
                requests: {cpu: 500m, memory: 1Gi}
---
apiVersion: batch/v1
kind: Job
metadata:
  name: index-rebuild-20260714
  namespace: batch-jobs
spec:
  backoffLimit: 2
  template:
    metadata:
      annotations:
        kwok.x-k8s.io/usage-cpu: "1"
        kwok.x-k8s.io/usage-memory: 2Gi
    spec:
      restartPolicy: Never
      containers:
        - name: reindex
          image: ghcr.io/acme/search-indexer:2.0.1
          resources:
            requests: {cpu: "1", memory: 2Gi}
EOF

# --- summary --------------------------------------------------------------------------

log "Waiting for pods to settle"
sleep 3

echo
echo "Cluster ready. Context: ${CTX}"
echo
k get nodes -o wide 2>/dev/null | awk '{print "  " $0}' || k get nodes | awk '{print "  " $0}'
echo
echo "Pods per namespace:"
k get pods -A --no-headers | awk '{count[$1]++} END {for (ns in count) printf "  %-20s %d\n", ns, count[ns]}' | sort
echo
echo "Try:  kubectl --context ${CTX} get pods -A"
echo "      go run ./cmd/kute   (context is merged into \$KUBECONFIG)"
