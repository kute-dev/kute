package overview

import (
	"fmt"
	"image/color"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/resources"
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/components"
)

// maxPanelRows caps how many rows NODES/TROUBLE/CHANGES ever render before
// folding the remainder into a trailing "+N" note — the same "don't let one
// screen's viewport thrash" reasoning tui/goto.go's maxGotoVisible already
// documents for the jump palette.
const maxPanelRows = 6

func (m Model) View() tea.View { return tea.NewView(m.Render()) }

func (m Model) Render() string { return tui.Frame(m.width, m.height, m) }

func (m Model) Theme() tui.Theme {
	if m.session != nil {
		return m.session.Theme
	}
	return tui.Dark()
}

// Header is 19a's breadcrumb — cluster-scoped like Nodes/CRDs/WhoCan (11a/
// 14b/22a): the namespace segment drops entirely, replaced by the same
// "cluster-scoped" tag.
func (m Model) Header() tui.HeaderState {
	theme := m.Theme()
	accent := lipgloss.NewStyle().Foreground(theme.Accent).Bold(true)
	dim := lipgloss.NewStyle().Foreground(theme.TextDim)
	faint := lipgloss.NewStyle().Foreground(theme.TextFaint)
	ghost := lipgloss.NewStyle().Foreground(theme.TextGhost)
	text := lipgloss.NewStyle().Foreground(theme.Text).Bold(true)

	ctxName := "cluster unavailable"
	if m.session != nil && m.session.Location.Context != "" {
		ctxName = m.session.Location.Context
	}

	crumbs := []tui.Crumb{
		{Text: "kute", Style: accent},
		{Text: " │ ", Style: ghost},
		{Text: ctxName, Style: dim},
		{Text: " › ", Style: ghost},
		{Text: "Cluster Overview", Style: text},
		{Text: "  cluster-scoped", Style: faint},
	}

	var forwardChip tui.ConnBadge
	if m.session != nil {
		forwardChip = tui.BuildForwardChip(theme, m.session.ForwardSummary())
	}
	return tui.HeaderState{
		Crumbs:      crumbs,
		UpdateChip:  tui.BuildUpdateChip(theme, m.session),
		ForwardChip: forwardChip,
		Conn:        tui.LiveConnBadge(theme, m.conn, tui.GlyphRunning+" connected"),
	}
}

// Strips is 19a's "top-line trouble counts + v1.30.2 · 5 nodes · 125 pods ·
// 6 namespaces".
func (m Model) Strips(width int) []string {
	if m.state != tui.TaskStateReady {
		return nil
	}
	theme := m.Theme()
	left := m.troubleSummary(theme)
	right := lipgloss.NewStyle().Foreground(theme.TextDim).Render(m.clusterSummaryText())
	return []string{insetStripLine(padBetween(left, right, stripInnerWidth(width)), width)}
}

func (m Model) clusterSummaryText() string {
	version := m.version
	if version == "" {
		version = "–"
	}
	return fmt.Sprintf("%s · %d nodes · %d pods · %d namespaces", version, m.nodeCount, m.podCount, m.nsCount)
}

// troubleSummary composes the strip's left side: fail/warn/cordoned counts
// aggregated across NODES and TROUBLE, or a green all-clear line — the same
// glyph/color-per-StatusClass mapping tasks/browse's own health strip uses
// (glyphColor's Fail=Bad/Warn=Warn/Neutral=Info duplicated locally, per the
// repo's package-local-seam convention).
func (m Model) troubleSummary(theme tui.Theme) string {
	fail, warn, cordoned := 0, 0, 0
	for _, r := range m.nodeTrouble {
		switch {
		case r.Cordoned:
			cordoned++
		case r.Status == resources.StatusFail:
			fail++
		case r.Status == resources.StatusWarn:
			warn++
		}
	}
	for _, r := range m.podTrouble {
		switch r.Status {
		case resources.StatusFail:
			fail++
		case resources.StatusWarn:
			warn++
		}
	}
	if fail == 0 && warn == 0 && cordoned == 0 {
		good := lipgloss.NewStyle().Foreground(theme.Good)
		return good.Render(fmt.Sprintf("%s nothing unhealthy · %d pods running", tui.GlyphRunning, m.podHealthy))
	}
	var parts []string
	if fail > 0 {
		parts = append(parts, lipgloss.NewStyle().Foreground(theme.Bad).Render(fmt.Sprintf("%s %d failing", tui.GlyphFailed, fail)))
	}
	if warn > 0 {
		parts = append(parts, lipgloss.NewStyle().Foreground(theme.Warn).Render(fmt.Sprintf("%s %d pending", tui.GlyphPending, warn)))
	}
	if cordoned > 0 {
		parts = append(parts, lipgloss.NewStyle().Foreground(theme.Info).Render(fmt.Sprintf("%s %d cordoned", tui.GlyphCordoned, cordoned)))
	}
	return strings.Join(parts, "   ")
}

