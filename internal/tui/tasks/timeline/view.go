package timeline

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"

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

// Header is 16a's "… › <namespace> › Timeline" + "last 30m" tag, or 16b's
// "… › <object> › Timeline" (docs/design README.md §16a/§16b).
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
	if m.objectScoped() {
		crumbs = append(crumbs,
			tui.Crumb{Text: " › ", Style: ghost},
			tui.Crumb{Text: string(m.objectKind) + "/" + m.objectName, Style: dim},
		)
	} else {
		nsText := m.namespace
		if nsText == "" {
			nsText = tui.GlyphAllNS + " all namespaces"
		}
		crumbs = append(crumbs,
			tui.Crumb{Text: " › ", Style: ghost},
			tui.Crumb{Text: nsText, Style: lipgloss.NewStyle().Foreground(theme.Accent)},
		)
	}
	crumbs = append(crumbs,
		tui.Crumb{Text: " › ", Style: ghost},
		tui.Crumb{Text: "Timeline", Style: text},
		tui.Crumb{Text: " · " + windowLabel(m.window), Style: ghost},
	)

	var forwardChip tui.ConnBadge
	if m.session != nil {
		forwardChip = tui.BuildForwardChip(theme, m.session.ForwardSummary())
	}
	return tui.HeaderState{
		Crumbs:      crumbs,
		ForwardChip: forwardChip,
		Conn:        tui.LiveConnBadge(theme, m.conn, tui.GlyphRunning+" connected"),
	}
}

func (m Model) Strips(width int) []string {
	if m.state != tui.TaskStateReady && m.state != tui.TaskStateEmpty {
		return nil
	}
	lines := []string{m.summaryLine(m.Theme(), width)}
	if m.filterActive {
		lines = append(lines, m.filterStripLine(m.Theme(), width))
	}
	return lines
}

// counts tallies m.rows (already window/filter-applied by recomputeVisible)
// into the strip's per-kind totals — reading the same rows Body walks, so
// the strip and the feed can never disagree about what's currently shown.
func (m Model) counts() (rollouts, restarts, warnings int) {
	for _, e := range m.rows {
		switch e.Kind {
		case kube.TimelineRollout:
			rollouts++
		case kube.TimelineRestart:
			restarts++
		case kube.TimelineEvent:
			if e.Severity == "Warning" {
				warnings++
			}
		}
	}
	return rollouts, restarts, warnings
}

// summaryLine is 16a's "⇅ 1 rollout · ↺ 41 restarts · ▲ warnings …" strip
// (docs/design README.md §16a).
func (m Model) summaryLine(theme tui.Theme, width int) string {
	rollout := lipgloss.NewStyle().Foreground(theme.Accent)
	restart := lipgloss.NewStyle().Foreground(theme.TextSecondary)
	warn := lipgloss.NewStyle().Foreground(theme.Warn)
	dim := lipgloss.NewStyle().Foreground(theme.TextFaint)
	num := lipgloss.NewStyle().Foreground(theme.TextPrimary)

	rollouts, restarts, warnings := m.counts()
	parts := make([]string, 0, 3)
	if rollouts > 0 {
		parts = append(parts, rollout.Render(tui.GlyphRollout)+" "+num.Render(fmt.Sprintf("%d", rollouts))+" "+dim.Render(pluralize(rollouts, "rollout")))
	}
	parts = append(parts, restart.Render(tui.GlyphRestarts)+" "+num.Render(fmt.Sprintf("%d", restarts))+" "+dim.Render("restarts"))
	parts = append(parts, warn.Render(tui.GlyphWarning)+" "+num.Render(fmt.Sprintf("%d", warnings))+" "+dim.Render("warnings"))

	left := strings.Join(parts, "   ")
	right := dim.Render(windowLabel(m.window) + " · merged feed")
	return fillLine(padBetween(left, right, width), width, false, theme)
}

