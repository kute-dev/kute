package nodedetail

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"
	corev1 "k8s.io/api/core/v1"

	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/components"
)

func (m Model) View() tea.View { return tea.NewView(m.Render()) }

func (m Model) Render() string { return tui.Frame(m.width, m.height, m) }

func (m Model) Theme() tui.Theme {
	if m.session != nil {
		return m.session.Theme
	}
	return tui.Dark()
}

func (m Model) Header() tui.HeaderState {
	theme := m.Theme()
	accent := lipgloss.NewStyle().Foreground(theme.Accent).Bold(true)
	dim := lipgloss.NewStyle().Foreground(theme.TextDim)
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
		{Text: "Nodes", Style: dim},
		{Text: " › ", Style: ghost},
		{Text: m.nodeName, Style: text},
	}

	if m.state == tui.TaskStateLoading {
		// 15a's loading-header treatment applied to a detail screen: a
		// counting timer instead of the usual conn/forward badges (see
		// browse.Model.Header's equivalent branch).
		elapsed := max(m.now.Sub(m.loadStartedAt), 0)
		return tui.HeaderState{
			Crumbs: crumbs,
			Conn: tui.ConnBadge{
				Text:  fmt.Sprintf("%s loading %s · %.1fs", tui.GlyphPending, m.nodeName, elapsed.Seconds()),
				Style: lipgloss.NewStyle().Foreground(theme.Warn),
			},
		}
	}

	return tui.HeaderState{
		Crumbs:      crumbs,
		ForwardChip: tui.BuildForwardChip(theme, m.session.ForwardSummary()),
		Conn:        tui.LiveConnBadge(theme, m.conn, tui.GlyphRunning+" connected"),
	}
}

// stripLineCount is how many Strips lines the current state renders — kept
// in sync with Strips itself so tableDataRows budgets the pods viewport
// correctly (mirrors browse's own stripLineCount/Strips split).
func (m Model) stripLineCount() int {
	switch {
	case m.state == tui.TaskStateReady && m.filterActive, m.state == tui.TaskStateLoading:
		return 1
	default:
		return 0
	}
}

func (m Model) Strips(width int) []string {
	switch {
	case m.state == tui.TaskStateReady && m.filterActive:
		return []string{m.filterStripLine(m.Theme(), width)}
	case m.state == tui.TaskStateLoading:
		return []string{m.loadingStripLine(m.Theme(), width)}
	default:
		return nil
	}
}

// filterStripLine renders the live "/" query plus a matched/total count for
// the pods list, and — when rows are hidden — the same "N hidden by filter
// — esc to clear" notice browse's own filterStripLine shows (docs/design
// system-wide interactions: "items never silently disappear").
func (m Model) filterStripLine(theme tui.Theme, width int) string {
	accent := lipgloss.NewStyle().Foreground(theme.Accent)
	text := lipgloss.NewStyle().Foreground(theme.Text)
	faint := lipgloss.NewStyle().Foreground(theme.TextFaint)
	dim := lipgloss.NewStyle().Foreground(theme.TextDim)

	left := accent.Render("/ ") + text.Render(m.filterQuery) + accent.Render(tui.GlyphSelBar)

	total, matched := len(m.allPods), len(m.pods)
	right := dim.Render(fmt.Sprintf("%d/%d pods", matched, total))
	if matched < total {
		right = faint.Render(fmt.Sprintf("%d hidden by filter — esc to clear   ", total-matched)) + right
	}
	return insetStripLine(padBetween(left, right, stripInnerWidth(width)), width)
}

// stripInnerWidth/insetStripLine give the filter strip the same
// tui.FrameInset horizontal inset as the Frame's chrome bands, duplicated
// per the repo's package-local-seam convention (browse's own copies).
func stripInnerWidth(width int) int {
	return max(width-2*tui.FrameInset, 0)
}

func insetStripLine(line string, width int) string {
	return components.Pad(strings.Repeat(" ", tui.FrameInset)+line, width)
}

// padBetween places left-aligned left and right-aligned right within width,
// measuring already-styled (ANSI-containing) strings via lipgloss.Width.
// When there isn't room for both, right is dropped.
func padBetween(left, right string, width int) string {
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return left
	}
	return left + strings.Repeat(" ", gap) + right
}

func (m Model) Body(width, height int) string {
	if m.actions.Active() {
		return m.confirmBody(width, height)
	}
	switch m.state {
	case tui.TaskStateReady:
		return m.readyBody(width, height)
	case tui.TaskStateLoading:
		return m.loadingBody(width, height)
	default:
		return components.CenterLines([]string{m.feedback}, width, height)
	}
}

