package poddetail

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"

	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/actions"
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
	}
	if m.namespace != "" {
		// mock 5a: "… › nva-stage › Pods › …" — the namespace segment keeps
		// browse's accent styling.
		crumbs = append(crumbs,
			tui.Crumb{Text: " › ", Style: ghost},
			tui.Crumb{Text: m.namespace, Style: lipgloss.NewStyle().Foreground(theme.Accent)},
		)
	}
	crumbs = append(crumbs,
		tui.Crumb{Text: " › ", Style: ghost},
		tui.Crumb{Text: "Pods", Style: dim},
		tui.Crumb{Text: " › ", Style: ghost},
		tui.Crumb{Text: m.name, Style: text},
	)

	return tui.HeaderState{
		Crumbs:      crumbs,
		UpdateChip:  tui.BuildUpdateChip(theme, m.session),
		ForwardChip: tui.BuildForwardChip(theme, m.session.ForwardSummary()),
		Conn:        tui.LiveConnBadge(theme, m.conn, tui.GlyphRunning+" connected"),
	}
}

func (m Model) Strips(width int) []string { return nil }

func (m Model) Body(width, height int) string {
	if m.actions.Active() && m.actions.Tier() == actions.TierModal {
		return m.deleteConfirmModal(width, height)
	}
	switch m.state {
	case tui.TaskStateReady:
		if m.gone {
			return components.CenterLines([]string{
				lipgloss.NewStyle().Foreground(m.Theme().Bad).Bold(true).Render("Pod deleted"),
				lipgloss.NewStyle().Foreground(m.Theme().TextDim).Render(m.name + " no longer exists · press any key to go back"),
			}, width, height)
		}
		return m.readyBody(width, height)
	case tui.TaskStateLoading:
		style := lipgloss.NewStyle().Foreground(m.Theme().Accent)
		return components.LoadingBody(m.spinner, style, m.feedback, width, height)
	default:
		return components.CenterLines([]string{m.feedback}, width, height)
	}
}

// readyBody stacks 5a's sections top to bottom: title row, an optional
// last-termination banner (promoted first, per docs/design README.md §5a —
// "answers why is it broken first, never bury it"), the meta grid, the
// CONTAINERS grid + CPU/MEM bars, EVENTS, and a right sidebar composited
// alongside the main column.
func (m Model) readyBody(width, height int) string {
	theme := m.Theme()
	// +2 keeps the sidebar's content width at a quarter of the screen after
	// its border gutter ("│ ") is drawn.
	sidebarWidth := max(width/4, 20) + 2
	mainWidth := width - sidebarWidth - 2

	var main []string
	main = append(main, m.titleLine(theme, mainWidth))
	if m.pod.LastTermination != nil {
		main = append(main, "", m.terminationBanner(theme, mainWidth))
	}
	main = append(main, "", m.metaGrid(theme))
	main = append(main, "", m.containersBlock(theme, mainWidth))
	main = append(main, "", m.eventsBlock(theme, mainWidth))

	mainBlock := strings.Join(main, "\n")
	sidebar := m.sidebarBlock(theme)
	return joinColumns(theme, mainBlock, sidebar, mainWidth, sidebarWidth, height)
}

// joinColumns lays main/sidebar out side by side, line by line — pre-styled
// single-line strings only (components.Pad only pads a single line
// correctly; joining multi-line blocks through lipgloss.JoinHorizontal
// silently corrupts the taller column, per nodedetail.factsPanel's doc
// comment on the same shortcut). The sidebar panel extends to the full
// budgeted height (mock 5a: the bordered side panel spans the body), so rows
// past its content get an empty gutter-only sidebar line.
func joinColumns(theme tui.Theme, main, sidebar string, mainWidth, sidebarWidth, height int) string {
	mainLines := strings.Split(main, "\n")
	sideLines := strings.Split(sidebar, "\n")
	n := max(len(mainLines), len(sideLines))
	if height > 0 {
		n = height
	}
	lines := make([]string, n)
	for i := range n {
		l, r := "", sidebarLine(theme, "")
		if i < len(mainLines) {
			l = mainLines[i]
		}
		if i < len(sideLines) {
			r = sideLines[i]
		}
		lines[i] = components.Pad(l, mainWidth) + "  " + components.Pad(r, sidebarWidth)
	}
	return strings.Join(lines, "\n")
}

