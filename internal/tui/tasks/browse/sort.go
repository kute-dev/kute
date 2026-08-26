package browse

import (
	"cmp"
	"slices"
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

// rowHealthRank orders one row worst-first. It is healthRank plus §30a's
// one addition: a suspended Flux object sorts *above* a healthy one, ahead
// of OK and behind Warn.
//
// Suspended is StatusNeutral, which for every other kind means "finished,
// nothing to see" (a Completed pod) and correctly sinks to the bottom. For
// Flux it means an operator stopped reconciliation and the object may be
// silently drifting from git — a risk state, and §30a's whole framing is
// that it is "a state, not a footnote". Only Flux projections set
// Row.Suspended, so no other kind's order changes.
func rowHealthRank(r resources.Row) int {
	if r.Suspended {
		return 2
	}
	if rank := healthRank(r.Status); rank >= 2 {
		// Shift OK and Neutral down one to make room for suspended above them.
		return rank + 1
	}
	return healthRank(r.Status)
}

// argoRank orders an Argo CD Application row per docs/design README.md
// §33a: Degraded/Missing → OutOfSync → Progressing → Healthy. Reads the
// row's own rendered Sync/Health cells directly (Cells[1]/Cells[2] —
// argoColumns' layout) rather than StatusClass: Sync and Health are two
// independent signals collapsed into one glyph/class for color and
// fold-visibility (resources.argoStatus), and that collapse alone can't
// express a four-way order — Progressing is StatusNeutral and Healthy is
// StatusOK, and rowHealthRank sorts every OK row ahead of every Neutral
// one, backwards from §33a's stated order. Same "read the cell instead of
// the class" idiom crdGroupCell already uses for 14b's own bespoke sort.
func argoRank(r resources.Row) int {
	sync, health := cellAt(r, 1), cellAt(r, 2)
	switch {
	case health == "Degraded" || health == "Missing":
		return 0
	case sync == "OutOfSync":
		return 1
	case health == "Progressing":
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
// unhealthyFirst adds the §30a Flux, §33a Argo and §35b Certificate kinds
// to the workload set: their whole point is triage ("unhealthy first ·
// suspended is a state, not a footnote"), and a Kustomization that failed
// to reconcile or a Degraded Application has to be at the top for the same
// reason a crashlooping pod does. argoRanked picks argoRank over the
// generic rowHealthRank for the within-namespace comparator — see
// argoRank's own doc comment for why the two curated kinds can't share one.
// certRanked adds §35b's own addition on top of the generic rank: "among
// ready certs, soonest expiry floats up" — a same-rank tiebreak applied
// before the fallback to name, via certExpiryTiebreak.
// jobAgeTiebreak breaks a same-rank tie between two Job rows newest-first
// (§37a: "unhealthy first, then newest") — reads the already-rendered AGE
// cell via parseShortAge (this file's own age-column sort comparator)
// rather than adding a dedicated timestamp field to Row, since AGE already
// carries exactly the precision the mockup's own tiebreak needs. AGE is
// always Job's last column (registry.go's Job Columns).
func jobAgeTiebreak(a, b resources.Row) int {
	da, db := parseShortAge(cellAt(a, len(a.Cells)-1)), parseShortAge(cellAt(b, len(b.Cells)-1))
	switch {
	case da < db:
		return -1 // a is newer (smaller age) -> sorts first
	case da > db:
		return 1
	default:
		return 0
	}
}

func sortForDisplay(kind kube.ResourceKind, namespace string, rows []resources.Row, unhealthyFirst, argoRanked, certRanked, jobRanked bool) {
	if kind == kube.KindCustomResourceDefinition {
		// docs/design README.md §14b: "sorted by group" — CRDDescriptor's
		// own Cells[1] is the CRD's API group; Name breaks ties within a
		// group so it stays deterministic.
		slices.SortStableFunc(rows, func(a, b resources.Row) int {
			return cmp.Or(
				cmp.Compare(crdGroupCell(a), crdGroupCell(b)),
				cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)),
			)
		})
		return
	}
	if !workloadKinds[kind] && !unhealthyFirst {
		return
	}
	rank := rowHealthRank
	if argoRanked {
		rank = argoRank
	}
	grouped := namespace == ""
	nsTrouble := namespaceTrouble(rows, grouped)
	slices.SortStableFunc(rows, func(a, b resources.Row) int {
		if grouped && a.Namespace != b.Namespace {
			ta, tb := nsTrouble[a.Namespace], nsTrouble[b.Namespace]
			if ta != tb {
				// Namespaces with trouble sort before fully-healthy ones.
				if ta {
					return -1
				}
				return 1
			}
			return cmp.Compare(a.Namespace, b.Namespace)
		}
		if c := cmp.Compare(rank(a), rank(b)); c != 0 {
			return c
		}
		if certRanked {
			if t := certExpiryTiebreak(a, b); t != 0 {
				return t
			}
		}
		if jobRanked {
			if t := jobAgeTiebreak(a, b); t != 0 {
				return t
			}
		}
		return cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
}

// certExpiryTiebreak breaks a same-rank tie between two Certificate rows by
// soonest expiry first (docs/design README.md §35b: "among ready certs,
// soonest expiry floats up") — 0 (defer to the name tiebreak, same as every
// other kind) when either side's ExpiresAt is zero: not yet issued, or not
// applicable to this row's state.
func certExpiryTiebreak(a, b resources.Row) int {
	if a.ExpiresAt.IsZero() || b.ExpiresAt.IsZero() {
		return 0
	}
	switch {
	case a.ExpiresAt.Before(b.ExpiresAt):
		return -1
	case a.ExpiresAt.After(b.ExpiresAt):
		return 1
	default:
		return 0
	}
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
	certRanked := m.kind == kube.KindCertificate
	jobRanked := m.kind == kube.KindJob
	sortForDisplay(m.kind, m.namespace, m.rows, m.desc.Flux || m.desc.Argo || certRanked, m.desc.Argo, certRanked, jobRanked)
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
	slices.SortStableFunc(rows, func(a, b resources.Row) int {
		if !asc {
			a, b = b, a
		}
		switch {
		case less(a, b):
			return -1
		case less(b, a):
			return 1
		}
		return 0
	})
}

// handleSortKey applies one 1-9 press: the same column pressed again flips
// direction, a different column switches to it at its own natural first
// direction (defaultSortAsc) — a no-op while grouped (6b's namespace
// partitioning owns row order there) or if this kind has no such column.
// CronJobs sort exactly like every other kind (docs/design README.md §36a):
// name order by default (sortForDisplay's no-op path, since CronJob is
// absent from workloadKinds/unhealthyFirst), but a manual 1-9 press wins
// over that default the same as anywhere else — applyCronJobTick re-applies
// it every clock tick so it survives the per-second row rebuild.
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
			return m.metricValue(a.Namespace, a.Name, true) < m.metricValue(b.Namespace, b.Name, true)
		}
	case "MEM":
		return func(a, b resources.Row) bool {
			return m.metricValue(a.Namespace, a.Name, false) < m.metricValue(b.Namespace, b.Name, false)
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
func (m Model) metricValue(namespace, name string, cpu bool) int64 {
	switch m.kind {
	case kube.KindPod:
		if pm, ok := m.podMetrics[kube.PodKey(namespace, name)]; ok {
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
