package browse

import (
	"github.com/kute-dev/kute/internal/resources"
	"github.com/kute-dev/kute/internal/tui"
)

// recomputeVisible reapplies the filter to m.rows (called after a reload or
// a filter-query edit), rebuilds m.display (grouping.go's buildDisplayRows)
// from the freshly filtered m.visible, and tries to keep the same row
// selected by name. A pendingSelect (a jump-palette resource Enter) wins
// over the previously selected name and is consumed here.
func (m *Model) recomputeVisible() {
	name := m.selectedName()
	if m.pendingSelect != "" {
		name = m.pendingSelect
		m.pendingSelect = ""
	}
	m.visible = applyFilter(m.rows, m.filterQuery)
	m.rebuildDisplay()
	m.restoreSelection(name)
}

// rebuildDisplay recomputes m.display from the current m.visible +
// expandedGroups — called whenever either changes (recomputeVisible,
// toggleGroup).
func (m *Model) rebuildDisplay() {
	m.display = buildDisplayRows(m.visible, m.grouped(), m.expandedGroups)
}

// selectedName returns the currently selected row's name, or "" when
// nothing is selected or the cursor rests on a fold/collapsed-summary line
// (no single row to name).
func (m Model) selectedName() string {
	row, ok := m.selectedRow()
	if !ok {
		return ""
	}
	return row.Name
}

// selectedRow returns the currently highlighted data row, if any — false
// both when nothing's selected and when the cursor is on one of 6b's
// synthetic group header/fold/collapsed-summary lines, so every row-scoped
// verb (open/yaml/logs/exec/delete/cordon/drain/rollout-restart) already
// gated on this becomes a no-op there for free. N (jump into namespace)
// deliberately does NOT use this — see selectedNamespace.
func (m Model) selectedRow() (resources.Row, bool) {
	if m.selected < 0 || m.selected >= len(m.display) {
		return resources.Row{}, false
	}
	dr := m.display[m.selected]
	if dr.kind != rowKindData {
		return resources.Row{}, false
	}
	return dr.row.row, true
}

// selectedNamespace returns the namespace of whatever's currently
// highlighted — unlike selectedRow, this works for every displayRow kind
// (header/fold/collapsed-summary lines carry their group's namespace too,
// not just data rows), since jumping into a namespace only ever needs the
// namespace name, not a specific pod. This is what "N" uses, so it works
// whether the cursor is on an actual pod or resting on the namespace's own
// group/fold/summary line.
func (m Model) selectedNamespace() (string, bool) {
	if m.selected < 0 || m.selected >= len(m.display) {
		return "", false
	}
	return m.display[m.selected].namespace, true
}

// restoreSelection re-finds name among m.display's data rows (just
// rebuilt), falling back to a clamped index when it's gone — filtered out,
// deleted, or now folded away inside a collapsed group (indistinguishable
// from "not found" here, same as the other two cases: press j/k or tab to
// reach it).
func (m *Model) restoreSelection(name string) {
	if name != "" {
		for i, dr := range m.display {
			if dr.kind == rowKindData && dr.row.row.Name == name {
				m.selected = i
				m.clampOffset()
				return
			}
		}
	}
	m.selected = clamp(m.selected, 0, len(m.display)-1)
	m.snapPastHeader()
	m.clampOffset()
}

// snapPastHeader nudges m.selected off a rowKindHeader entry (never
// selectable — moveSelection already skips these, but restoreSelection's
// clamp fallback can still land on one directly, e.g. right after
// toggleGroup expands the group the cursor was on). A header is always
// immediately followed by at least one data/fold/summary row in the same
// group, so scanning forward first always finds one; the backward scan only
// matters if display somehow ends on a header (shouldn't happen, but keeps
// this from ever leaving m.selected on an unselectable line).
func (m *Model) snapPastHeader() {
	if len(m.display) == 0 || m.display[m.selected].kind != rowKindHeader {
		return
	}
	for i := m.selected + 1; i < len(m.display); i++ {
		if m.display[i].kind != rowKindHeader {
			m.selected = i
			return
		}
	}
	for i := m.selected - 1; i >= 0; i-- {
		if m.display[i].kind != rowKindHeader {
			m.selected = i
			return
		}
	}
}

// moveSelection shifts the selection by delta, skipping over rowKindHeader
// entries (never selectable — fold/collapsed-summary lines ARE valid stops,
// since a fully-collapsed group has no rowKindData entries at all and would
// otherwise be unreachable), and scrolls the offset to follow it.
func (m *Model) moveSelection(delta int) {
	if len(m.display) == 0 {
		m.selected, m.offset = 0, 0
		return
	}
	next := m.selected
	for step := 0; step < len(m.display); step++ {
		cand := clamp(next+delta, 0, len(m.display)-1)
		if cand == next {
			break
		}
		next = cand
		if m.display[next].kind != rowKindHeader {
			break
		}
	}
	m.selected = clamp(next, 0, len(m.display)-1)
	m.clampOffset()
}

// toggleGroup flips the expand state of the namespace group the cursor is
// currently in (6b's "tab") — works whether the cursor is on a real row, a
// fold line, or a collapsed-summary line, since every displayRow kind
// carries its namespace. Rebuilds m.display and tries to keep the same
// named row selected (falls back to the usual clamp when the cursor was on
// a fold/summary line, which has no name to preserve).
func (m *Model) toggleGroup() {
	if m.selected < 0 || m.selected >= len(m.display) {
		return
	}
	ns := m.display[m.selected].namespace
	if m.expandedGroups == nil {
		m.expandedGroups = make(map[string]bool)
	}
	m.expandedGroups[ns] = !m.expandedGroups[ns]
	name := m.selectedName()
	m.rebuildDisplay()
	m.restoreSelection(name)
}

// clampOffset keeps the selected line within the table's rendered viewport.
// m.selected indexes m.display, the same list Table.Rows is built from one
// entry at a time, so this needs no grouped/ungrouped branch — a selected
// fold/header/summary line consumes exactly one viewport slot, same as any
// data row.
func (m *Model) clampOffset() {
	rows := m.tableDataRows()
	if m.selected < m.offset {
		m.offset = m.selected
	}
	if m.selected >= m.offset+rows {
		m.offset = m.selected - rows + 1
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

// tableDataRows is how many data rows the table viewport shows: the exact
// body height Frame budgets (header/strip/keybar bands, including the
// filter mode's second strip line) minus the three lines tableBody spends
// around the rows — the table's own header row, the rule dividing it from
// the data rows, and the FooterLine.
func (m Model) tableDataRows() int {
	body := tui.FrameBodyHeight(m.height, m.stripLineCount())
	return max(body-3, 1)
}

func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
