package browse

import (
	"sort"
	"strings"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/resources"
)

// workloadKinds default-sort unhealthy-first (mvp-plan.md §Phase 1) — kinds
// whose StatusClass reflects an operational health signal worth surfacing
// first. Everything else keeps resources.List's stable namespace/name order.
var workloadKinds = map[kube.ResourceKind]bool{
	kube.KindPod:         true,
	kube.KindDeployment:  true,
	kube.KindDaemonSet:   true,
	kube.KindStatefulSet: true,
	kube.KindReplicaSet:  true,
	kube.KindJob:         true,
}

// healthRank orders StatusClass worst-first: failing and warning rows sort
// to the top, neutral (e.g. completed) rows sink to the bottom.
func healthRank(class resources.StatusClass) int {
	switch class {
	case resources.StatusFail:
		return 0
	case resources.StatusWarn:
		return 1
	case resources.StatusOK:
		return 2
	default:
		return 3
	}
}

// sortForDisplay reorders rows in place for workload kinds; it's a no-op
// (preserving resources.List's namespace/name order) for every other kind.
// namespace == "" (6b's all-namespaces triage, docs/design README.md §6b)
// sorts namespace first so tableBody's grouped rendering sees contiguous
// per-namespace runs — namespaces with any unhealthy row before
// fully-healthy ones (which 6b renders collapsed and grayed out, so pushing
// them to the bottom keeps the top of the list all triage-worthy),
// alphabetical within each of those two partitions — then unhealthy-first
// *within* each namespace — a single namespace's rows sort exactly as 2a's
// plain unhealthy-first.
func sortForDisplay(kind kube.ResourceKind, namespace string, rows []resources.Row) {
	if kind == kube.KindCustomResourceDefinition {
		// docs/design README.md §14b: "sorted by group" — CRDDescriptor's
		// own Cells[1] is the CRD's API group; Name breaks ties within a
		// group so it stays deterministic.
		sort.SliceStable(rows, func(i, j int) bool {
			gi, gj := crdGroupCell(rows[i]), crdGroupCell(rows[j])
			if gi != gj {
				return gi < gj
			}
			return strings.Compare(strings.ToLower(rows[i].Name), strings.ToLower(rows[j].Name)) < 0
		})
		return
	}
	if !workloadKinds[kind] {
		return
	}
	grouped := namespace == ""
	nsTrouble := namespaceTrouble(rows, grouped)
	sort.SliceStable(rows, func(i, j int) bool {
		if grouped && rows[i].Namespace != rows[j].Namespace {
			ti, tj := nsTrouble[rows[i].Namespace], nsTrouble[rows[j].Namespace]
			if ti != tj {
				return ti // namespaces with trouble sort before fully-healthy ones
			}
			return rows[i].Namespace < rows[j].Namespace
		}
		ri, rj := healthRank(rows[i].Status), healthRank(rows[j].Status)
		if ri != rj {
			return ri < rj
		}
		return strings.Compare(strings.ToLower(rows[i].Name), strings.ToLower(rows[j].Name)) < 0
	})
}

// crdGroupCell reads the API group cell off a 14b CRD row (crdColumns'
// Cells[1], resources.projectCRD's own layout) — empty/out-of-range rows
// (never expected in practice) sort first, as the empty string.
func crdGroupCell(r resources.Row) string {
	if len(r.Cells) < 2 {
		return ""
	}
	return r.Cells[1]
}

// namespaceTrouble reports, for each namespace, whether it has any
// Warn/Fail row — nil when ungrouped, since sortForDisplay's namespace
// partitioning only applies in 6b's grouped mode.
func namespaceTrouble(rows []resources.Row, grouped bool) map[string]bool {
	if !grouped {
		return nil
	}
	trouble := make(map[string]bool)
	for _, r := range rows {
		if isUnhealthy(r.Status) {
			trouble[r.Namespace] = true
		}
	}
	return trouble
}
