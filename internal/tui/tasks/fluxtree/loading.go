package fluxtree

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/components"
)

const loadingSkeletonRows = 7

var loadingNameFrac = [loadingSkeletonRows]float64{0.62, 0.48, 0.70, 0.55, 0.66, 0.44, 0.58}

func (m Model) loadingStripLine(theme tui.Theme, width int) string {
	warn := lipgloss.NewStyle().Foreground(theme.Warn)
	dim := lipgloss.NewStyle().Foreground(theme.TextDim)
	faint := lipgloss.NewStyle().Foreground(theme.TextFaint)
	left := warn.Render(m.spinner.View()) + " " + dim.Render("listing Flux sources and reconcilers…")
	right := faint.Render("watch starts when the lists land")
	inner := max(width-2*tui.FrameInset, 0)
	space := inner - lipgloss.Width(left) - lipgloss.Width(right)
	if space < 1 {
		return left
	}
	return left + strings.Repeat(" ", space) + right
}

func loadingRowStyles(theme tui.Theme, row int) (lipgloss.Style, lipgloss.Style) {
	if row < (loadingSkeletonRows+1)/2 {
		return lipgloss.NewStyle().Foreground(theme.TextGhost), lipgloss.NewStyle().Foreground(theme.TextGhost2)
	}
	return lipgloss.NewStyle().Foreground(theme.TextGhost2), lipgloss.NewStyle().Foreground(theme.BorderSubtle)
}

func (m Model) loadingBody(width, height int) string {
	theme := m.Theme()
	columns := []components.Column{
		{Title: "", Min: 1},
		{Title: "Source / Reconciler", Min: 20, Flex: true},
		{Title: "Kind", Min: 11},
		{Title: "Revision", Min: 20, Flex: true},
		{Title: "Reconciled", Min: 10},
	}
	rows := make([]components.Row, loadingSkeletonRows)
	for i := range rows {
		nameStyle, cellStyle := loadingRowStyles(theme, i)
		cells := make([]components.Cell, len(columns))
		for j, col := range columns {
			style := cellStyle
			text := strings.Repeat("▬", max(col.Min*3/5, 1))
			if col.Title == "" {
				text = "●"
			}
			if col.Flex {
				style = nameStyle
				text = strings.Repeat("▬", max(int(40*loadingNameFrac[i]), 3))
			}
			cells[j] = components.Cell{Text: text, Style: style}
		}
		rows[i] = components.Row{Cells: cells}
	}
	table := components.Table{
		Columns: columns, Rows: rows, Selected: -1, Width: width,
		Height:      max(height-2, 1),
		HeaderStyle: lipgloss.NewStyle().Foreground(theme.TextFaint),
	}
	left := "– of –"
	footer := lipgloss.NewStyle().Foreground(theme.TextGhost).Render(
		components.Pad("  "+left+strings.Repeat(" ", max(width-4-lipgloss.Width(left)-6, 1))+"░░░░░░", width))
	return "\n" + table.Render() + "\n" + footer
}