func (m Model) titleLine(theme tui.Theme, width int) string {
	name := lipgloss.NewStyle().Foreground(theme.Text).Bold(true).Render(m.name)
	glyph, class, text := statusClass(m.pod)
	statusStyle := lipgloss.NewStyle().Foreground(statusColor(theme, class))
	status := statusStyle.Render(glyph + " " + text)
	restarts := ""
	if m.pod.Restarts > 0 {
		restarts = lipgloss.NewStyle().Foreground(theme.Warn).Render(fmt.Sprintf("%s %d restarts", tui.GlyphRestarts, m.pod.Restarts))
	}
	parts := []string{name, status}
	if restarts != "" {
		parts = append(parts, restarts)
	}
	left := strings.Join(parts, "  ")
	// mock 5a: "watching · live" sits at the right of the title row (the
	// header badge carries the real connection state).
	right := lipgloss.NewStyle().Foreground(theme.TextFaint).Render("watching · live")
	return padBetween(left, right, width)
}

// padBetween right-aligns right against width, dropping it when the line is
// too tight — same shape as browse's own padBetween, duplicated per the
// repo's package-local convention.
func padBetween(left, right string, width int) string {
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 || right == "" {
		return left
	}
	return left + strings.Repeat(" ", gap) + right
}

func statusColor(theme tui.Theme, class string) lipgloss.Color {
	switch class {
	case "ok":
		return theme.Good
	case "warn":
		return theme.Warn
	case "fail":
		return theme.Bad
	default:
		return theme.TextDim
	}
}

func (m Model) terminationBanner(theme tui.Theme, width int) string {
	lt := m.pod.LastTermination
	if lt == nil {
		return ""
	}
	title := lipgloss.NewStyle().Foreground(theme.Bad).Bold(true).Render("Last termination")
	facts := lipgloss.NewStyle().Foreground(theme.BadMuted).Render(
		fmt.Sprintf("exit %d · %s · %s ago", lt.ExitCode, lt.Reason, shortDur(lt.Age)),
	)

	// mock 5a's body line names the container and says what happened —
	// the OOMKilled wording only when the limit that was exceeded is known —
	// then the next backoff estimate (docs/design README.md §5a: "the
	// memory limit + next backoff").
	bodyStyle := lipgloss.NewStyle().Foreground(theme.BadText)
	container := lipgloss.NewStyle().Foreground(theme.Warn).Render(lt.Container)
	what := fmt.Sprintf(" exited with code %d.", lt.ExitCode)
	if lt.Reason == "OOMKilled" && m.pod.MEMLimitBytes > 0 {
		what = " exceeded memory limit " + formatBytes(m.pod.MEMLimitBytes) + "."
	}
	what += fmt.Sprintf(" Next backoff ~%s.", shortDur(lt.NextBackoff()))
	body := bodyStyle.Render("Container ") + container + bodyStyle.Render(what)

	content := title + "  " + facts + "\n" + body
	style := lipgloss.NewStyle().
		Background(theme.ErrBannerBg).
		BorderForeground(theme.ErrBannerBorder).
		Border(lipgloss.RoundedBorder()).
		Padding(0, 1).
		Width(width - 2) // + the border's own 2 columns = exactly width
	return style.Render(content)
}

func (m Model) metaGrid(theme tui.Theme) string {
	label := lipgloss.NewStyle().Foreground(theme.TextFaint)
	value := lipgloss.NewStyle().Foreground(theme.TextPrimary)
	link := lipgloss.NewStyle().Foreground(theme.Accent)

	node := m.pod.Node
	if node == "" {
		node = "–"
	}
	ip := m.pod.IP
	if ip == "" {
		ip = "–"
	}
	qos := m.pod.QoSClass
	if qos == "" {
		qos = "–"
	}
	controller := "–"
	controllerStyle := value
	if m.controller != "" {
		controller = m.controller + " ↗"
		controllerStyle = link
	}

	fields := []struct {
		label, value string
		style        lipgloss.Style
	}{
		{"NODE", node, value},
		{"IP", ip, value},
		{"QOS", qos, value},
		{"CONTROLLER", controller, controllerStyle},
		// mock 5a shows "3h" — never kube.Pod.Age's raw Go duration string.
		{"AGE", shortDur(m.pod.AgeDuration), value},
	}
	// mock 5a: label over value, columns aligned across both rows.
	labels := make([]string, len(fields))
	values := make([]string, len(fields))
	for i, f := range fields {
		w := max(len(f.label), lipgloss.Width(f.value))
		labels[i] = label.Render(components.Pad(f.label, w))
		values[i] = f.style.Render(f.value) + strings.Repeat(" ", w-lipgloss.Width(f.value))
	}
	const gap = "    "
	return strings.Join(labels, gap) + "\n" + strings.Join(values, gap)
}

