package browse

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/resources"
)

// workloadKinds default-sort unhealthy-first (mvp-plan.md §Phase 1) — kinds
// whose StatusClass reflects an operational health signal worth surfacing
// first. Everything else keeps resources.List's stable namespace/name order.
// Ingress (docs/design README.md §23a) joins these via its own backend
// health signal (projectIngress), not a workload in the traditional sense
// but the same "surface trouble first" reasoning applies.
var workloadKinds = map[kube.ResourceKind]bool{
	kube.KindPod:         true,
	kube.KindDeployment:  true,
	kube.KindDaemonSet:   true,
	kube.KindStatefulSet: true,
	kube.KindReplicaSet:  true,
	kube.KindJob:         true,
	kube.KindIngress:     true,
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

// applySort is sortForDisplay's own call site, upgraded with a manual
// override: once the user has picked a column with 1-9 (handleSortKey),
// that choice wins over the built-in default (unhealthy-first, CRD's
// group-then-name, etc.) for as long as it stays set — grouped mode (6b)
// is the one exception, since namespace partitioning keeps sole ownership
// of row order there and the sort arrow already hides in that mode too
// (tableBody's own !grouped guard).
func (m *Model) applySort() {
	if m.sortColumn > 0 && !m.grouped() {
		m.applyUserSort(m.rows)
		return
	}
	sortForDisplay(m.kind, m.namespace, m.rows)
}

// applyUserSort stable-sorts rows by m.sortColumn/m.sortAsc — a no-op if
// the column index doesn't exist for the current kind (defensive only;
// handleSortKey already guards this before ever setting m.sortColumn).
func (m Model) applyUserSort(rows []resources.Row) {
	idx := m.sortColumn - 1
	if idx < 0 || idx >= len(m.desc.Columns) {
		return
	}
	less := m.cellLess(m.desc.Columns[idx], idx)
	asc := m.sortAsc
	sort.SliceStable(rows, func(i, j int) bool {
		if asc {
			return less(rows[i], rows[j])
		}
		return less(rows[j], rows[i])
	})
}

// handleSortKey applies one 1-9 press: the same column pressed again flips
// direction, a different column switches to it at its own natural first
// direction (defaultSortAsc) — a no-op while grouped (6b's namespace
// partitioning owns row order there) or if this kind has no such column.
func (m *Model) handleSortKey(col int) {
	if m.grouped() {
		return
	}
	if col < 1 || col > len(m.desc.Columns) {
		return
	}
	if m.sortColumn == col {
		m.sortAsc = !m.sortAsc
	} else {
		m.sortColumn = col
		m.sortAsc = defaultSortAsc(m.desc.Columns[col-1])
	}
	m.applySort()
	m.recomputeVisible()
}

// descendingFirstTitles are numeric-ish columns not already covered by
// resources.RightAlignTitles: Restarts renders left-aligned (2a's own ↺
// header), and CPU/MEM are bar cells with no right-aligned text at all —
// but "biggest/busiest first" is exactly as sensible a first press for all
// three as it is for Age/Replicas/etc.
var descendingFirstTitles = map[string]bool{
	"Restarts": true, "CPU": true, "MEM": true,
}

// defaultSortAsc is a column's "first press" direction: descending for
// anything that reads as a metric/count/recency (busiest, most, or most
// recent first reads more useful than the reverse), ascending — the
// ordinary alphabetical sense — for everything else (Name, Status, Node,
// Ready, ...).
func defaultSortAsc(title string) bool {
	return !resources.RightAlignTitles[title] && !descendingFirstTitles[title]
}

// cellLess returns an ascending "a sorts before b" comparator for column
// idx (canonicalTitle is m.desc.Columns[idx] — the un-swapped title, e.g.
// "Restarts" rather than browseColumns' ↺ glyph, since that's what this
// dispatch and metricValue key off). CPU/MEM read live usage off
// m.podMetrics/m.nodeMetrics — resources.Row carries no numeric value for
// them at all, only a "–" placeholder cell (the bar is rendered from those
// maps, not from Cells). Age parses shortAge's compact display string back
// into a duration. Everything else compares as an integer when both cells
// parse as one (Restarts, Replicas, Completions, Active, Data, Rev, ...),
// falling back to a case-insensitive string compare otherwise — mirroring
// sortForDisplay's own tie-breaker above.
func (m Model) cellLess(canonicalTitle string, idx int) func(a, b resources.Row) bool {
	switch canonicalTitle {
	case "Age":
		return func(a, b resources.Row) bool {
			return parseShortAge(cellAt(a, idx)) < parseShortAge(cellAt(b, idx))
		}
	case "CPU":
		return func(a, b resources.Row) bool {
			return m.metricValue(a.Name, true) < m.metricValue(b.Name, true)
		}
	case "MEM":
		return func(a, b resources.Row) bool {
			return m.metricValue(a.Name, false) < m.metricValue(b.Name, false)
		}
	default:
		return func(a, b resources.Row) bool {
			at, bt := cellAt(a, idx), cellAt(b, idx)
			if an, aok := parseNumeric(at); aok {
				if bn, bok := parseNumeric(bt); bok {
					return an < bn
				}
			}
			return strings.Compare(strings.ToLower(at), strings.ToLower(bt)) < 0
		}
	}
}

// cellAt reads row.Cells[idx], "" for a short/empty row (never expected in
// practice, same defensive convention as crdGroupCell above).
func cellAt(r resources.Row, idx int) string {
	if idx < 0 || idx >= len(r.Cells) {
		return ""
	}
	return r.Cells[idx]
}

// metricValue looks up a Pod/Node row's live CPU/MEM usage by name — the
// only place that data exists, since neither kind's Cells carry a numeric
// value for these (metrics.go's metricCell/nodes.go's nodeMetricCell render
// straight from these same maps). -1 for a row with no metrics yet (still
// loading, or the row is neither a Pod nor a Node), so it sorts below every
// real reading regardless of direction rather than colliding with a
// genuine zero-usage row.
func (m Model) metricValue(name string, cpu bool) int64 {
	switch m.kind {
	case kube.KindPod:
		if pm, ok := m.podMetrics[name]; ok {
			if cpu {
				return pm.CPUMilli
			}
			return pm.MemBytes
		}
	case kube.KindNode:
		if nm, ok := m.nodeMetrics[name]; ok {
			if cpu {
				return nm.CPUMilli
			}
			return nm.MemBytes
		}
	}
	return -1
}

// parseShortAge inverts resources' shortAge display format ("12m"/"3h"/
// "5d"/"45s") back into a comparable duration — kept browse-local since
// sort ordering is a browse concern, not resources'. Unparseable input
// (never expected — every kind's Age cell is always shortAge's own output)
// sorts as zero.
func parseShortAge(s string) time.Duration {
	if s == "" {
		return 0
	}
	n, err := strconv.Atoi(s[:len(s)-1])
	if err != nil {
		return 0
	}
	switch s[len(s)-1] {
	case 's':
		return time.Duration(n) * time.Second
	case 'm':
		return time.Duration(n) * time.Minute
	case 'h':
		return time.Duration(n) * time.Hour
	case 'd':
		return time.Duration(n) * 24 * time.Hour
	default:
		return 0
	}
}

// parseNumeric parses a plain base-10 integer cell (Restarts, Replicas,
// Completions, Active, Data, Rev, ...) for cellLess's numeric comparison.
func parseNumeric(s string) (int64, bool) {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return n, err == nil
}