func stripInnerWidth(width int) int {
	return max(width-2*tui.FrameInset, 0)
}

func insetStripLine(line string, width int) string {
	return components.Pad(strings.Repeat(" ", tui.FrameInset)+line, width)
}

func padBetween(left, right string, width int) string {
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return left
	}
	return left + strings.Repeat(" ", gap) + right
}

func (m Model) Body(width, height int) string {
	switch m.state {
	case tui.TaskStateReady:
		return m.readyBody(width, height)
	case tui.TaskStateLoading:
		style := lipgloss.NewStyle().Foreground(m.Theme().Accent)
		return components.LoadingBody(m.spinner, style, m.feedback, width, height)
	default:
		return components.CenterLines([]string{m.feedback}, width, height)
	}
}

// readyBody lays out 19a's two-column body: CAPACITY+NODES on the left,
// TROUBLE+RECENT CHANGES on the right — the same side-by-side line-zip
// tasks/nodedetail's factsPanel already establishes for 11b's top facts
// panel (duplicated locally per the repo's package-local-seam convention).
func (m Model) readyBody(width, height int) string {
	theme := m.Theme()
	leftWidth := width / 2
	rightWidth := width - leftWidth - 2

	var left []string
	left = append(left, m.capacityLines(theme)...)
	left = append(left, "")
	left = append(left, m.nodesLines(theme, leftWidth)...)

	var right []string
	right = append(right, m.troubleLines(theme, rightWidth)...)
	right = append(right, "")
	right = append(right, m.changesLines(theme, rightWidth)...)

	lineCount := max(len(left), len(right))
	lineCount = min(lineCount, height)
	lines := make([]string, lineCount)
	for i := range lineCount {
		l, r := "", ""
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		lines[i] = components.Pad(l, leftWidth) + "  " + components.Pad(r, rightWidth)
	}
	return strings.Join(lines, "\n")
}

func sectionTitle(theme tui.Theme, text string) string {
	return lipgloss.NewStyle().Foreground(theme.TextFaint).Bold(true).Render(text)
}

// capacityLines renders CAPACITY's cpu/mem/pods bars — cluster-wide totals,
// the same allocationBarLine idiom tasks/nodedetail's 11b ALLOCATED/
// ALLOCATABLE block already uses (duplicated locally), except cpu/mem are
// simply omitted (not zeroed — a flatlined bar would misreport) when no
// metrics-server poll has ever landed, mirroring tasks/browse's own
// nodeSummaryText degrade-gracefully rule.
func (m Model) capacityLines(theme tui.Theme) []string {
	lines := []string{sectionTitle(theme, "CAPACITY")}
	if m.metricsAvailable {
		lines = append(lines,
			allocationBarLine("cpu", m.capCPUUsed, m.capCPUTotal, theme, func(v int64) string { return fmt.Sprintf("%dm", v) }),
			allocationBarLine("mem", m.capMemUsed, m.capMemTotal, theme, formatBytes),
		)
	} else {
		lines = append(lines, lipgloss.NewStyle().Foreground(theme.TextDim).Render("cpu / mem — no metrics-server installed"))
	}
	if m.capPodsTotal > 0 {
		lines = append(lines, allocationBarLine("pods", m.capPodsUsed, m.capPodsTotal, theme, func(v int64) string { return fmt.Sprintf("%d", v) }))
	} else {
		// No node reports an Allocatable pods capacity (fake/demo fixtures,
		// or a real node whose kubelet hasn't reported yet) — a "N / 0" bar
		// would misreport, so fall back to a bare count, the same
		// degrade-gracefully rule browse/nodes.go's nodePodsCell already
		// applies per-row.
		dim := lipgloss.NewStyle().Foreground(theme.TextDim)
		lines = append(lines, dim.Render(fmt.Sprintf("pods %d", m.capPodsUsed)))
	}
	return lines
}