func (m Model) containersBlock(theme tui.Theme, width int) string {
	title := lipgloss.NewStyle().Foreground(theme.TextFaint).Bold(true).Render("CONTAINERS")
	if len(m.pod.ContainerInfos) == 0 {
		return title + "\n" + lipgloss.NewStyle().Foreground(theme.TextDim).Render("none")
	}

	cols := []components.Column{
		{Title: "", Min: 1},
		{Title: "Name", Min: 10, Flex: true},
		{Title: "Image", Min: 12, Flex: true},
		{Title: "State", Min: 20},
		{Title: "Restarts", Min: 8, Align: components.AlignRight},
	}
	rows := make([]components.Row, 0, len(m.pod.ContainerInfos))
	for i, c := range m.pod.ContainerInfos {
		var glyph string
		var glyphStyle lipgloss.Style
		stateStyle := lipgloss.NewStyle().Foreground(theme.Good)
		stateText := c.State
		switch c.State {
		case "Waiting":
			glyph, glyphStyle = "◐", lipgloss.NewStyle().Foreground(theme.Bad)
			stateStyle = lipgloss.NewStyle().Foreground(theme.Bad)
			if c.Reason != "" {
				stateText = "Waiting · " + c.Reason
			}
		case "Terminated":
			glyph, glyphStyle = "○", lipgloss.NewStyle().Foreground(theme.Warn)
			stateStyle = lipgloss.NewStyle().Foreground(theme.Warn)
			if c.Reason != "" {
				stateText = "Terminated · " + c.Reason
			}
		case "Running":
			glyph, glyphStyle = "●", lipgloss.NewStyle().Foreground(theme.Good)
		default:
			glyph, glyphStyle = "◌", lipgloss.NewStyle().Foreground(theme.TextDim)
			stateStyle = lipgloss.NewStyle().Foreground(theme.TextDim)
			stateText = "–"
		}
		nameStyle := lipgloss.NewStyle().Foreground(theme.TextPrimary)
		imgStyle := lipgloss.NewStyle().Foreground(theme.TextDim)
		// mock 5a: restarts render "6 ↺" — yellow once non-zero.
		restartStyle := lipgloss.NewStyle().Foreground(theme.TextDim)
		if c.Restarts > 0 {
			restartStyle = lipgloss.NewStyle().Foreground(theme.Warn)
		}
		// The selection wash only means something when tab has somewhere to
		// go — a single-container pod renders plain (no stray highlight).
		if i == m.selectedContainer && len(m.pod.ContainerInfos) > 1 {
			nameStyle = nameStyle.Background(theme.SelBg)
			imgStyle = imgStyle.Background(theme.SelBg)
			stateStyle = stateStyle.Background(theme.SelBg)
			restartStyle = restartStyle.Background(theme.SelBg)
			glyphStyle = glyphStyle.Background(theme.SelBg)
		}
		rows = append(rows, components.Row{Cells: []components.Cell{
			{Text: glyph, Style: glyphStyle},
			{Text: c.Name, Style: nameStyle},
			{Text: c.Image, Style: imgStyle},
			{Text: stateText, Style: stateStyle},
			{Text: fmt.Sprintf("%d %s", c.Restarts, tui.GlyphRestarts), Style: restartStyle},
		}})
	}
	t := components.Table{
		Columns:     cols,
		Rows:        rows,
		Selected:    -1,
		Width:       width,
		Height:      len(rows) + 1,
		HeaderStyle: lipgloss.NewStyle().Foreground(theme.TextFaint),
		FooterStyle: lipgloss.NewStyle().Foreground(theme.TextGhost),
	}
	bars := m.barsLine(theme, width)
	return title + "\n" + t.Render() + "\n" + bars
}

