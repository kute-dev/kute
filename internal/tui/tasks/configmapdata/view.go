package configmapdata

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/kute-dev/kute/internal/kube"
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

// Header is 27a's breadcrumb: "… › <namespace> › cm/<name> › Data" — the
// same trailing-segment shape secretdata's own "secret/<name> › Data" uses.
func (m Model) Header() tui.HeaderState {
	theme := m.Theme()
	accent := lipgloss.NewStyle().Foreground(theme.Accent).Bold(true)
	dim := lipgloss.NewStyle().Foreground(theme.TextDim)
	ghost := lipgloss.NewStyle().Foreground(theme.TextGhost)
	secondary := lipgloss.NewStyle().Foreground(theme.TextSecondary)
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
		{Text: m.namespace, Style: lipgloss.NewStyle().Foreground(theme.Accent)},
		{Text: " › ", Style: ghost},
		{Text: "cm/" + m.name, Style: secondary},
		{Text: " › ", Style: ghost},
		{Text: "Data", Style: text},
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

// Strips renders two lines: the key count (with a live → N preview while an
// add/remove is in flight, matching 26a/27b's own precedent), and 27a's own
// consumer strip — "deploy/aim-worker ↗ env · deploy/aim-gateway ↗ volume"
// left, "pods don't reload configmaps on their own" right (docs/design
// README.md §27a).
func (m Model) Strips(width int) []string {
	if m.state != tui.TaskStateReady {
		return nil
	}
	theme := m.Theme()
	count := lipgloss.NewStyle().Foreground(theme.TextPrimary)
	dim := lipgloss.NewStyle().Foreground(theme.TextFaint)

	word := "key"
	if len(m.keys) != 1 {
		word = "keys"
	}
	label := fmt.Sprintf("%d %s", len(m.keys), word)
	switch {
	case m.adding != nil || (m.pendingCommit != nil && !m.pendingCommit.remove && !m.pendingCommit.isEdit && m.actions.Active()):
		label = fmt.Sprintf("%d %s → %d", len(m.keys), word, len(m.keys)+1)
	case m.pendingCommit != nil && m.pendingCommit.remove && m.actions.Active():
		label = fmt.Sprintf("%d %s → %d", len(m.keys), word, len(m.keys)-1)
	}
	countLine := insetStripLine(padBetween(count.Render(label), "", stripInnerWidth(width)), width)

	consumersLeft := dim.Render("no consumers found")
	if len(m.consumers) > 0 {
		parts := make([]string, len(m.consumers))
		for i, c := range m.consumers {
			parts[i] = consumerRefText(theme, c)
		}
		consumersLeft = strings.Join(parts, dim.Render(" · "))
	}
	consumersRight := dim.Render("pods don't reload configmaps on their own")
	consumersLine := insetStripLine(padBetween(consumersLeft, consumersRight, stripInnerWidth(width)), width)

	return []string{countLine, consumersLine}
}

// consumerRefText renders one consumer as "deploy/aim-worker ↗ env" — the
// same short workload-arg forms (deploy/sts/ds) kubectl-command rendering
// uses elsewhere (kube.ConfigMapConsumerRestartCommandString), so the strip
// and the will-run restart lines name workloads identically.
func consumerRefText(theme tui.Theme, c configMapConsumer) string {
	name := lipgloss.NewStyle().Foreground(theme.TextPrimary).Render(shortWorkloadArg(c.Kind) + "/" + c.Name)
	arrow := lipgloss.NewStyle().Foreground(theme.TextFaint).Render(" ↗ " + c.refKind)
	return name + arrow
}

func shortWorkloadArg(kind kube.ResourceKind) string {
	switch kind {
	case kube.KindStatefulSet:
		return "sts"
	case kube.KindDaemonSet:
		return "ds"
	default:
		return "deploy"
	}
}

func (m Model) Body(width, height int) string {
	switch m.state {
	case tui.TaskStateLoading:
		style := lipgloss.NewStyle().Foreground(m.Theme().Accent)
		return components.LoadingBody(m.spinner, style, m.feedback, width, height)
	case tui.TaskStateReady:
		if m.multiline != nil {
			return m.multilineBody(m.Theme(), width, height)
		}
		return m.gridBody(m.Theme(), width, height)
	default:
		return components.CenterLines([]string{m.feedback}, width, height)
	}
}

// gridBody renders the KEY/VALUE/SIZE grid, the add row (or its hint), and
// the will-run strip — mirrors secretdata's own gridBody.
func (m Model) gridBody(theme tui.Theme, width, height int) string {
	var lines []string
	lines = append(lines, m.columnHeaderLine(theme, width))
	for i := range m.keys {
		lines = append(lines, m.configMapRowLine(theme, i, width))
	}
	switch {
	case m.adding != nil:
		lines = append(lines, m.addRowLine(theme, width))
	case m.pendingCommit != nil && !m.pendingCommit.remove && !m.pendingCommit.isEdit && m.actions.Active():
		lines = append(lines, m.pendingAddRowLine(theme, width, m.pendingCommit))
	default:
		hint := lipgloss.NewStyle().Foreground(theme.TextGhost).Render("+ a add key")
		lines = append(lines, hint)
	}
	if len(m.keys) == 0 && m.adding == nil {
		lines = append(lines, lipgloss.NewStyle().Foreground(theme.TextGhost).Render("no keys"))
	}
	if strip := m.willRunStrip(theme, width); strip != "" {
		lines = append(lines, "", strip)
	}
	return components.Pad(strings.Join(lines, "\n"), width)
}

func (m Model) columnHeaderLine(theme tui.Theme, width int) string {
	faint := lipgloss.NewStyle().Foreground(theme.TextFaint)
	return configMapRowColumns("", faint.Render("KEY"), faint.Render("VALUE"), faint.Render("SIZE"), width, lipgloss.NewStyle())
}

// configMapRowLine renders one existing key's row — the real value (no
// masking) normally, a folded "▸ N lines · e opens the buffer editor"
// summary for a multi-line value, or the in-place edit buffer/pending-
// confirm note for whichever row is currently in flight.
func (m Model) configMapRowLine(theme tui.Theme, idx int, width int) string {
	r := m.keys[idx]
	removing, removeKey := m.pendingRemove()
	editConfirming, editConfirmKey := m.pendingEditConfirm()
	isPendingRemove := removing && removeKey == r.key
	isPendingEdit := editConfirming && editConfirmKey == r.key
	isEditingNow := m.editing != nil && m.editing.key == r.key
	selected := m.adding == nil && m.editing == nil && m.selected == idx

	fill := lipgloss.NewStyle()
	if selected || isEditingNow {
		fill = fill.Background(theme.SelBg)
	}
	marker := fill.Render("  ")
	if selected || isEditingNow {
		marker = lipgloss.NewStyle().Foreground(theme.Accent).Background(theme.SelBg).Render("›") + fill.Render(" ")
	}
	keyStyle := fill.Foreground(theme.Text)
	valueStyle := fill.Foreground(theme.TextSecondary)
	dimStyle := fill.Foreground(theme.TextFaint)
	sizeStyle := fill.Foreground(theme.TextDim)

	var value string
	switch {
	case isEditingNow:
		value = m.editValueCell(theme, fill)
	case isPendingEdit:
		value = valueStyle.Render(components.Truncate(oneLine(r.value), 24)) + fill.Render("  ") + fill.Foreground(theme.Warn).Render("confirm to update · y/N")
	case isPendingRemove:
		value = valueStyle.Render(components.Truncate(oneLine(r.value), 24)) + fill.Render("  ") + fill.Foreground(theme.Bad).Render("remove · y/N")
	case r.multiline():
		lineCount := strings.Count(r.value, "\n") + 1
		value = dimStyle.Render(fmt.Sprintf("▸ %d lines · e opens the buffer editor", lineCount))
	default:
		value = valueStyle.Render(r.value)
	}
	size := sizeStyle.Render(formatByteSize(r.size))
	return configMapRowColumns(marker, keyStyle.Render(r.key), value, size, width, fill)
}

// editValueCell renders '↵'s single-line in-place edit buffer — "was
// <original> · <live buffer>" per docs/design README.md §27a ("prior value
// stays visible as `was info ·` while typing").
func (m Model) editValueCell(theme tui.Theme, fill lipgloss.Style) string {
	e := m.editing
	dim := fill.Foreground(theme.TextDim)
	bold := fill.Foreground(theme.Text).Bold(true)
	accent := fill.Foreground(theme.Accent)
	was := dim.Render("was " + components.Truncate(oneLine(e.original), 20) + " · ")
	return was + addBufferCell(e.value, e.valueCursor, true, bold, accent, dim)
}

// pendingEditConfirm reports whether an existing key's PROD y/N is currently
// showing, and which key it targets — mirrors pendingRemove.
func (m Model) pendingEditConfirm() (bool, string) {
	if !m.actions.Active() {
		return false, ""
	}
	pc := m.pendingCommit
	if pc == nil || pc.remove || !pc.isEdit {
		return false, ""
	}
	return true, pc.key
}

// pendingRemove reports whether a removal's inline y/N is currently showing,
// and which key it targets.
func (m Model) pendingRemove() (bool, string) {
	if !m.actions.Active() {
		return false, ""
	}
	p := m.actions.Pending()
	if p == nil || !p.Scope.ConfigMapRemove {
		return false, ""
	}
	return true, p.Scope.ConfigMapKey
}

// addRowLine renders 'a'/insert's line-insert row: a highlighted "+" marker,
// the key buffer, then the value buffer — always visible while typing, no
// mask toggle (a ConfigMap value is never sensitive).
func (m Model) addRowLine(theme tui.Theme, width int) string {
	a := m.adding
	fill := lipgloss.NewStyle().Background(theme.SelBg)
	bold := fill.Foreground(theme.Text).Bold(true)
	accent := fill.Foreground(theme.Accent)
	dim := fill.Foreground(theme.TextDim)
	good := fill.Foreground(theme.Good)

	marker := good.Render("+") + fill.Render(" ")
	key := addBufferCell(a.key, a.keyCursor, !a.onValue, bold, accent, dim)
	value := addBufferCell(a.value, a.valueCursor, a.onValue, bold, accent, dim)
	size := dim.Render("new")
	return configMapRowColumns(marker, key, value, size, width, fill)
}

// addBufferCell renders one of the add/edit row's buffers, with the cursor
// glyph only in the currently focused one — mirrors secretdata's own helper.
func addBufferCell(buffer string, cursor int, focused bool, textStyle, accent, dim lipgloss.Style) string {
	if !focused {
		if buffer == "" {
			return dim.Render("…")
		}
		return textStyle.Render(buffer)
	}
	runes := []rune(buffer)
	pos := min(max(cursor, 0), len(runes))
	pre, post := string(runes[:pos]), string(runes[pos:])
	rendered := textStyle.Render(pre) + accent.Render(tui.GlyphSelBar)
	if post != "" {
		rendered += textStyle.Render(post)
	}
	return rendered
}

// pendingAddRowLine renders the in-flight add row while its own PROD y/N
// confirm is showing (m.adding is already nil by then).
func (m Model) pendingAddRowLine(theme tui.Theme, width int, pc *configMapPendingCommit) string {
	fill := lipgloss.NewStyle().Background(theme.SelBg)
	good := fill.Foreground(theme.Good)
	bold := fill.Foreground(theme.Text).Bold(true)
	warn := fill.Foreground(theme.Warn)
	dim := fill.Foreground(theme.TextDim)

	marker := good.Render("+") + fill.Render(" ")
	value := bold.Render(components.Truncate(oneLine(pc.value), 24)) + fill.Render("  ") + warn.Render("confirm to add · y/N")
	return configMapRowColumns(marker, bold.Render(pc.key), value, dim.Render("new"), width, fill)
}

// multilineBody renders the buffer editor full-screen — the "simpler
// solution" this package substitutes for 17a's own shared buffer editor: a
// key header, a scrollable window of numbered lines with the cursor glyph on
// the active row/column, and the will-run strip at the bottom.
func (m Model) multilineBody(theme tui.Theme, width, height int) string {
	e := m.multiline
	headerStyle := lipgloss.NewStyle().Foreground(theme.Accent).Bold(true)
	hintStyle := lipgloss.NewStyle().Foreground(theme.TextDim)
	header := headerStyle.Render(e.key) + hintStyle.Render("  ctrl-o apply · ctrl-r apply + restart consumers · esc discard")

	willRun := m.willRunStrip(theme, width)
	budget := height - 2 // header + blank line
	if willRun != "" {
		budget -= 2 // blank + strip line
	}
	budget = max(budget, 1)

	lineNoStyle := lipgloss.NewStyle().Foreground(theme.TextGhost)
	textStyle := lipgloss.NewStyle().Foreground(theme.Text)
	accent := lipgloss.NewStyle().Foreground(theme.Accent)

	start, end := scrollWindow(e.row, len(e.lines), budget)
	var body []string
	for i := start; i < end; i++ {
		lineNo := lineNoStyle.Render(fmt.Sprintf("%4d  ", i+1))
		var content string
		if i == e.row {
			runes := []rune(e.lines[i])
			pos := min(max(e.col, 0), len(runes))
			content = textStyle.Render(string(runes[:pos])) + accent.Render(tui.GlyphSelBar) + textStyle.Render(string(runes[pos:]))
		} else {
			content = textStyle.Render(e.lines[i])
		}
		body = append(body, lineNo+content)
	}
	if start > 0 || end < len(e.lines) {
		body = append(body, hintStyle.Render(fmt.Sprintf("… %d of %d lines shown", end-start, len(e.lines))))
	}

	lines := append([]string{header, ""}, body...)
	if willRun != "" {
		lines = append(lines, "", willRun)
	}
	return components.Pad(strings.Join(lines, "\n"), width)
}

// scrollWindow picks a [start, end) line range of size at most budget,
// keeping cursorRow inside it — centers the window on the cursor once the
// buffer is taller than the budget.
func scrollWindow(cursorRow, total, budget int) (int, int) {
	if total <= budget {
		return 0, total
	}
	start := cursorRow - budget/2
	if start < 0 {
		start = 0
	}
	end := start + budget
	if end > total {
		end = total
		start = end - budget
	}
	return start, end
}

// willRunStrip is the screen's own "will run" line(s), styled like
// secretdata's own metaWillRunStrip-derived band: a BorderSubtle top rule,
// then a BgStrip-filled band showing the exact command(s) that would run.
// For a ctrl-r commit this is multiple lines — the patch, then one `kubectl
// rollout restart` per consumer (docs/design README.md §27a: "prints every
// command it runs"). Returns "" when there's nothing to preview.
func (m Model) willRunStrip(theme tui.Theme, width int) string {
	fill := lipgloss.NewStyle().Background(theme.BgStrip)
	label := fill.Foreground(theme.TextDim)
	cmd := fill.Foreground(theme.TextSecondary)

	var primary string
	restart := false
	hasContent := true
	removing, removeKey := m.pendingRemove()
	editConfirming, editConfirmKey := m.pendingEditConfirm()
	switch {
	case m.lastError != "":
		primary = label.Render("error") + fill.Render(" ") + fill.Foreground(theme.Bad).Render(m.lastError)
	case m.message != "":
		primary = fill.Foreground(theme.Good).Render(m.message)
	case removing:
		primary = label.Render("will run") + fill.Render(" ") + cmd.Render(kube.ConfigMapDataCommandString(m.namespace, m.name, removeKey, "", true))
	case editConfirming:
		primary = label.Render("will run") + fill.Render(" ") + cmd.Render(m.commandForKey(editConfirmKey))
		restart = m.pendingCommit != nil && m.pendingCommit.restartConsumers
	case m.adding != nil:
		key := strings.TrimSpace(m.adding.key)
		if key == "" {
			primary = label.Render("will run") + fill.Render(" ") + cmd.Render("type a key to add")
		} else {
			primary = label.Render("will run") + fill.Render(" ") + cmd.Render(kube.ConfigMapDataCommandString(m.namespace, m.name, key, m.adding.value, false))
		}
	case m.pendingCommit != nil && !m.pendingCommit.remove && !m.pendingCommit.isEdit:
		primary = label.Render("will run") + fill.Render(" ") + cmd.Render(kube.ConfigMapDataCommandString(m.namespace, m.name, m.pendingCommit.key, m.pendingCommit.value, false))
		restart = m.pendingCommit.restartConsumers
	case m.editing != nil:
		if !m.editing.changed() {
			primary = label.Render("will run") + fill.Render(" ") + cmd.Render("no changes — ↵ has nothing to apply")
		} else {
			primary = label.Render("will run") + fill.Render(" ") + cmd.Render(kube.ConfigMapDataCommandString(m.namespace, m.name, m.editing.key, m.editing.value, false))
		}
	case m.multiline != nil:
		if !m.multiline.changed() {
			primary = label.Render("will run") + fill.Render(" ") + cmd.Render("no changes — ctrl-o has nothing to apply")
		} else {
			primary = label.Render("will run") + fill.Render(" ") + cmd.Render(m.commandForKey(m.multiline.key))
		}
	default:
		hasContent = false
	}
	if !hasContent {
		return ""
	}

	rule := lipgloss.NewStyle().Foreground(theme.BorderSubtle).Render(strings.Repeat("─", width))
	linesOut := []string{rule, insetStripLineFill(padBetweenFill(primary, "", stripInnerWidth(width), fill), width, fill)}
	if restart {
		for _, c := range m.consumers {
			restartLine := label.Render("       ") + cmd.Render(kube.ConfigMapConsumerRestartCommandString(m.namespace, c.ConfigMapConsumerRef))
			linesOut = append(linesOut, insetStripLineFill(padBetweenFill(restartLine, "", stripInnerWidth(width), fill), width, fill))
		}
	}
	return strings.Join(linesOut, "\n")
}

// commandForKey renders the will-run command for key's currently-buffered
// edit — a single-line value gets the real ConfigMapDataCommandString, a
// multi-line one elides the value to a line count rather than embedding the
// whole escaped buffer in the preview.
func (m Model) commandForKey(key string) string {
	if m.multiline != nil && m.multiline.key == key {
		return fmt.Sprintf("kubectl patch cm/%s --type merge -p '{\"data\":{%q:\"<%d lines>\"}}' -n %s", m.name, key, len(m.multiline.lines), m.namespace)
	}
	if m.editing != nil && m.editing.key == key {
		return kube.ConfigMapDataCommandString(m.namespace, m.name, key, m.editing.value, false)
	}
	if m.pendingCommit != nil && m.pendingCommit.key == key {
		if strings.Contains(m.pendingCommit.value, "\n") {
			lines := strings.Count(m.pendingCommit.value, "\n") + 1
			return fmt.Sprintf("kubectl patch cm/%s --type merge -p '{\"data\":{%q:\"<%d lines>\"}}' -n %s", m.name, key, lines, m.namespace)
		}
		return kube.ConfigMapDataCommandString(m.namespace, m.name, key, m.pendingCommit.value, false)
	}
	return kube.ConfigMapDataCommandString(m.namespace, m.name, key, "", false)
}

// oneLine collapses a value to its first line, for cells that must stay a
// single terminal row (an edit-in-flight/pending-confirm cell showing a
// multi-line value's original).
func oneLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + "…"
	}
	return s
}