// readyBody splits the body into 11b's top facts panel (CONDITIONS │
// ALLOCATED/ALLOCATABLE + TAINTS) and bottom pods table.
func (m Model) readyBody(width, height int) string {
	theme := m.Theme()
	left := m.conditionsBlock(theme)
	right := m.allocationBlock(theme)
	topHeight, bottomHeight := panelHeights(height, len(left), len(right))

	top := m.factsPanel(left, right, width, topHeight)
	bottom := m.podsPanel(width, bottomHeight)
	return top + "\n" + bottom
}

// panelHeights splits the ready body's height between the top facts panel
// and the bottom pods table — factored out of readyBody so tableDataRows
// (selection.go's scroll-offset clamp) can compute the same split before a
// render happens, without duplicating the formula.
func panelHeights(bodyHeight, leftLines, rightLines int) (top, bottom int) {
	top = min(max(leftLines, rightLines)+1, max(bodyHeight/2, 6))
	bottom = max(bodyHeight-top-1, 3)
	return top, bottom
}

// tableDataRows is how many pod rows the bottom pane can show at once,
// mirroring readyBody/podsPanel's own math so moveSelection's clampOffset
// scrolls against the same viewport the render actually produces.
func (m Model) tableDataRows() int {
	body := tui.FrameBodyHeight(m.height, m.stripLineCount())
	theme := m.Theme()
	left := m.conditionsBlock(theme)
	right := m.allocationBlock(theme)
	_, bottomHeight := panelHeights(body, len(left), len(right))
	return max(bottomHeight-2, 1)
}

