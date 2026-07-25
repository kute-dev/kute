#!/usr/bin/env bash
# measure-cluster-payload.sh — report what kute pulls from a cluster at connect,
# and what each kind would cost if you opened it.
#
# Why this exists: kute starts informers lazily (only Namespaces, Pods and Nodes
# at connect; everything else on first read), so "how slow is startup" is really
# "how big are those three LISTs, plus CRD discovery". CRD discovery is the one
# that surprises people — a CustomResourceDefinition carries the full OpenAPI v3
# validation schema for every version it serves, and on a cluster running a few
# operators that is routinely megabytes. kute reads none of those schemas: it
# needs group/names/scope/versions/additionalPrinterColumns and the Established
# condition, nothing more.
#
# Usage:
#   scripts/measure-cluster-payload.sh              # measure the current kubecontext
#   scripts/measure-cluster-payload.sh --context X  # measure a specific one
#
# Reads only. Needs kubectl and python3.

set -euo pipefail

KUBECTL_ARGS=()
if [[ "${1:-}" == "--context" && -n "${2:-}" ]]; then
	KUBECTL_ARGS=(--context "$2")
fi

command -v kubectl >/dev/null || { echo "measure-cluster-payload.sh: kubectl not found" >&2; exit 1; }
command -v python3 >/dev/null || { echo "measure-cluster-payload.sh: python3 not found" >&2; exit 1; }

kube() { kubectl "${KUBECTL_ARGS[@]}" "$@"; }

human() { python3 -c "
import sys
n = int(sys.argv[1])
for unit in ('B', 'KB', 'MB', 'GB'):
    if n < 1024 or unit == 'GB':
        print(f'{n:.1f} {unit}' if unit != 'B' else f'{n} B')
        break
    n /= 1024
" "$1"; }

echo "context: $(kube config current-context)"
echo

# --- CRD discovery -----------------------------------------------------------
# Blocking at connect: kute waits for the CRD cache before the kind registry
# (and so the goto corpus and browse's kind list) is complete.

echo "== CRD discovery =="
crd_json=$(kube get crds -o json 2>/dev/null || echo '{"items":[]}')
printf '%s' "$crd_json" | python3 -c '
import json, sys

doc = json.load(sys.stdin)
items = doc.get("items", [])
if not items:
    print("  no CRDs on this cluster")
    raise SystemExit

total = len(json.dumps(doc))

# Everything kute actually reads out of a CRD (see kube.ParseDiscoveredKind).
kept = []
for it in items:
    spec = it.get("spec", {})
    kept.append({
        "name": it.get("metadata", {}).get("name"),
        "spec": {
            "group": spec.get("group"),
            "names": spec.get("names"),
            "scope": spec.get("scope"),
            "versions": [
                {k: v[k] for k in
                 ("name", "served", "storage", "deprecated", "additionalPrinterColumns")
                 if k in v}
                for v in spec.get("versions", [])
            ],
        },
        "status": {"conditions": it.get("status", {}).get("conditions", [])},
    })
needed = len(json.dumps({"items": kept}))

def mb(n):
    return f"{n/1024/1024:.2f} MB" if n >= 1024*1024 else f"{n/1024:.1f} KB"

print(f"  CRDs:            {len(items)}")
print(f"  transferred:     {mb(total)}")
print(f"  actually used:   {mb(needed)}  ({100*needed/total:.1f}%)")
print(f"  schema overhead: {mb(total-needed)}  ({100*(total-needed)/total:.1f}%)")
print()

# The worst offenders, so you can see whether it is one operator or many.
sized = sorted(
    ((len(json.dumps(it)), it.get("metadata", {}).get("name", "?")) for it in items),
    reverse=True,
)
print("  largest CRDs:")
for size, name in sized[:8]:
    print(f"    {mb(size):>10}  {name}")
'
echo

# --- Startup LISTs -----------------------------------------------------------
# The three kinds kute starts eagerly. Everything else waits for a first read.

echo "== eager kinds (fetched before first paint) =="
eager_total=0
for res in namespaces pods nodes; do
	bytes=$(kube get "$res" --all-namespaces -o json 2>/dev/null | wc -c || echo 0)
	count=$(kube get "$res" --all-namespaces --no-headers 2>/dev/null | wc -l || echo 0)
	eager_total=$((eager_total + bytes))
	printf '  %-14s %6s objects  %10s\n' "$res" "$count" "$(human "$bytes")"
done
printf '  %-14s %6s           %10s\n' "total" "" "$(human "$eager_total")"
echo

# --- Lazy LISTs --------------------------------------------------------------
# Each of these is paid only if you open that kind. Before lazy informers, all
# of them were pulled at connect alongside the eager set above.

echo "== lazy kinds (fetched on first read) =="
lazy_total=0
for res in secrets configmaps events replicasets controllerrevisions \
	deployments daemonsets statefulsets services ingresses \
	persistentvolumeclaims jobs cronjobs horizontalpodautoscalers \
	roles rolebindings clusterroles clusterrolebindings; do
	bytes=$(kube get "$res" --all-namespaces -o json 2>/dev/null | wc -c || echo 0)
	[[ "$bytes" -le 2 ]] && continue
	count=$(kube get "$res" --all-namespaces --no-headers 2>/dev/null | wc -l || echo 0)
	lazy_total=$((lazy_total + bytes))
	printf '  %-24s %6s objects  %10s\n' "$res" "$count" "$(human "$bytes")"
done
printf '  %-24s %6s           %10s\n' "total" "" "$(human "$lazy_total")"
echo

echo "== summary =="
printf '  before lazy informers, connect pulled:  %s\n' "$(human $((eager_total + lazy_total)))"
printf '  now it pulls:                           %s  (+ CRD discovery)\n' "$(human "$eager_total")"
