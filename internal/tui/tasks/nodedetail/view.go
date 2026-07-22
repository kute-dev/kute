package nodedetail

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"
	corev1 "k8s.io/api/core/v1"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/resources"
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
		UpdateChip:  tui.BuildUpdateChip(theme, m.session),
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
// ALLOCATED/ALLOCATABLE + TAINTS) and bottom pods table, with a rule
// dividing the two — Kute Spec.dc.html §11b's facts-panel grid carries its
// own border-bottom separating it from the strip below. panelHeights
// already reserves exactly this one line (its "-1"), so this doesn't grow
// the body past height.
func (m Model) readyBody(width, height int) string {
	theme := m.Theme()
	left := m.conditionsBlock(theme)
	right := m.allocationBlock(theme)
	topHeight, bottomHeight := panelHeights(height, len(left), len(right))

	top := m.factsPanel(left, right, width, topHeight)
	bottom := m.podsPanel(width, bottomHeight)
	return top + "\n" + podStripRule(theme, width) + "\n" + bottom
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
	// -5: podsPanel's own health-strip line + its rule (2 lines) and the
	// table's FooterLine (1 line) sit outside the table's own Height budget
	// (bottomHeight-3, see podsPanel), which itself reserves 1 line for the
	// column header and 1 more for ShowHeaderRule's divider.
	return max(bottomHeight-5, 1)
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
	secondary := lipgloss.NewStyle().Foreground(theme.TextSecondary)
	dim := lipgloss.NewStyle().Foreground(theme.TextDim)
	faint := lipgloss.NewStyle().Foreground(theme.TextGhost)

	lines := make([]string, 0, len(m.node.Status.Conditions))
	for _, c := range m.node.Status.Conditions {
		active := c.Status == corev1.ConditionTrue
		label := string(c.Type)
		switch {
		case c.Type == corev1.NodeReady && active:
			// Kute Spec.dc.html §11b: the healthy line is glyph + label only
			// — no "true" word — and the label itself stays dim (only the
			// glyph carries the green), the same "healthy state renders dim,
			// not green" carve-out 11a's own STATUS/ROLLOUT columns use.
			lines = append(lines, good.Render(tui.GlyphRunning)+" "+secondary.Render(label))
		case c.Type == corev1.NodeReady:
			// docs/design README.md §11a: NotReady renders red on the nodes
			// list (the identical signal) — this detail screen previously
			// diverged with yellow for the same condition.
			lines = append(lines, bad.Render(tui.GlyphFailed+" "+label+" false"))
		case active:
			// docs/design README.md §11b: "active pressure yellow with
			// kubelet message + age" — the message clause is only appended
			// when non-empty, so a condition with no kubelet message doesn't
			// leave a dangling "— " with nothing after it. Kute Spec.dc.html
			// §11b: no "true" word, and the message/age trail dims separately
			// from the yellow glyph+label.
			line := warn.Render(tui.GlyphPending) + " " + warn.Render(label)
			if c.Message != "" {
				line += dim.Render(" — " + c.Message)
			}
			line += dim.Render(" · " + shortAge(time.Since(c.LastTransitionTime.Time)))
			lines = append(lines, line)
		default:
			// Kute Spec.dc.html §11b: glyph green (no pressure = healthy),
			// label dim, and "false" itself a shade dimmer still than the
			// label.
			lines = append(lines, good.Render(tui.GlyphRunning)+" "+secondary.Render(label)+" "+faint.Render("false"))
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

// podColumns builds 11b's bottom-pane column spec by reusing browse's own
// Pods-list machinery (resources.Columns over the Pod descriptor, then the
// same "Restarts" → ↺ relabel browse's browseColumns applies), with NODE
// swapped for NAMESPACE since every row here is already scoped to this
// node — the one deliberate divergence from a literal copy of 2a's table.
func podColumns() []components.Column {
	desc, _ := resources.DefaultRegistry().Descriptor(kube.KindPod)
	titles := make([]string, len(desc.Columns))
	for i, title := range desc.Columns {
		if title == "Node" {
			title = "Namespace"
		}
		titles[i] = title
	}
	desc.Columns = titles

	cols := resources.Columns(desc)
	for i := range cols {
		switch cols[i].Title {
		case "Restarts":
			cols[i].Title = tui.GlyphRestarts
		case "Namespace":
			// resources.fixedWidths has no "Namespace" entry (Forwards' own
			// Namespace column relies on its plain len+2 fallback) — 12 is
			// this screen's own previous hardcoded width, kept local so it
			// doesn't change Forwards' column width too.
			cols[i].Min = 12
		}
	}
	return cols
}

// podRowStyles is 11b's per-cell palette for the pods table — the same
// per-status/per-column colors browse's own newRowCellStyles resolves for
// its Pods list, duplicated locally per the repo's package-local-seam
// convention (no muted/marked variants: neither applies to this screen).
type podRowStyles struct {
	name, nameBad, ready, restartsZero, restartsHot, dim lipgloss.Style
	bars                                                 components.BarStyles
	status                                               map[resources.StatusClass]lipgloss.Style
}

func newPodRowStyles(theme tui.Theme, selected bool) podRowStyles {
	style := func(fg lipgloss.Color) lipgloss.Style {
		s := lipgloss.NewStyle().Foreground(fg)
		if selected {
			s = s.Background(theme.SelBg)
		}
		return s
	}
	name := style(theme.TextPrimary)
	if selected {
		name = style(theme.Text) // the one cell that brightens on selection, mirroring browse
	}
	nameBad := style(theme.BadText)
	if selected {
		nameBad = name // selection brightening wins over the crashloop tint, same as browse
	}
	warn := style(theme.Warn)
	return podRowStyles{
		name:         name,
		nameBad:      nameBad,
		ready:        style(theme.TextSecondary),
		restartsZero: style(theme.TextDim),
		restartsHot:  warn,
		dim:          style(theme.TextDim),
		bars: components.BarStyles{
			Track: style(theme.BarTrack),
			Fill:  style(theme.Accent),
			Warn:  warn,
			Bad:   warn, // relative-to-busiest-pod bar, same reasoning as browse's metricCell
		},
		status: map[resources.StatusClass]lipgloss.Style{
			resources.StatusOK:      style(theme.Good),
			resources.StatusWarn:    style(theme.Warn),
			resources.StatusFail:    style(theme.Bad),
			resources.StatusNeutral: style(theme.Info),
		},
	}
}

// podDefaultGlyph/podGlyphColor mirror browse's defaultGlyphFor/glyphColor
// (StatusClass → glyph/color), duplicated locally per the repo's
// package-local-seam convention — used by the health strip's per-class
// segments (row-level glyphs come from resources.Row.Glyph directly, since
// projectPod always sets one for Pods).
func podDefaultGlyph(class resources.StatusClass) string {
	switch class {
	case resources.StatusOK:
		return tui.GlyphRunning
	case resources.StatusWarn:
		return tui.GlyphPending
	case resources.StatusFail:
		return tui.GlyphFailed
	default:
		return tui.GlyphCompleted
	}
}

func podGlyphColor(theme tui.Theme, class resources.StatusClass) lipgloss.Color {
	switch class {
	case resources.StatusOK:
		return theme.Good
	case resources.StatusWarn:
		return theme.Warn
	case resources.StatusFail:
		return theme.Bad
	default:
		return theme.Info
	}
}

// podMetricsMax finds the busiest CPU/MEM usage across every pod on this
// node (m.allPods, not the filtered m.pods, so the bar scale doesn't jump
// around while filtering) — the bar denominator podMetricCell uses, mirror
// of browse's own metricsMax.
func (m Model) podMetricsMax() (cpuMax, memMax int64) {
	for _, r := range m.allPods {
		cpuMax = max(cpuMax, r.pod.CPUMilli)
		memMax = max(memMax, r.pod.MEMBytes)
	}
	return cpuMax, memMax
}

// podMetricCell renders one row's CPU/MEM cell: a MiniBar scaled to the
// busiest pod on this node, then the compact usage value in dim — same
// bar-width math as browse's own metricCell (resources.MetricColumnWidth).
func podMetricCell(cpu bool, pod kube.Pod, maxVal int64, st podRowStyles) components.Cell {
	const barWidth = 6
	valWidth := resources.MetricColumnWidth - barWidth - 1

	value, used := pod.MEM, pod.MEMBytes
	if cpu {
		value, used = pod.CPU, pod.CPUMilli
	}
	if value == "" || value == "n/a" {
		value = "–"
	}
	valText := st.dim.Render(" " + components.Truncate(value, valWidth))
	return components.Cell{Text: components.MiniBar(used, maxVal, barWidth, st.bars) + valText}
}

// podHealthStripLine renders the bottom pane's own per-status glyph+count
// summary, directly above the pods table — the same "● N running · ◐ M
// pending · ✕ K crashloop" shape browse's 2a health strip uses.
// Descriptor.Health/HealthLabel are reused as-is (already wired for Pod in
// the registry); only the glyph/color mapping and line layout are
// duplicated locally, mirroring browse's healthStripLine/defaultGlyphFor/
// glyphColor (the marks/grouping/CRD/node-count branches there don't apply
// here). Tallies m.allPods, the pre-filter population — filterStripLine
// already shows matched/total separately, the same split browse's own
// filter strip vs. health strip has.
func (m Model) podHealthStripLine(theme tui.Theme, width int) string {
	desc, _ := resources.DefaultRegistry().Descriptor(kube.KindPod)
	rows := make([]resources.Row, len(m.allPods))
	for i, r := range m.allPods {
		rows[i] = r.row
	}
	counts := desc.Health(rows)

	numStyle := lipgloss.NewStyle().Foreground(theme.TextPrimary)
	labelStyle := lipgloss.NewStyle().Foreground(theme.TextDim)
	segments := []struct {
		class resources.StatusClass
		n     int
	}{
		{resources.StatusOK, counts.OK},
		{resources.StatusWarn, counts.Warn},
		{resources.StatusFail, counts.Fail},
		{resources.StatusNeutral, counts.Neutral},
	}
	var parts []string
	for _, seg := range segments {
		if seg.n == 0 {
			continue
		}
		glyphStyle := lipgloss.NewStyle().Foreground(podGlyphColor(theme, seg.class))
		parts = append(parts, glyphStyle.Render(podDefaultGlyph(seg.class))+" "+
			numStyle.Render(fmt.Sprintf("%d", seg.n))+" "+labelStyle.Render(desc.HealthLabel(seg.class)))
	}
	left := strings.Join(parts, "   ")
	right := labelStyle.Render(fmt.Sprintf("%d pods", len(rows)))
	return insetStripLine(padBetween(left, right, stripInnerWidth(width)), width)
}

// podStripRule draws a full-width horizontal rule — the same "border-bottom"
// treatment Frame's own Strips get (internal/tui/chrome.go's stripRule/
// rule), duplicated here since these dividers render inside the body
// (readyBody's top/bottom-panel divider, and podsPanel's own divider under
// the health-strip line) rather than through Frame's Strips band.
func podStripRule(theme tui.Theme, width int) string {
	return lipgloss.NewStyle().Foreground(theme.TextGhost2).Render(strings.Repeat("─", width))
}

// podsPanel renders 11b's bottom half: a health-strip summary line (with its
// own rule underneath, mirroring 2a's own strip/table treatment) over the
// node's pods table — reusing browse's own Pods-list widget (columns,
// per-status coloring, live CPU/MEM mini-bars, restart/crashloop tinting,
// the rule between the column headers and the data rows, and the same
// "1–N of M" + scrollbar FooterLine 2a's own table footer uses) instead of
// this screen's old bespoke glyph/Name/Namespace/MEM/CPU/Age table. Rows
// are already sorted unhealthy-first then name by load(), same as 2a.
func (m Model) podsPanel(width, height int) string {
	theme := m.Theme()
	strip := m.podHealthStripLine(theme, width) + "\n" + podStripRule(theme, width)

	cols := podColumns()
	cpuMax, memMax := m.podMetricsMax()
	styles := [2]podRowStyles{newPodRowStyles(theme, false), newPodRowStyles(theme, true)}

	rows := make([]components.Row, 0, len(m.pods))
	for i, p := range m.pods {
		st := styles[0]
		if i == m.selected {
			st = styles[1]
		}
		r := p.row
		cells := resources.Cells(r, width, nil)
		for c := range cells {
			switch {
			case c == 0: // status glyph column
				cells[c].Style = st.status[r.Status]
			case cols[c].Title == "Name":
				base := st.name
				if r.Status == resources.StatusFail {
					base = st.nameBad
				}
				cells[c].Style = base
			case cols[c].Title == "Ready":
				cells[c].Style = st.ready
			case cols[c].Title == "Status":
				cells[c].Style = st.status[r.Status]
			case cols[c].Title == tui.GlyphRestarts:
				cells[c].Style = st.restartsHot
				if cells[c].Text == "0" {
					cells[c].Style = st.restartsZero
				}
			case cols[c].Title == "CPU":
				cells[c] = podMetricCell(true, p.pod, cpuMax, st)
			case cols[c].Title == "MEM":
				cells[c] = podMetricCell(false, p.pod, memMax, st)
			case cols[c].Title == "Namespace":
				cells[c] = components.Cell{Text: r.Namespace, Style: st.dim}
			default: // Age
				cells[c].Style = st.dim
			}
		}
		rows = append(rows, components.Row{Cells: cells})
	}

	t := components.Table{
		Columns:        cols,
		Rows:           rows,
		Selected:       m.selected,
		Offset:         m.offset,
		Width:          width,
		Height:         max(height-3, 1),
		HeaderStyle:    lipgloss.NewStyle().Foreground(theme.TextFaint),
		SelBarStyle:    lipgloss.NewStyle().Foreground(theme.Accent).Background(theme.SelBg),
		SelRowStyle:    lipgloss.NewStyle().Background(theme.SelBg),
		FooterStyle:    lipgloss.NewStyle().Foreground(theme.TextGhost),
		ShowHeaderRule: true,
		RuleStyle:      lipgloss.NewStyle().Foreground(theme.TextGhost2),
	}
	return strip + "\n" + t.Render() + "\n" + t.FooterLine(width)
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
