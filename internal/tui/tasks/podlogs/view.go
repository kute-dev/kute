package podlogs

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"

	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/components"
)

func (m Model) View() tea.View { return tea.NewView(m.Render()) }

func (m Model) Render() string { return tui.Frame(m.width, m.height, m) }

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
	container, _ := m.activeContainer()

	crumbs := []tui.Crumb{
		{Text: "kute", Style: accent},
		{Text: " │ ", Style: ghost},
		{Text: ctxName, Style: dim},
		{Text: " › ", Style: ghost},
		{Text: m.pod.Name, Style: text},
		{Text: " › ", Style: ghost},
		{Text: "logs", Style: dim},
	}
	if container != "" {
		crumbs = append(crumbs, tui.Crumb{Text: " · " + container, Style: dim})
	}

	conn := tui.ConnBadge{Text: tui.GlyphFollowing + " following", Style: lipgloss.NewStyle().Foreground(theme.Good)}
	switch {
	case m.stream == StreamError:
		conn = tui.ConnBadge{Text: tui.GlyphFailed + " error", Style: lipgloss.NewStyle().Foreground(theme.Bad)}
	case !m.view.AutoScroll:
		conn = tui.ConnBadge{Text: "⏸ paused", Style: dim}
	}

	return tui.HeaderState{Crumbs: crumbs, ForwardChip: tui.BuildForwardChip(theme, m.session.ForwardSummary()), Conn: conn}
}

// Strips is the toolbar (docs/design README.md §5b): container + since +
// wrap + timestamps on the left, severity-in-view counts on the right —
// plus, while filtering, a second line mirroring browse's filter strip.
func (m Model) Strips(width int) []string {
	if m.taskState() != tui.TaskStateReady {
		return nil
	}
	lines := []string{m.toolbarLine(width)}
	if m.filterActive {
		lines = append(lines, m.filterStripLine(width))
	}
	return lines
}

func (m Model) toolbarLine(width int) string {
	theme := m.Theme()
	dim := lipgloss.NewStyle().Foreground(theme.TextDim)
	text := lipgloss.NewStyle().Foreground(theme.TextSecondary)
	warn := lipgloss.NewStyle().Foreground(theme.Warn)
	bad := lipgloss.NewStyle().Foreground(theme.Bad)

	container, _ := m.activeContainer()
	parts := []string{dim.Render("container ") + text.Render(container)}
	if len(m.pod.Containers) > 1 {
		next := m.pod.Containers[m.nextContainerIndex()]
		parts[0] += dim.Render(" (tab: " + next + ")")
	}
	parts = append(parts, dim.Render("since ")+text.Render(m.sinceLabel()))
	parts = append(parts, dim.Render("wrap ")+text.Render(onOff(m.view.Wrap)))
	parts = append(parts, dim.Render("timestamps ")+text.Render(onOff(m.view.Timestamps)))
	left := strings.Join(parts, dim.Render("  ·  "))

	warnCount, errCount := m.visibleSeverityCounts()
	right := warn.Render(fmt.Sprintf("%d WRN", warnCount)) + dim.Render(" · ") +
		bad.Render(fmt.Sprintf("%d ERR", errCount)) + dim.Render(" in view")

	return insetStripLine(padBetween(left, right, stripInnerWidth(width)), width)
}

// filterStripLine mirrors browse's live "/" query strip (view.go's
// filterStripLine there) — same left query/marker + right matched/total
// shape, applied to log entries instead of table rows, plus the same "N
// hidden by filter — esc to clear" notice once the query hides lines
// (docs/design system-wide interactions: "items never silently disappear").
func (m Model) filterStripLine(width int) string {
	theme := m.Theme()
	accent := lipgloss.NewStyle().Foreground(theme.Accent)
	text := lipgloss.NewStyle().Foreground(theme.Text)
	faint := lipgloss.NewStyle().Foreground(theme.TextFaint)
	dim := lipgloss.NewStyle().Foreground(theme.TextDim)

	left := accent.Render("/ ") + text.Render(m.filterQuery) + accent.Render(tui.GlyphSelBar)
	total, matched := len(m.buffer.Entries), len(m.filteredEntries())
	right := dim.Render(fmt.Sprintf("%d/%d", matched, total))
	if matched < total {
		right = faint.Render(fmt.Sprintf("%d hidden by filter — esc to clear   ", total-matched)) + right
	}
	return insetStripLine(padBetween(left, right, stripInnerWidth(width)), width)
}

func (m Model) Body(width, height int) string {
	if state := m.taskState(); state != tui.TaskStateReady {
		if state == tui.TaskStateLoading {
			style := lipgloss.NewStyle().Foreground(m.Theme().Accent)
			return components.LoadingBody(m.spinner, style, m.feedback, width, height)
		}
		return components.CenterLines([]string{m.feedback}, width, height)
	}

	theme := m.Theme()
	entries := m.filteredEntries()
	// docs/design README.md §5b: "a full-width red-tinted row for the most
	// significant one" — every ERR line gets red message/level text, but
	// only the latest (highest-index) ERR entry gets the extra tinted
	// background row.
	lastErr := lastErrIndex(entries)
	start := m.view.VerticalOffset
	if start > len(entries) {
		start = len(entries)
	}
	lines := make([]string, 0, height)
	for i, entry := range m.visibleEntries(entries) {
		lines = append(lines, m.formatEntry(theme, entry, width, start+i == lastErr))
	}
	if m.buffer.DroppedCount > 0 {
		lines = append(lines, lipgloss.NewStyle().Foreground(theme.TextFaint).Render(fmt.Sprintf("… %d older log lines dropped …", m.buffer.DroppedCount)))
	}
	for len(lines) < height-1 {
		lines = append(lines, "")
	}
	lines = append(lines, m.statusLine(theme, width))
	return strings.Join(lines, "\n")
}

