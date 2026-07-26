package helmhistory

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/components"
)

// loadingBarGlyph is the skeleton content's placeholder block — same glyph
// browse's and nodedetail's 15a loading states use, duplicated per the
// repo's package-local-seam convention.
const loadingBarGlyph = "▬"

// loadingRevisionRows is how many skeleton rows the rail shows while the
// revisions decode — deliberately fewer than browse's 7, since a release's
// history is a short list and a screenful of placeholders would overstate
// what's coming.
const loadingRevisionRows = 4

// loadingChartFrac are the skeleton rail's per-row CHART-column fill
// fractions — a fixed, varied set standing in for however many revisions the
// release actually has, mirroring browse's loadingNameFrac trick.
var loadingChartFrac = [loadingRevisionRows]float64{0.7, 0.5, 0.62, 0.44}

// loadingStripLine is 18a's loading strip: what's being read, and where it
// comes from — the applied-to-a-detail-screen version of docs/design
// README.md §15a's strip, alongside nodedetail's own.
func (m Model) loadingStripLine(theme tui.Theme, width int) string {
	warn := lipgloss.NewStyle().Foreground(theme.Warn)
	dim := lipgloss.NewStyle().Foreground(theme.TextDim)
	faint := lipgloss.NewStyle().Foreground(theme.TextFaint)

	left := warn.Render(m.spinner.View()) + " " + dim.Render(fmt.Sprintf("reading %s revisions…", m.name))
	right := faint.Render("decoded from the release's own secrets")
	return insetStripLine(padBetween(left, right, stripInnerWidth(width)), width)
}

// loadingRowStyles picks a skeleton row's two bar colors (chart brighter,
// other cells dimmer, both a tone darker for the back half of the rows) —
// duplicated from nodedetail.loadingRowStyles per the repo's
// package-local-seam convention, sized for loadingRevisionRows.
func loadingRowStyles(theme tui.Theme, row int) (flex, cell lipgloss.Style) {
	if row < (loadingRevisionRows+1)/2 {
		return lipgloss.NewStyle().Foreground(theme.TextGhost), lipgloss.NewStyle().Foreground(theme.TextGhost2)
	}
	return lipgloss.NewStyle().Foreground(theme.TextGhost2), lipgloss.NewStyle().Foreground(theme.BorderSubtle)
}

// loadingCellBar picks one skeleton rail-row cell's placeholder bar length —
// duplicated from nodedetail.loadingCellBar, reading loadingChartFrac.
func loadingCellBar(col components.Column, row int) string {
	if col.Title == "" {
		return "●"
	}
	if col.Flex {
		n := max(int(30*loadingChartFrac[row%loadingRevisionRows]), 3)
		return strings.Repeat(loadingBarGlyph, n)
	}
	n := max(col.Min*3/5, 1)
	return strings.Repeat(loadingBarGlyph, n)
}

// loadingFooterLine is Table.FooterLine's skeleton stand-in — "– of –" plus
// an empty scrollbar track, duplicated from nodedetail's own
// loadingPodsFooterLine (calling the real FooterLine here would show a
// confident "1–4 of 4" counted off the placeholder rows).
func loadingFooterLine(theme tui.Theme, width int) string {
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

// loadingBody is 18a's applied 15a: the shell (breadcrumb, strip, the rail's
// own REV/STATUS/CHART/UPDATED headers, keybar) paints instantly and
// placeholder bars fill the rows while load() is in flight — never a bare
// spinner-only blank screen (docs/design README.md §15a).
//
// It mirrors railBody's geometry exactly — same railColumns, same Height
// budget, same leading newline and footer line — so the revisions landing is
// a fill-in rather than a relayout. Strips renders one line in both states
// for the same reason.
func (m Model) loadingBody(theme tui.Theme, width, height int) string {
	rows := make([]components.Row, loadingRevisionRows)
	for i := range rows {
		flexStyle, cellStyle := loadingRowStyles(theme, i)
		cells := make([]components.Cell, len(railColumns))
		for c, col := range railColumns {
			style := cellStyle
			if col.Flex {
				style = flexStyle
			}
			cells[c] = components.Cell{Text: loadingCellBar(col, i), Style: style}
		}
		rows[i] = components.Row{Cells: cells}
	}

	t := components.Table{
		Columns:     railColumns,
		Rows:        rows,
		Selected:    -1,
		Width:       width,
		Height:      max(height-2, 1),
		HeaderStyle: lipgloss.NewStyle().Foreground(theme.TextFaint),
		FooterStyle: lipgloss.NewStyle().Foreground(theme.TextGhost),
	}
	return "\n" + t.Render() + "\n" + loadingFooterLine(theme, width)
}