func allocationBarLine(label string, used, total int64, theme tui.Theme, format func(int64) string) string {
	const barWidth = 12
	dim := lipgloss.NewStyle().Foreground(theme.TextDim)
	bars := components.BarStyles{
		Track: lipgloss.NewStyle().Foreground(theme.BarTrack),
		Fill:  lipgloss.NewStyle().Foreground(theme.Accent),
		Warn:  lipgloss.NewStyle().Foreground(theme.Warn),
		Bad:   lipgloss.NewStyle().Foreground(theme.Bad),
	}
	bar := components.MiniBar(used, total, barWidth, bars)
	return dim.Render(fmt.Sprintf("%-4s ", label)) + bar + " " + dim.Render(format(used)+" / "+format(total))
}

func formatBytes(v int64) string {
	const gi = 1024 * 1024 * 1024
	const mi = 1024 * 1024
	switch {
	case v >= gi:
		return fmt.Sprintf("%.1fGi", float64(v)/gi)
	case v >= mi:
		return fmt.Sprintf("%.0fMi", float64(v)/mi)
	default:
		return fmt.Sprintf("%dB", v)
	}
}

// nodesLines renders NODES: pressure/cordoned rows first (already sorted by
// load's sortTrouble), a "+N ready" trailer for the folded healthy
// remainder, or one green all-clear line when nothing's wrong.
func (m Model) nodesLines(theme tui.Theme, width int) []string {
	lines := []string{sectionTitle(theme, "NODES")}
	if len(m.nodeTrouble) == 0 {
		good := lipgloss.NewStyle().Foreground(theme.Good)
		lines = append(lines, good.Render(fmt.Sprintf("%s %d nodes ready", tui.GlyphRunning, m.nodeHealthy)))
		return lines
	}

	shown := m.nodeTrouble
	extra := 0
	if len(shown) > maxPanelRows {
		extra = len(shown) - maxPanelRows
		shown = shown[:maxPanelRows]
	}
	rows := make([]components.Row, len(shown))
	for i, row := range shown {
		rows[i] = m.nodeRow(theme, row)
	}
	t := components.Table{
		Columns:     []components.Column{{Min: 2}, {Min: 10, Flex: true}, {Min: 12}},
		Rows:        rows,
		Selected:    m.focusedIndex(panelNodes, m.nodesSel),
		Width:       width,
		Height:      len(rows) + 1,
		HeaderStyle: lipgloss.NewStyle(),
		SelBarStyle: lipgloss.NewStyle().Foreground(theme.Accent).Background(theme.SelBg),
		SelRowStyle: lipgloss.NewStyle().Background(theme.SelBg),
	}
	lines = append(lines, strings.Split(t.Render(), "\n")[1:]...)
	if extra+m.nodeHealthy > 0 {
		lines = append(lines, dimFoldLine(theme, extra+m.nodeHealthy, "ready"))
	}
	return lines
}

func (m Model) nodeRow(theme tui.Theme, row resources.Row) components.Row {
	glyphStyle := lipgloss.NewStyle().Foreground(glyphTone(theme, row))
	name := lipgloss.NewStyle().Foreground(theme.TextPrimary)
	status := lipgloss.NewStyle().Foreground(theme.TextDim)
	statusText := row.Name
	if len(row.Cells) > 1 {
		statusText = row.Cells[1]
	}
	return components.Row{Cells: []components.Cell{
		{Text: row.Glyph, Style: glyphStyle},
		{Text: row.Name, Style: name},
		{Text: statusText, Style: status},
	}}
}

func glyphTone(theme tui.Theme, row resources.Row) color.Color {
	switch {
	case row.Cordoned:
		return theme.Info
	case row.Status == resources.StatusFail:
		return theme.Bad
	case row.Status == resources.StatusWarn:
		return theme.Warn
	default:
		return theme.Good
	}
}

func dimFoldLine(theme tui.Theme, n int, word string) string {
	return lipgloss.NewStyle().Foreground(theme.TextGhost).Render(fmt.Sprintf("+ %d %s", n, word))
}

