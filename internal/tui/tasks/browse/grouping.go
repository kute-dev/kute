package browse

import "github.com/kute-dev/kute/internal/resources"

// namespaceHealthCounts tallies each namespace's rows by StatusClass, for
// 6b's group-header/fold-line trouble summaries (groupHeaderLine/
// collapsedSummaryLine/foldLine in view.go).
func namespaceHealthCounts(visible []filterMatch) map[string]resources.HealthCounts {
	counts := make(map[string]resources.HealthCounts)
	for _, fm := range visible {
		c := counts[fm.row.Namespace]
		switch fm.row.Status {
		case resources.StatusOK:
			c.OK++
		case resources.StatusWarn:
			c.Warn++
		case resources.StatusFail:
			c.Fail++
		default:
			c.Neutral++
		}
		counts[fm.row.Namespace] = c
	}
	return counts
}

// displayRowKind identifies what one buildDisplayRows entry renders as.
type displayRowKind int

const (
	// rowKindData is a real data row (row/fm below is meaningful). The only
	// kind that exists outside grouped mode.
	rowKindData displayRowKind = iota
	// rowKindHeader is an expanded group's ▾ header line — never a
	// selectable stop (moveSelection always skips it), same as it always
	// has been.
	rowKindHeader
	// rowKindFold is a partially-shown group's "+N running · ↹ expand"
	// trailing line, standing in for the folded healthy tail.
	rowKindFold
	// rowKindCollapsedSummary is a fully-healthy group's single
	// "▸ ns · N pods · all running" line, replacing the header entirely.
	rowKindCollapsedSummary
	// rowKindSubLine is a full-width continuation line rendered directly
	// under its data row, carrying resources.Row.SubLine — §30a puts the
	// Flux Ready condition's message there verbatim. Never a selectable
	// stop: the row above it is the thing verbs act on, and stopping on a
	// line that has no object would make ctrl-d ambiguous.
	rowKindSubLine
)

// displayRow is one line browse renders/selects — either a real data row or
// one of 6b's synthetic group/fold/summary lines. namespace is set on every
// kind (including rowKindData) so toggleGroup can read it uniformly
// regardless of what's currently selected.
type displayRow struct {
	kind      displayRowKind
	namespace string
	row       filterMatch            // rowKindData only
	counts    resources.HealthCounts // rowKindHeader/rowKindCollapsedSummary
	folded    int                    // rowKindFold/rowKindCollapsedSummary: rows represented
	text      string                 // rowKindSubLine only
	subClass  resources.StatusClass  // rowKindSubLine only: its parent row's class
}

// buildDisplayRows expands visible into 6b's render/selection list —
// m.selected indexes this directly (not visible), and each entry maps 1:1
// to one components.Row in the final table (grouping.go used to separately
// reconcile a visible-space m.selected against Rows-space via
// groupBoundaries/rowsPosition; this replaces that translation layer
// outright now that selection just indexes the same list that renders).
//
// Ungrouped mode returns one rowKindData entry per row, unchanged from
// pre-collapse behavior. Grouped mode walks visible one namespace run at a
// time — sortForDisplay already sorts unhealthy-first within each run, so
// the leading Warn/Fail rows are exactly the ones that always stay shown —
// and, per group, unless expanded[namespace] is set:
//   - zero unhealthy rows: one rowKindCollapsedSummary line
//   - some unhealthy rows: rowKindHeader + the unhealthy rows + one
//     rowKindFold line for the folded healthy remainder
//
// expanded[namespace] renders rowKindHeader + every row in the group, the
// original (pre-collapse) 6b behavior.
func buildDisplayRows(visible []filterMatch, grouped bool, expanded map[string]bool, foldHealthy bool) []displayRow {
	if !grouped {
		// §30a's ungrouped fold: unhealthy rows stay shown, the healthy tail
		// collapses to one "+N ready" line. sortForDisplay has already put
		// the unhealthy ones first, so the split is a prefix count. The fold
		// line carries an empty namespace, which is the key toggleGroup
		// flips — there is no namespace to expand in ungrouped mode, so ""
		// stands in as the single fold's own key.
		if foldHealthy && !expanded[""] {
			unhealthy := 0
			for unhealthy < len(visible) && staysVisible(visible[unhealthy].row) {
				unhealthy++
			}
			if folded := len(visible) - unhealthy; folded > 0 && unhealthy > 0 {
				out := make([]displayRow, 0, unhealthy+2)
				for _, fm := range visible[:unhealthy] {
					out = appendWithSubLine(out, fm)
				}
				return append(out, displayRow{kind: rowKindFold, folded: folded})
			}
		}
		rows := make([]displayRow, 0, len(visible))
		for _, fm := range visible {
			rows = appendWithSubLine(rows, fm)
		}
		return rows
	}

	counts := namespaceHealthCounts(visible)
	var out []displayRow
	i := 0
	for i < len(visible) {
		ns := visible[i].row.Namespace
		j := i
		for j < len(visible) && visible[j].row.Namespace == ns {
			j++
		}
		unhealthy := 0
		for k := i; k < j && isUnhealthy(visible[k].row.Status); k++ {
			unhealthy++
		}
		nsCounts := counts[ns]

		switch {
		case expanded[ns], unhealthy == j-i:
			// expanded, or every row in the group is unhealthy — nothing
			// left to fold, so this renders identically to the expanded
			// case (no spurious "+0 running" fold line).
			out = append(out, displayRow{kind: rowKindHeader, namespace: ns, counts: nsCounts})
			for k := i; k < j; k++ {
				out = appendWithSubLine(out, visible[k])
			}
		case unhealthy == 0:
			out = append(out, displayRow{kind: rowKindCollapsedSummary, namespace: ns, counts: nsCounts, folded: j - i})
		default:
			out = append(out, displayRow{kind: rowKindHeader, namespace: ns, counts: nsCounts})
			for k := i; k < i+unhealthy; k++ {
				out = appendWithSubLine(out, visible[k])
			}
			out = append(out, displayRow{kind: rowKindFold, namespace: ns, folded: j - i - unhealthy})
		}
		i = j
	}
	return out
}

// appendWithSubLine appends fm's data row plus, when the projection gave it
// one, its continuation line. Driven by the row's own data rather than by a
// per-kind flag: a projection that has something to say says it.
func appendWithSubLine(out []displayRow, fm filterMatch) []displayRow {
	out = append(out, displayRow{kind: rowKindData, namespace: fm.row.Namespace, row: fm})
	if fm.row.SubLine != "" {
		out = append(out, displayRow{kind: rowKindSubLine, namespace: fm.row.Namespace, text: fm.row.SubLine, subClass: fm.row.Status})
	}
	return out
}

// selectableStop reports whether the cursor may land on a line of this
// kind. Headers and sub-lines are pass-through decoration; fold and
// collapsed-summary lines ARE valid stops, since a fully-collapsed group
// has no data rows at all and would otherwise be unreachable.
func selectableStop(kind displayRowKind) bool {
	return kind != rowKindHeader && kind != rowKindSubLine
}

// staysVisible reports whether a row is exempt from §30a's healthy-tail
// fold. Unhealthy rows obviously are; so is a suspended one, because
// folding it away would make it exactly the footnote §30a says it must not
// be. Matches rowHealthRank's ordering, so the exempt rows are always the
// sorted prefix.
func staysVisible(r resources.Row) bool {
	return isUnhealthy(r.Status) || r.Suspended
}

func isUnhealthy(status resources.StatusClass) bool {
	return status == resources.StatusFail || status == resources.StatusWarn
}