// barsLine renders the CPU/MEM bars vs each container's summed limits
// (docs/design README.md §5a: "CPU/MEM bars with used/limit text; MEM at
// 96% renders the bar and text red").
func (m Model) barsLine(theme tui.Theme, width int) string {
	barStyles := components.BarStyles{
		Track: lipgloss.NewStyle().Foreground(theme.BarTrack),
		Fill:  lipgloss.NewStyle().Foreground(theme.Accent),
		Warn:  lipgloss.NewStyle().Foreground(theme.Warn),
		Bad:   lipgloss.NewStyle().Foreground(theme.Bad),
	}
	dim := lipgloss.NewStyle().Foreground(theme.TextDim)
	const barWidth = 16

	cpuBar := components.MiniBar(m.pod.CPUMilli, m.pod.CPULimitMilli, barWidth, barStyles)
	cpuText := dim.Render(fmt.Sprintf("%s / %s", usageText(m.pod.CPU), limitText(m.pod.CPULimitMilli, formatMilli)))
	memRatio := ratio(m.pod.MEMBytes, m.pod.MEMLimitBytes)
	memStyle := dim
	if memRatio >= 0.96 {
		memStyle = lipgloss.NewStyle().Foreground(theme.BadText)
	}
	memBar := components.MiniBarBadAt(m.pod.MEMBytes, m.pod.MEMLimitBytes, barWidth, barStyles, 0.96)
	memText := memStyle.Render(fmt.Sprintf("%s / %s", usageText(m.pod.MEM), limitText(m.pod.MEMLimitBytes, formatBytes)))

	faint := lipgloss.NewStyle().Foreground(theme.TextFaint)
	line1 := faint.Render("CPU ") + cpuBar + " " + cpuText
	line2 := faint.Render("MEM ") + memBar + " " + memText
	return line1 + "\n" + line2
}

func ratio(used, limit int64) float64 {
	if limit <= 0 {
		return 0
	}
	return float64(used) / float64(limit)
}

func limitText(limit int64, format func(int64) string) string {
	if limit <= 0 {
		return "–"
	}
	return format(limit)
}

func formatMilli(v int64) string { return fmt.Sprintf("%dm", v) }

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

func (m Model) eventsBlock(theme tui.Theme, width int) string {
	title := lipgloss.NewStyle().Foreground(theme.TextFaint).Bold(true).Render("EVENTS") +
		lipgloss.NewStyle().Foreground(theme.TextGhost2).Render(" · newest first")
	if len(m.eventRows) == 0 {
		// a failed fetch is not an empty result — never render a throttled
		// or timed-out events call as a reassuring "no events".
		note := "no events"
		if m.eventsErr != nil {
			note = "events unavailable"
		}
		return title + "\n" + lipgloss.NewStyle().Foreground(theme.TextDim).Render(note)
	}
	warn := lipgloss.NewStyle().Foreground(theme.Warn)
	bad := lipgloss.NewStyle().Foreground(theme.Bad)
	info := lipgloss.NewStyle().Foreground(theme.Info)
	dim := lipgloss.NewStyle().Foreground(theme.TextDim)
	msgStyle := lipgloss.NewStyle().Foreground(theme.TextSecondary)

	// docs/design README.md §5a: "type (Warning yellow/red, Normal blue)" —
	// 9b's sibling events screen already escalates Warning to red for a
	// currently-failing object (severityStyle); this pod IS the object, so
	// its own current status class stands in for 9b's cross-object lookup.
	_, class, _ := statusClass(m.pod)
	failing := class == "fail"

	lines := []string{title}
	for _, e := range m.eventRows {
		typeStyle := info
		if e.Type == "Warning" {
			typeStyle = warn
			if failing {
				typeStyle = bad
			}
		}
		age := shortAge(e.LastSeen)
		msg := ellipsize(e.Message, max(width-40, 10))
		lines = append(lines, fmt.Sprintf("%s  %s  %s  %s",
			typeStyle.Render(padTo(e.Type, 8)),
			dim.Render(padTo(e.Reason, 16)),
			dim.Render(padTo(age, 6)),
			msgStyle.Render(msg)))
	}
	return strings.Join(lines, "\n")
}