// troubleLines renders TROUBLE: cluster-wide unhealthy pods, or one green
// all-clear line (docs/design README.md §19a: "empty = 'nothing unhealthy ·
// 125 pods running' in green").
func (m Model) troubleLines(theme tui.Theme, width int) []string {
	lines := []string{sectionTitle(theme, "TROUBLE")}
	if len(m.podTrouble) == 0 {
		good := lipgloss.NewStyle().Foreground(theme.Good)
		lines = append(lines, good.Render(fmt.Sprintf("nothing unhealthy · %d pods running", m.podHealthy)))
		return lines
	}

	shown := m.podTrouble
	extra := 0
	if len(shown) > maxPanelRows {
		extra = len(shown) - maxPanelRows
		shown = shown[:maxPanelRows]
	}
	rows := make([]components.Row, len(shown))
	for i, row := range shown {
		rows[i] = m.troubleRow(theme, row)
	}
	t := components.Table{
		Columns:     []components.Column{{Min: 2}, {Min: 10, Flex: true}, {Min: 10}, {Min: 14}},
		Rows:        rows,
		Selected:    m.focusedIndex(panelTrouble, m.troubleSel),
		Width:       width,
		Height:      len(rows) + 1,
		HeaderStyle: lipgloss.NewStyle(),
		SelBarStyle: lipgloss.NewStyle().Foreground(theme.Accent).Background(theme.SelBg),
		SelRowStyle: lipgloss.NewStyle().Background(theme.SelBg),
	}
	lines = append(lines, strings.Split(t.Render(), "\n")[1:]...)
	if extra > 0 {
		lines = append(lines, dimFoldLine(theme, extra, "more unhealthy"))
	}
	return lines
}

func (m Model) troubleRow(theme tui.Theme, row resources.Row) components.Row {
	glyphStyle := lipgloss.NewStyle().Foreground(glyphTone(theme, row))
	name := lipgloss.NewStyle().Foreground(theme.BadText)
	ns := lipgloss.NewStyle().Foreground(theme.TextDim)
	status := lipgloss.NewStyle().Foreground(theme.TextSecondary)
	statusText := row.Name
	if len(row.Cells) > 2 {
		statusText = row.Cells[2]
	}
	return components.Row{Cells: []components.Cell{
		{Text: row.Glyph, Style: glyphStyle},
		{Text: row.Name, Style: name},
		{Text: row.Namespace, Style: ns},
		{Text: statusText, Style: status},
	}}
}

// changesLines renders RECENT CHANGES: the ReplicaSet-derived rollout feed
// (load.go), fixed to the last 30m, cluster-wide.
func (m Model) changesLines(theme tui.Theme, width int) []string {
	lines := []string{sectionTitle(theme, "RECENT CHANGES")}
	if len(m.changes) == 0 {
		lines = append(lines, lipgloss.NewStyle().Foreground(theme.TextDim).Render("no changes in the last 30m"))
		return lines
	}

	shown := m.changes
	extra := 0
	if len(shown) > maxPanelRows {
		extra = len(shown) - maxPanelRows
		shown = shown[:maxPanelRows]
	}
	rows := make([]components.Row, len(shown))
	for i, e := range shown {
		rows[i] = m.changeRow(theme, e)
	}
	t := components.Table{
		Columns:     []components.Column{{Min: 5}, {Min: 2}, {Min: 10, Flex: true}, {Min: 16}},
		Rows:        rows,
		Selected:    m.focusedIndex(panelChanges, m.changesSel),
		Width:       width,
		Height:      len(rows) + 1,
		HeaderStyle: lipgloss.NewStyle(),
		SelBarStyle: lipgloss.NewStyle().Foreground(theme.Accent).Background(theme.SelBg),
		SelRowStyle: lipgloss.NewStyle().Background(theme.SelBg),
	}
	lines = append(lines, strings.Split(t.Render(), "\n")[1:]...)
	if extra > 0 {
		lines = append(lines, dimFoldLine(theme, extra, "more changes"))
	}
	return lines
}

func (m Model) changeRow(theme tui.Theme, e kube.TimelineEntry) components.Row {
	rollout := lipgloss.NewStyle().Foreground(theme.Accent)
	dim := lipgloss.NewStyle().Foreground(theme.TextDim)
	object := lipgloss.NewStyle().Foreground(theme.TextPrimary)
	msg := lipgloss.NewStyle().Foreground(theme.TextSecondary)
	return components.Row{Cells: []components.Cell{
		{Text: e.Time.Format("15:04"), Style: dim},
		{Text: tui.GlyphRollout, Style: rollout},
		{Text: e.Object, Style: object},
		{Text: e.Message, Style: msg},
	}}
}

// focusedIndex is the Table.Selected value for panel p: the real cursor
// when p is focused, or an out-of-range value (-1) otherwise — Table never
// highlights a row whose index doesn't match Selected, so only the focused
// panel's row ever renders the selection bar/background.
func (m Model) focusedIndex(p panel, sel int) int {
	if m.focus != p {
		return -1
	}
	return sel
}
