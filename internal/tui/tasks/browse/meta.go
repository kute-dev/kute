// 26a's 'm' inline labels/annotations editor — the panel itself lives in
// internal/tui/metapanel (shared with every object detail screen; task
// packages can't import one another, so the editor was extracted there).
// This file keeps only browse's own hosting glue: the open gate and the
// Body() branch that frames the panel under the frozen selected-row line,
// per the hosting contract metapanel's package doc comment spells out.
package browse

import (
	"strings"

	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/components"
	"github.com/kute-dev/kute/internal/tui/metapanel"
)

// beginMeta opens 26a's panel for the selected row. ok is false when nothing
// applies — mirrors beginSetImage/beginSetResources's ok-bool contract.
func (m *Model) beginMeta() bool {
	if !metapanel.Editable(m.kind) || m.mutator == nil || m.state != tui.TaskStateReady {
		return false
	}
	row, ok := m.selectedRow()
	if !ok {
		return false
	}
	p, ok := metapanel.Open(metapanel.Config{Session: m.session, Lister: m.lister}, m.kind, row.Namespace, row.Name)
	if !ok {
		return false
	}
	m.pendingMeta = p
	return true
}

// setMetaBody renders 26a's panel in place of the live table — same
// selected-row-alone simplification setImageBody/setResourcesBody already
// make; the grid and will-run strip come from the shared metapanel renderer.
func (m Model) setMetaBody(width, height int) string {
	theme := m.Theme()
	var lines []string
	if row, ok := m.selectedRow(); ok {
		lines = append(lines, m.setImageSelectedRowLine(row, theme, width))
	} else {
		lines = append(lines, m.columnHeaderLine(theme, width))
	}
	lines = append(lines, "")
	lines = append(lines, m.pendingMeta.PanelLines(width, &m.actions)...)
	lines = append(lines, "", m.pendingMeta.WillRunStrip(width, &m.actions))
	return components.Pad(strings.Join(lines, "\n"), width)
}