// configMapRowColumns lays marker/key/value/size out — mirrors secretdata's
// own secretRowColumns (widths sized for a terminal character grid, package-
// local per the repo's duplication-over-cross-package-coupling convention).
func configMapRowColumns(marker, key, value, size string, width int, fill lipgloss.Style) string {
	const markerWidth, colGap = 2, 2
	avail := max(width-markerWidth-colGap, 6)
	keyWidth := min(28, avail*30/100)
	sizeWidth := min(9, max(avail*8/100, 6))
	valueWidth := max(avail-keyWidth-sizeWidth-colGap, 1)

	padLeft := func(s string, w int) string {
		s = components.Truncate(s, w)
		gap := w - lipgloss.Width(s)
		if gap < 0 {
			gap = 0
		}
		return s + fill.Render(strings.Repeat(" ", gap))
	}
	padRight := func(s string, w int) string {
		s = components.Truncate(s, w)
		gap := w - lipgloss.Width(s)
		if gap < 0 {
			gap = 0
		}
		return fill.Render(strings.Repeat(" ", gap)) + s
	}

	return padLeft(marker, markerWidth) + padLeft(key, keyWidth) + padLeft(value, valueWidth) +
		fill.Render(strings.Repeat(" ", colGap)) + padRight(size, sizeWidth)
}

// formatByteSize renders a value's byte length the same "N B"/"N.N KiB"
// style secretdata's own formatByteSize uses.
func formatByteSize(n int) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%.1f KiB", float64(n)/1024)
}

// padBetween/stripInnerWidth/insetStripLine/insetStripLineFill/
// padBetweenFill mirror browse's/secretdata's own strip-line helpers
// (package-local since Go doesn't share unexported functions across
// packages).
func padBetween(left, right string, width int) string {
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return left
	}
	return left + strings.Repeat(" ", gap) + right
}

func padBetweenFill(left, right string, width int, fill lipgloss.Style) string {
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return left
	}
	return left + fill.Render(strings.Repeat(" ", gap)) + right
}

func stripInnerWidth(width int) int {
	return max(width-2*tui.FrameInset, 0)
}

func insetStripLine(line string, width int) string {
	return components.Pad(strings.Repeat(" ", tui.FrameInset)+line, width)
}

func insetStripLineFill(line string, width int, fill lipgloss.Style) string {
	content := fill.Render(strings.Repeat(" ", tui.FrameInset)) + line
	slack := width - lipgloss.Width(content)
	if slack <= 0 {
		return content
	}
	return content + fill.Render(strings.Repeat(" ", slack))
}