// factsPanel lays left/right out side by side, line by line — each block's
// lines are already pre-styled, single-line strings (components.Pad only
// pads a single line correctly, so the two columns can't be joined as
// multi-line blobs).
func (m Model) factsPanel(left, right []string, width, height int) string {
	leftWidth := width / 2
	rightWidth := width - leftWidth - 2

	lines := make([]string, height)
	for i := range height {
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

func (m Model) conditionsBlock(theme tui.Theme) []string {
	title := lipgloss.NewStyle().Foreground(theme.TextFaint).Bold(true).Render("CONDITIONS")
	lines := []string{title}
	lines = append(lines, m.conditionLines()...)
	return lines
}

func (m Model) conditionLines() []string {
	if m.node == nil {
		return nil
	}
	theme := m.Theme()
	good := lipgloss.NewStyle().Foreground(theme.Good)
	warn := lipgloss.NewStyle().Foreground(theme.Warn)
	bad := lipgloss.NewStyle().Foreground(theme.Bad)
	dim := lipgloss.NewStyle().Foreground(theme.TextDim)

	lines := make([]string, 0, len(m.node.Status.Conditions))
	for _, c := range m.node.Status.Conditions {
		active := c.Status == corev1.ConditionTrue
		label := string(c.Type)
		switch {
		case c.Type == corev1.NodeReady && active:
			lines = append(lines, good.Render(label+" true"))
		case c.Type == corev1.NodeReady:
			// docs/design README.md §11a: NotReady renders red on the nodes
			// list (the identical signal) — this detail screen previously
			// diverged with yellow for the same condition.
			lines = append(lines, bad.Render(label+" false"))
		case active:
			// docs/design README.md §11b: "active pressure yellow with
			// kubelet message + age" — the message clause is only appended
			// when non-empty, so a condition with no kubelet message doesn't
			// leave a dangling "— " with nothing after it.
			line := label + " true"
			if c.Message != "" {
				line += " — " + c.Message
			}
			line += " · " + shortAge(time.Since(c.LastTransitionTime.Time))
			lines = append(lines, warn.Render(line))
		default:
			lines = append(lines, dim.Render(label+" false"))
		}
	}
	return lines
}

func (m Model) allocationBlock(theme tui.Theme) []string {
	title := lipgloss.NewStyle().Foreground(theme.TextFaint).Bold(true).Render("ALLOCATED / ALLOCATABLE")
	lines := []string{
		title,
		allocationBarLine("cpu", m.allocated.cpuMilli, m.allocatable.cpuMilli, theme, func(v int64) string { return fmt.Sprintf("%dm", v) }),
		allocationBarLine("mem", m.allocated.memBytes, m.allocatable.memBytes, theme, formatBytes),
		allocationBarLine("pods", int64(len(m.pods)), m.allocatable.pods, theme, func(v int64) string { return fmt.Sprintf("%d", v) }),
		"",
	}
	lines = append(lines, lipgloss.NewStyle().Foreground(theme.TextFaint).Bold(true).Render("TAINTS"))
	lines = append(lines, m.taintLines()...)
	return lines
}

func (m Model) taintLines() []string {
	if m.node == nil || len(m.node.Spec.Taints) == 0 {
		return []string{lipgloss.NewStyle().Foreground(m.Theme().TextDim).Render("none")}
	}
	dim := lipgloss.NewStyle().Foreground(m.Theme().TextSecondary)
	lines := make([]string, 0, len(m.node.Spec.Taints))
	for _, t := range m.node.Spec.Taints {
		text := t.Key
		if t.Value != "" {
			text += "=" + t.Value
		}
		text += ":" + string(t.Effect)
		lines = append(lines, dim.Render(text))
	}
	return lines
}

func allocationBarLine(label string, used, total int64, theme tui.Theme, format func(int64) string) string {
	const barWidth = 12
	dim := lipgloss.NewStyle().Foreground(theme.TextDim)
	warn := lipgloss.NewStyle().Foreground(theme.Warn)
	bars := components.BarStyles{
		Track: lipgloss.NewStyle().Foreground(theme.BarTrack),
		Fill:  lipgloss.NewStyle().Foreground(theme.Accent),
		Warn:  lipgloss.NewStyle().Foreground(theme.Warn),
		Bad:   lipgloss.NewStyle().Foreground(theme.Bad),
	}
	bar := components.MiniBar(used, total, barWidth, bars)
	// docs/design README.md §11b: "used / total text, hot values yellow" —
	// previously only the bar's own fill segment changed color, matching
	// the same 70% "hot" threshold MiniBar's fillStyleFor uses internally.
	textStyle := dim
	if total > 0 && float64(used)/float64(total) >= 0.7 {
		textStyle = warn
	}
	return dim.Render(fmt.Sprintf("%-4s ", label)) + bar + " " + textStyle.Render(format(used)+" / "+format(total))
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

// podsPanel renders the node's pods table — 11b's bottom half, already
// sorted memory-desc by load().
func (m Model) podsPanel(width, height int) string {
	theme := m.Theme()
	cols := []components.Column{
		{Title: "", Min: 1},
		{Title: "Name", Min: 10, Flex: true},
		{Title: "Namespace", Min: 12},
		{Title: "MEM", Min: 8},
		{Title: "CPU", Min: 8},
		{Title: "Age", Min: 4, Align: components.AlignRight},
	}
	rows := make([]components.Row, 0, len(m.pods))
	for i, p := range m.pods {
		nameStyle := lipgloss.NewStyle().Foreground(theme.TextPrimary)
		glyphStyle := lipgloss.NewStyle().Foreground(theme.Good)
		if p.glyphBad {
			glyphStyle = lipgloss.NewStyle().Foreground(theme.Bad)
			nameStyle = lipgloss.NewStyle().Foreground(theme.BadText)
		}
		dim := lipgloss.NewStyle().Foreground(theme.TextDim)
		if i == m.selected {
			nameStyle = nameStyle.Background(theme.SelBg)
			glyphStyle = glyphStyle.Background(theme.SelBg)
			dim = dim.Background(theme.SelBg)
		}
		rows = append(rows, components.Row{Cells: []components.Cell{
			{Text: p.glyph, Style: glyphStyle},
			{Text: p.pod.Name, Style: nameStyle},
			{Text: p.pod.Namespace, Style: dim},
			{Text: p.memText, Style: dim},
			{Text: p.cpuText, Style: dim},
			{Text: p.pod.Age, Style: dim},
		}})
	}

	t := components.Table{
		Columns:     cols,
		Rows:        rows,
		Selected:    m.selected,
		Offset:      m.offset,
		Width:       width,
		Height:      max(height-1, 1),
		HeaderStyle: lipgloss.NewStyle().Foreground(theme.TextFaint),
		SelBarStyle: lipgloss.NewStyle().Foreground(theme.Accent).Background(theme.SelBg),
		SelRowStyle: lipgloss.NewStyle().Background(theme.SelBg),
		FooterStyle: lipgloss.NewStyle().Foreground(theme.TextGhost),
	}
	return t.Render()
}

func (m Model) confirmBody(width, height int) string {
	theme := m.Theme()
	title := "Confirm"
	if pending := m.actions.Pending(); pending != nil {
		title = pending.Label
	}
	styles := components.ConfirmStyles{
		Border: lipgloss.NewStyle().Foreground(theme.ConfirmBorder).Background(theme.ConfirmHeaderBg),
		Title:  lipgloss.NewStyle().Foreground(theme.Text).Bold(true).Background(theme.ConfirmHeaderBg),
		Detail: lipgloss.NewStyle().Foreground(theme.TextSecondary).Background(theme.ConfirmHeaderBg),
		Rule:   lipgloss.NewStyle().Foreground(theme.TextGhost).Background(theme.ConfirmHeaderBg),
		Key:    lipgloss.NewStyle().Foreground(theme.Accent).Background(theme.ConfirmHeaderBg),
		Label:  lipgloss.NewStyle().Foreground(theme.TextDim).Background(theme.ConfirmHeaderBg),
	}
	return components.ConfirmCard(title, "", styles, width, height)
}

// shortAge renders a duration as a compact "12m"/"3h"/"5d" string — the same
// format every other package's own copy of this helper uses (e.g.
// poddetail's, resources/projections.go's).
func shortAge(d time.Duration) string {
	switch {
	case d <= 0:
		return "0s"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