func pluralize(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

// filterStripLine mirrors tasks/events' own filter strip.
func (m Model) filterStripLine(theme tui.Theme, width int) string {
	accent := lipgloss.NewStyle().Foreground(theme.Accent)
	text := lipgloss.NewStyle().Foreground(theme.Text)
	dim := lipgloss.NewStyle().Foreground(theme.TextDim)

	left := accent.Render("/ ") + text.Render(m.filterQuery) + accent.Render(tui.GlyphSelBar)
	right := dim.Render(fmt.Sprintf("%d matched", len(m.rows)))
	return fillLine(padBetween(left, right, width), width, false, theme)
}

func windowLabel(d time.Duration) string {
	switch d {
	case 30 * time.Minute:
		return "last 30m"
	case time.Hour:
		return "last hour"
	case 6 * time.Hour:
		return "last 6h"
	case 24 * time.Hour:
		return "last 24h"
	default:
		return "all time"
	}
}

func (m Model) Body(width, height int) string {
	switch m.state {
	case tui.TaskStateEmpty:
		return components.CenterLines([]string{m.emptyMessage()}, width, height)
	case tui.TaskStateReady:
		return m.timelineBody(m.Theme(), width, height)
	case tui.TaskStateLoading:
		style := lipgloss.NewStyle().Foreground(m.Theme().Accent)
		return components.LoadingBody(m.spinner, style, m.feedback, width, height)
	default:
		return components.CenterLines([]string{m.feedback}, width, height)
	}
}

func (m Model) emptyMessage() string {
	if m.objectScoped() {
		return "nothing changed for " + string(m.objectKind) + "/" + m.objectName + " in " + windowLabel(m.window)
	}
	if m.namespace == "" {
		return "nothing changed cluster-wide in " + windowLabel(m.window)
	}
	return "nothing changed in " + m.namespace + " in " + windowLabel(m.window)
}

// timelineBody stacks 16b's revision rail (when resolved) above the merged
// feed, separated by a rule — the "vertical rail" idiom (docs/design
// README.md §16b) sitting over the same one-clock feed 16a renders alone.
func (m Model) timelineBody(theme tui.Theme, width, height int) string {
	var lines []string
	if len(m.rail) > 0 {
		lines = append(lines, m.railLines(theme, width)...)
		lines = append(lines, dividerLine(theme, width))
	}
	feedHeight := max(height-len(lines), 1)
	lines = append(lines, m.feedLines(theme, width, feedHeight)...)
	return strings.Join(lines, "\n")
}

var railColumns = []components.Column{
	{Title: "", Min: 2},
	{Title: "REV", Min: 14},
	{Title: "IMAGE", Min: 20, Flex: true},
	{Title: "UPDATED", Min: 10, Align: components.AlignRight},
}

// railLines renders 16b's revision rail: newest-first, one line per
// revision, index 0 (the current revision) bright/bold instead of the dim
// treatment every other revision gets — "the current one highlighted"
// (docs/design README.md §16b), the same idiom tasks/helmhistory later
// reused for Helm release history.
func (m Model) railLines(theme tui.Theme, width int) []string {
	good := lipgloss.NewStyle().Foreground(theme.Good)
	dim := lipgloss.NewStyle().Foreground(theme.TextDim)
	current := lipgloss.NewStyle().Foreground(theme.Text).Bold(true)
	label := lipgloss.NewStyle().Foreground(theme.TextFaint)

	rows := make([]components.Row, 0, len(m.rail))
	for i, e := range m.rail {
		glyph, glyphStyle := tui.GlyphCompleted, dim
		revStyle := dim
		revText := fmt.Sprintf("%d", e.Revision)
		if i == 0 {
			glyph, glyphStyle = tui.GlyphRunning, good
			revStyle = current
			revText += " (current)"
		}
		updated := "–"
		if !e.Time.IsZero() {
			updated = shortAge(time.Since(e.Time)) + " ago"
		}
		rows = append(rows, components.Row{Cells: []components.Cell{
			{Text: glyph, Style: glyphStyle},
			{Text: revText, Style: revStyle},
			{Text: e.Image, Style: dim},
			{Text: updated, Style: dim},
		}})
	}

	t := components.Table{
		Columns:     railColumns,
		Rows:        rows,
		Selected:    -1,
		Width:       width,
		Height:      len(rows) + 1,
		HeaderStyle: lipgloss.NewStyle().Foreground(theme.TextFaint),
	}
	header := label.Render("REVISIONS · " + m.railDeployment)
	return append([]string{header}, strings.Split(t.Render(), "\n")...)
}

func dividerLine(theme tui.Theme, width int) string {
	return lipgloss.NewStyle().Foreground(theme.BorderSubtle).Render(strings.Repeat("─", max(width, 0)))
}

// feedLines renders the currently visible window of m.rows, growing around
// m.selected until height is filled — computed fresh every render from
// Model state alone (no persisted scroll offset), mirroring tasks/events'
// own eventRows.
func (m Model) feedLines(theme tui.Theme, width, height int) []string {
	if len(m.rows) == 0 {
		return []string{components.CenterLines([]string{m.emptyMessage()}, width, height)}
	}
	start, end := visibleWindow(len(m.rows), m.selected, height)
	lines := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		lines = append(lines, m.renderRow(theme, m.rows[i], i == m.selected, width))
	}
	return lines
}

