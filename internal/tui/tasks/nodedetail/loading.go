package nodedetail

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/components"
)

// loadingBarGlyph is the skeleton content's placeholder block — same glyph
// browse's 15a loading state uses (internal/tui/tasks/browse/loading.go),
// duplicated per the repo's package-local-seam convention.
const loadingBarGlyph = "▬"

// loadingStripLine is 11b's loading strip: what's fetching, and a note that
// the facts panel and pods table land together (a single load() call,
// unlike browse's per-kind watch) — the applied-to-a-detail-screen version
// of docs/design README.md §15a's strip.
func (m Model) loadingStripLine(theme tui.Theme, width int) string {
	warn := lipgloss.NewStyle().Foreground(theme.Warn)
	dim := lipgloss.NewStyle().Foreground(theme.TextDim)
	faint := lipgloss.NewStyle().Foreground(theme.TextFaint)

	left := warn.Render(tui.GlyphPending) + " " + dim.Render(fmt.Sprintf("fetching %s…", m.nodeName))
	right := faint.Render("conditions, allocation & pods load together")
	return insetStripLine(padBetween(left, right, stripInnerWidth(width)), width)
}

// loadingConditionFrac are the skeleton CONDITIONS block's per-line fill
// fractions — a fixed, varied set standing in for however many real
// conditions the node reports, mirroring browse's loadingNameFrac trick
// (internal/tui/tasks/browse/loading.go).
var loadingConditionFrac = []float64{0.55, 0.4, 0.62, 0.36}

// loadingConditionsBlock is CONDITIONS' skeleton: the real title (the shell
// paints instantly) over placeholder bars standing in for condition lines.
func loadingConditionsBlock(theme tui.Theme) []string {
	title := lipgloss.NewStyle().Foreground(theme.TextFaint).Bold(true).Render("CONDITIONS")
	style := lipgloss.NewStyle().Foreground(theme.TextGhost)
	lines := make([]string, 0, len(loadingConditionFrac)+1)
	lines = append(lines, title)
	for _, frac := range loadingConditionFrac {
		n := max(int(24*frac), 3)
		lines = append(lines, style.Render(strings.Repeat(loadingBarGlyph, n)))
	}
	return lines
}

// loadingAllocationBlock is ALLOCATED/ALLOCATABLE + TAINTS' skeleton: same
// line shape as allocationBlock/allocationBarLine (label, then a
// placeholder standing in for the bar+value), so the swap to live data
// doesn't relayout.
func loadingAllocationBlock(theme tui.Theme) []string {
	title := lipgloss.NewStyle().Foreground(theme.TextFaint).Bold(true).Render("ALLOCATED / ALLOCATABLE")
	dim := lipgloss.NewStyle().Foreground(theme.TextDim)
	style := lipgloss.NewStyle().Foreground(theme.TextGhost)

	bar := func(label string) string {
		return dim.Render(fmt.Sprintf("%-4s ", label)) +
			style.Render(strings.Repeat(loadingBarGlyph, 12)) + " " +
			style.Render(strings.Repeat(loadingBarGlyph, 9))
	}

	lines := []string{
		title,
		bar("cpu"),
		bar("mem"),
		bar("pods"),
		"",
		lipgloss.NewStyle().Foreground(theme.TextFaint).Bold(true).Render("TAINTS"),
		style.Render(strings.Repeat(loadingBarGlyph, 14)),
	}
	return lines
}

// loadingPodRows is how many skeleton rows the bottom pane shows —
// deliberately fewer than browse's 7 (internal/tui/tasks/browse/loading.go)
// since one node's pods panel is a smaller viewport than the full-screen
// table.
const loadingPodRows = 5

// loadingPodNameFrac are the skeleton pods table's per-row NAME-column fill
// fractions — same trick as browse's loadingNameFrac, sized for this
// package's own row count.
var loadingPodNameFrac = [loadingPodRows]float64{0.65, 0.45, 0.72, 0.5, 0.6}