func (m Model) visibleEntries(entries []LogEntry) []LogEntry {
	start := m.view.VerticalOffset
	if start > len(entries) {
		start = len(entries)
	}
	end := start + m.entryViewportHeight()
	if end > len(entries) {
		end = len(entries)
	}
	return entries[start:end]
}

// lastErrIndex returns the index of the last (most recent) ERR-severity
// entry in entries, or -1 if none.
func lastErrIndex(entries []LogEntry) int {
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Severity == SeverityErr {
			return i
		}
	}
	return -1
}

// formatEntry renders one log line. mostSignificantErr is true only for the
// single ERR entry (lastErrIndex) that gets the spec's extra full-width
// tinted background row — every ERR line still gets red message/level text
// regardless (docs/design README.md §5b).
func (m Model) formatEntry(theme tui.Theme, entry LogEntry, width int, mostSignificantErr bool) string {
	if entry.Boundary {
		return centeredRule(entry.Message+" · "+entry.Timestamp, width, lipgloss.NewStyle().Foreground(theme.TextFaint))
	}

	dimTS := lipgloss.NewStyle().Foreground(theme.TextGhost)
	msgStyle := lipgloss.NewStyle().Foreground(theme.TextSecondary)
	levelStyle := lipgloss.NewStyle().Foreground(theme.TextDim)
	isErr := entry.Severity == SeverityErr
	tinted := isErr && mostSignificantErr
	switch {
	case isErr:
		levelStyle = lipgloss.NewStyle().Foreground(theme.Bad)
		msgStyle = lipgloss.NewStyle().Foreground(theme.BadText)
		if tinted {
			dimTS = dimTS.Background(theme.ErrBannerBg)
			levelStyle = levelStyle.Background(theme.ErrBannerBg)
			msgStyle = msgStyle.Background(theme.ErrBannerBg)
		}
	case entry.Severity == SeverityWarn:
		levelStyle = lipgloss.NewStyle().Foreground(theme.Warn)
	}

	parts := []string{}
	if m.view.Timestamps && entry.Timestamp != "" {
		parts = append(parts, dimTS.Render(entry.Timestamp))
	}
	if entry.Severity != "" {
		parts = append(parts, levelStyle.Render(entry.Severity))
	}
	message := entry.Message
	if !m.view.Wrap {
		if m.view.HorizontalOffset < len(message) {
			message = message[m.view.HorizontalOffset:]
		} else {
			message = ""
		}
	}
	parts = append(parts, msgStyle.Render(message))
	line := strings.Join(parts, " ")
	if !tinted {
		return line
	}

	// A full-width tinted row (docs/design README.md §5b's "red-tinted
	// row" for ERR lines) needs the trailing pad itself styled with the
	// background — plain components.Pad spaces would leave an unstyled
	// gap once each span's own ANSI reset lands (the same lesson 3a's
	// palette exit notes document for BgPalette/BgInput fills).
	line = components.Truncate(line, width)
	if fill := width - lipgloss.Width(line); fill > 0 {
		line += lipgloss.NewStyle().Background(theme.ErrBannerBg).Render(strings.Repeat(" ", fill))
	}
	return line
}

// statusLine is 5b's bottom status ("▶ live · 2 new lines/s" / paused /
// reconnecting), computed from state set in Update — never a clock read
// here, keeping Render pure.
func (m Model) statusLine(theme tui.Theme, width int) string {
	good := lipgloss.NewStyle().Foreground(theme.Good)
	dim := lipgloss.NewStyle().Foreground(theme.TextDim)

	var line string
	switch {
	case m.stream == StreamReconnecting:
		line = dim.Render("↺ reconnecting…")
	case !m.view.AutoScroll:
		line = dim.Render("⏸ paused · space to resume following")
	default:
		line = good.Render(fmt.Sprintf("%s live · %d new lines/s", tui.GlyphFollowing, m.lastRate))
	}
	return components.Pad(line, width)
}

// visibleViewText renders the currently-visible lines as plain text for
// ctrl-y's clipboard copy (docs/design README.md §5b).
func (m Model) visibleViewText() string {
	entries := m.visibleEntries(m.filteredEntries())
	lines := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.Boundary {
			lines = append(lines, "--- "+e.Message+" · "+e.Timestamp+" ---")
			continue
		}
		var parts []string
		if m.view.Timestamps && e.Timestamp != "" {
			parts = append(parts, e.Timestamp)
		}
		if e.Severity != "" {
			parts = append(parts, e.Severity)
		}
		parts = append(parts, e.Message)
		lines = append(lines, strings.Join(parts, " "))
	}
	return strings.Join(lines, "\n")
}

func (m Model) visibleSeverityCounts() (warn, err int) {
	for _, e := range m.visibleEntries(m.filteredEntries()) {
		switch e.Severity {
		case SeverityWarn:
			warn++
		case SeverityErr:
			err++
		}
	}
	return warn, err
}

func onOff(enabled bool) string {
	if enabled {
		return "on"
	}
	return "off"
}

func centeredRule(text string, width int, style lipgloss.Style) string {
	label := " " + text + " "
	dashes := max(width-lipgloss.Width(label), 0)
	left := dashes / 2
	right := dashes - left
	return style.Render(strings.Repeat("─", left) + label + strings.Repeat("─", right))
}

func padBetween(left, right string, width int) string {
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return left
	}
	return left + strings.Repeat(" ", gap) + right
}

func stripInnerWidth(width int) int {
	return max(width-2*tui.FrameInset, 0)
}

func insetStripLine(line string, width int) string {
	return components.Pad(strings.Repeat(" ", tui.FrameInset)+line, width)
}