func visibleWindow(n, selected, height int) (start, end int) {
	if n == 0 || height <= 0 {
		return 0, 0
	}
	selected = clamp(selected, 0, n-1)
	start, end = selected, selected+1
	for end-start < height {
		grew := false
		if end < n {
			end++
			grew = true
		}
		if end-start < height && start > 0 {
			start--
			grew = true
		}
		if !grew {
			break
		}
	}
	return start, end
}

// entryGlyphStyle is 16a's "rollouts (⇅ purple) are the visual anchors"
// plus 9b's warning/normal red-vs-yellow-vs-blue rule reused for the
// Events source, and a quiet secondary tone for restarts (docs/design
// README.md §16a).
func (m Model) entryGlyphStyle(theme tui.Theme, e kube.TimelineEntry) (string, lipgloss.Style) {
	switch e.Kind {
	case kube.TimelineRollout:
		return tui.GlyphRollout, lipgloss.NewStyle().Foreground(theme.Accent).Bold(true)
	case kube.TimelineRestart:
		return tui.GlyphRestarts, lipgloss.NewStyle().Foreground(theme.Warn)
	default:
		if e.Severity == "Warning" {
			return tui.GlyphWarning, lipgloss.NewStyle().Foreground(theme.Warn)
		}
		return tui.GlyphCompleted, lipgloss.NewStyle().Foreground(theme.Info)
	}
}

func (m Model) renderRow(theme tui.Theme, e kube.TimelineEntry, selected bool, width int) string {
	glyph, glyphStyle := m.entryGlyphStyle(theme, e)
	dim := lipgloss.NewStyle().Foreground(theme.TextFaint)
	secondary := lipgloss.NewStyle().Foreground(theme.TextSecondary)
	object := lipgloss.NewStyle().Foreground(theme.TextDim)
	if selected {
		glyphStyle = glyphStyle.Background(theme.SelBg)
		dim = dim.Background(theme.SelBg)
		secondary = secondary.Background(theme.SelBg)
		object = object.Background(theme.SelBg)
	}

	ts := "–"
	if !e.Time.IsZero() {
		ts = e.Time.Local().Format("15:04:05")
	}
	objText := components.Truncate(e.Object, max(width/4, 10))
	left := "  " + dim.Render(ts) + "  " + glyphStyle.Render(glyph) + "  " + object.Render(objText)
	msg := components.Truncate(entrySummary(e), max(width-lipgloss.Width(left)-2, 8))
	line := left + "  " + secondary.Render(msg)
	return fillLine(line, width, selected, theme)
}

// entrySummary is the row's "what changed" text — the Reason leads (9b's
// own REASON·OBJECT cell reused as a prefix) with the fuller Message
// trailing, since Message alone drops the reason for restarts/rollouts
// (their own Reason is "Restarted"/"Rollout", not embedded in Message the
// way an Event's already reads).
func entrySummary(e kube.TimelineEntry) string {
	if e.Reason == "" {
		return e.Message
	}
	if e.Message == "" {
		return e.Reason
	}
	return e.Reason + " · " + e.Message
}

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

// padBetween places left-aligned left and right-aligned right within width
// (measuring already-styled/ANSI content) — mirrors tasks/events' own strip
// layout helper.
func padBetween(left, right string, width int) string {
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return left
	}
	return left + strings.Repeat(" ", gap) + right
}

// fillLine pads content out to width with trailing spaces styled through
// the selection background when selected — mirrors tasks/events' own
// helper of the same name.
func fillLine(content string, width int, selected bool, theme tui.Theme) string {
	pad := lipgloss.NewStyle()
	if selected {
		pad = lipgloss.NewStyle().Background(theme.SelBg)
	}
	slack := max(width-lipgloss.Width(content), 0)
	return content + pad.Render(strings.Repeat(" ", slack))
}