// sidebarBlock renders the LABELS/RELATED/TOLERATIONS panel behind a left
// border gutter (mock 5a's 1px #26263a rule); joinColumns pads/clips each
// line to the panel width. The mock's #0a0a0f panel fill is deliberately NOT
// painted: chrome renders transparent in this app (see chrome.go's
// insetChromeLine — background fills show up as a solid slab on translucent/
// non-matching terminals), so the gutter alone carries the panel boundary.
func (m Model) sidebarBlock(theme tui.Theme) string {
	title := lipgloss.NewStyle().Foreground(theme.TextFaint).Bold(true)
	keyStyle := lipgloss.NewStyle().Foreground(theme.TextDim)
	valStyle := lipgloss.NewStyle().Foreground(theme.TextPrimary)
	dim := lipgloss.NewStyle().Foreground(theme.TextDim)
	secondary := lipgloss.NewStyle().Foreground(theme.TextSecondary)
	accent := lipgloss.NewStyle().Foreground(theme.Accent)

	var lines []string
	lines = append(lines, title.Render("LABELS"))
	if len(m.pod.Labels) == 0 {
		lines = append(lines, dim.Render("none"))
	} else {
		for _, k := range sortedKeys(m.pod.Labels) {
			lines = append(lines, keyStyle.Render(k+"=")+valStyle.Render(m.pod.Labels[k]))
		}
	}

	lines = append(lines, "", title.Render("RELATED"))
	if m.pod.Owner != "" {
		lines = append(lines, accent.Render(m.pod.Owner+" ↗"))
	} else {
		lines = append(lines, dim.Render("none"))
	}

	lines = append(lines, "", title.Render("TOLERATIONS"))
	if len(m.pod.Tolerations) == 0 {
		lines = append(lines, dim.Render("none"))
	} else {
		for _, t := range m.pod.Tolerations {
			lines = append(lines, secondary.Render(t))
		}
	}

	for i, l := range lines {
		lines[i] = sidebarLine(theme, l)
	}
	return strings.Join(lines, "\n")
}

// sidebarLine wraps one pre-styled sidebar content line with the border
// gutter; joinColumns' Pad pads/clips it to the panel width.
func sidebarLine(theme tui.Theme, content string) string {
	return lipgloss.NewStyle().Foreground(theme.Border).Render("│ ") + content
}

// deleteConfirmModal renders 8b's type-the-name modal for the pod's delete
// confirmation (poddetail has only the one mutating verb, so — unlike
// browse — this branch is unconditional, no per-verb split needed).
func (m Model) deleteConfirmModal(width, height int) string {
	theme := m.Theme()
	title := "Confirm"
	target := m.name
	detail := ""
	var ownerLine string
	if pending := m.actions.Pending(); pending != nil {
		title = "✕ " + pending.Label
		target = pending.Scope.ResourceName
		if pending.Owner != "" {
			ownerLine = pending.Owner + " — will be recreated"
		}
		if pending.Scope.Verb == "force-delete" {
			detail = "grace period 0 — force delete, immediate"
		} else {
			detail = "default grace period applies · ctrl-k force delete (immediate)"
		}
	}

	styles := components.TypeModalStyles{
		Border:   lipgloss.NewStyle().BorderForeground(theme.ConfirmBorder).Background(theme.ConfirmHeaderBg),
		Title:    lipgloss.NewStyle().Foreground(theme.Bad).Bold(true).Background(theme.ConfirmHeaderBg),
		ProdTag:  lipgloss.NewStyle().Foreground(theme.ProdText).Bold(true).Background(theme.ConfirmHeaderBg),
		Owner:    lipgloss.NewStyle().Foreground(theme.Good).Background(theme.ConfirmHeaderBg),
		Detail:   lipgloss.NewStyle().Foreground(theme.TextSecondary).Background(theme.ConfirmHeaderBg),
		Rule:     lipgloss.NewStyle().Foreground(theme.TextGhost).Background(theme.ConfirmHeaderBg),
		Input:    lipgloss.NewStyle().Foreground(theme.Text).Background(theme.ConfirmHeaderBg),
		Progress: lipgloss.NewStyle().Foreground(theme.TextFaint).Background(theme.ConfirmHeaderBg),
		Key:      lipgloss.NewStyle().Foreground(theme.Bad).Background(theme.ConfirmHeaderBg),
		Label:    lipgloss.NewStyle().Foreground(theme.TextDim).Background(theme.ConfirmHeaderBg),
	}
	return components.TypeNameModal(title, ownerLine, detail, target, m.actions.TypedName(), m.isProd(), styles, width, height)
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

func padTo(s string, width int) string {
	if len(s) >= width {
		return s[:width]
	}
	return s + strings.Repeat(" ", width-len(s))
}

// shortAge renders how long ago t was — mirrors resources.shortAge's
// compact "12m"/"3h"/"5d" shape (that helper is unexported in the resources
// package, so poddetail's EVENTS grid needs its own copy).
func shortAge(t time.Time) string {
	return shortDur(time.Since(t))
}

// shortDur is shortAge for an already-computed duration — the termination
// banner's "4m ago", never a raw Go duration like "456h29m47s".
func shortDur(d time.Duration) string {
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

func ellipsize(s string, width int) string {
	if width <= 1 || len(s) <= width {
		return s
	}
	return s[:width-1] + "…"
}
