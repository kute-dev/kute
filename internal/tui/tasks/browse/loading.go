package browse

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/components"
)

// loadingSkeletonRows is how many placeholder rows 15a shows below the
// column headers while the first list is in flight (docs/design
// README.md §15a's mock: "hint-placeholder-count=7").
const loadingSkeletonRows = 7

// loadingNameFrac are 15a's mock per-row NAME-column fill fractions
// (skeletonRows' nameW), reused verbatim so rows read as differently-sized
// real names rather than one uniform block — every other column's bar is a
// fixed width across rows, matching the mock exactly.
var loadingNameFrac = [loadingSkeletonRows]float64{0.62, 0.48, 0.70, 0.55, 0.66, 0.44, 0.58}

// loadingBarGlyph is the skeleton cell's placeholder block.
const loadingBarGlyph = "▬"

// loadingNameNominalWidth is the nominal cell count loadingNameFrac scales
// against — long enough to vary noticeably row to row; Table truncates it to
// whatever the NAME column's real flexed width is, so this only needs to be
// a plausible upper bound, not an exact measurement.
const loadingNameNominalWidth = 40

// loadingStripLine is 15a's strip replacing the health strip while loading:
// "listing <kind> in <scope>…" on the left, a note that the watch hasn't
// started yet on the right (docs/design README.md §15a).
func (m Model) loadingStripLine(theme tui.Theme, width int) string {
	warn := lipgloss.NewStyle().Foreground(theme.Warn)
	dim := lipgloss.NewStyle().Foreground(theme.TextDim)
	faint := lipgloss.NewStyle().Foreground(theme.TextFaint)

	scope := ""
	switch {
	case m.desc.ClusterScoped:
		// 11a-style kinds have no namespace to name.
	case m.grouped():
		scope = " in all namespaces"
	default:
		scope = " in " + m.namespace
	}
	left := warn.Render(tui.GlyphPending) + " " +
		dim.Render(fmt.Sprintf("listing %s%s…", lowerDisplay(m.desc.Display), scope))
	right := faint.Render("watch starts when the list lands")
	return insetStripLine(padBetween(left, right, stripInnerWidth(width)), width)
}

// loadingRowStyles picks a skeleton row's two bar colors: name (brighter,
// the identifying column) and cell (dimmer, everything else) — both step one
// tone darker for the back half of the rows, standing in for the mock's
// continuous opacity fade with the app's two ghost tones (docs/design
// README.md §15a: "skeleton rows fade toward the bottom").
func loadingRowStyles(theme tui.Theme, row int) (name, cell lipgloss.Style) {
	if row < (loadingSkeletonRows+1)/2 {
		return lipgloss.NewStyle().Foreground(theme.TextGhost), lipgloss.NewStyle().Foreground(theme.TextGhost2)
	}
	return lipgloss.NewStyle().Foreground(theme.TextGhost2), lipgloss.NewStyle().Foreground(theme.BorderSubtle)
}

// loadingCellBar picks one skeleton cell's placeholder bar length: the
// leading status column gets a single dot, the flex NAME(-equivalent) column
// follows loadingNameFrac so it varies by row, and every fixed column gets a
// bar at roughly 60% of its declared width — long enough to read as text,
// short enough that Table's own trailing padding still shows (mirroring the
// mock's per-column fixed skeleton widths).
func loadingCellBar(col components.Column, row int) string {
	if col.Title == "" {
		return "●"
	}
	if col.Flex {
		n := max(int(loadingNameNominalWidth*loadingNameFrac[row%loadingSkeletonRows]), 3)
		return strings.Repeat(loadingBarGlyph, n)
	}
	n := max(col.Min*3/5, 1)
	return strings.Repeat(loadingBarGlyph, n)
}

// loadingBody renders 15a: the real column headers (Strips/Table both build
// them from the same browseColumns, so switching to live data is a fill-in,
// never a relayout) over skeletonRows placeholder rows, plus a "– of –"
// footer standing in for the real range/scrollbar.
func (m Model) loadingBody(width, height int) string {
	theme := m.Theme()
	cols := browseColumns(m.desc)
	if len(cols) == 0 {
		return ""
	}

	rows := make([]components.Row, loadingSkeletonRows)
	for i := range rows {
		nameStyle, cellStyle := loadingRowStyles(theme, i)
		cells := make([]components.Cell, len(cols))
		for c, col := range cols {
			style := cellStyle
			if col.Flex {
				style = nameStyle
			}
			cells[c] = components.Cell{Text: loadingCellBar(col, i), Style: style}
		}
		rows[i] = components.Row{Cells: cells}
	}

	t := components.Table{
		Columns:     cols,
		Rows:        rows,
		Selected:    -1,
		Width:       width,
		Height:      max(height-2, 1),
		HeaderStyle: lipgloss.NewStyle().Foreground(theme.TextFaint),
	}
	return "\n" + t.Render() + "\n" + m.loadingFooterLine(theme, width)
}

// loadingFooterLine is 15a's "– of –" + empty scrollbar track standing in
// for Table.FooterLine's real range/scrollbar before any rows exist.
func (m Model) loadingFooterLine(theme tui.Theme, width int) string {
	const inset = 2
	style := lipgloss.NewStyle().Foreground(theme.TextGhost)
	left := "– of –"
	right := strings.Repeat("░", 6)

	inner := max(width-2*inset, 0)
	avail := inner - lipgloss.Width(left) - lipgloss.Width(right)
	line := left
	if avail >= 1 {
		line = left + strings.Repeat(" ", avail) + right
	}
	return style.Render(components.Pad(strings.Repeat(" ", inset)+line, width))
}