// loadingRowStyles picks a skeleton row's two bar colors (name brighter,
// other cells dimmer, both a tone darker for the back half of the rows) —
// duplicated from browse.loadingRowStyles per the repo's package-local-seam
// convention, sized for loadingPodRows instead of browse's
// loadingSkeletonRows.
func loadingRowStyles(theme tui.Theme, row int) (name, cell lipgloss.Style) {
	if row < (loadingPodRows+1)/2 {
		return lipgloss.NewStyle().Foreground(theme.TextGhost), lipgloss.NewStyle().Foreground(theme.TextGhost2)
	}
	return lipgloss.NewStyle().Foreground(theme.TextGhost2), lipgloss.NewStyle().Foreground(theme.BorderSubtle)
}

// loadingCellBar picks one skeleton pod-row cell's placeholder bar length —
// duplicated from browse.loadingCellBar per the repo's package-local-seam
// convention, reading loadingPodNameFrac instead of browse's
// loadingNameFrac.
func loadingCellBar(col components.Column, row int) string {
	if col.Title == "" {
		return "●"
	}
	if col.Flex {
		n := max(int(40*loadingPodNameFrac[row%loadingPodRows]), 3)
		return strings.Repeat(loadingBarGlyph, n)
	}
	n := max(col.Min*3/5, 1)
	return strings.Repeat(loadingBarGlyph, n)
}

// loadingStripPlaceholder is podHealthStripLine's skeleton stand-in — a
// placeholder bar plus its own rule underneath, reserving the same two
// lines podHealthStripLine + podStripRule take, so the swap to the live
// strip doesn't relayout the table beneath it.
func loadingStripPlaceholder(theme tui.Theme, width int) string {
	style := lipgloss.NewStyle().Foreground(theme.TextGhost)
	left := style.Render(strings.Repeat(loadingBarGlyph, 24))
	return insetStripLine(padBetween(left, "", stripInnerWidth(width)), width) + "\n" + podStripRule(theme, width)
}

// loadingPodsFooterLine is Table.FooterLine's skeleton stand-in — "– of –"
// plus an empty scrollbar track, duplicated from browse's own
// loadingFooterLine per the repo's package-local-seam convention (calling
// the real FooterLine here would show a real "1–5 of 5" range off the
// skeleton's fake placeholder rows, not a loading look).
func loadingPodsFooterLine(theme tui.Theme, width int) string {
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

// loadingPodsPanel is the bottom pane's skeleton: a placeholder strip line
// (+ rule) over the pods table's real columns (+ header rule) over
// loadingPodRows placeholder rows, plus a placeholder footer — same shape
// podsPanel renders, so the swap to live pods is a fill-in, not a relayout.
func (m Model) loadingPodsPanel(theme tui.Theme, width, height int) string {
	cols := podColumns()

	rows := make([]components.Row, loadingPodRows)
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
		Columns:        cols,
		Rows:           rows,
		Selected:       -1,
		Width:          width,
		Height:         max(height-3, 1),
		HeaderStyle:    lipgloss.NewStyle().Foreground(theme.TextFaint),
		ShowHeaderRule: true,
		RuleStyle:      lipgloss.NewStyle().Foreground(theme.TextGhost2),
	}
	return loadingStripPlaceholder(theme, width) + "\n" + t.Render() + "\n" + loadingPodsFooterLine(theme, width)
}

// loadingBody is 11b's applied 15a: the shell (breadcrumb, facts-panel
// titles/pods columns, keybar) paints instantly, skeleton content fills the
// facts panel and pods table while load() is in flight — never a bare
// spinner-only blank screen (docs/design README.md §15a).
func (m Model) loadingBody(width, height int) string {
	theme := m.Theme()
	left := loadingConditionsBlock(theme)
	right := loadingAllocationBlock(theme)
	topHeight, bottomHeight := panelHeights(height, len(left), len(right))

	top := m.factsPanel(left, right, width, topHeight)
	bottom := m.loadingPodsPanel(theme, width, bottomHeight)
	return top + "\n" + podStripRule(theme, width) + "\n" + bottom
}
