package events

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
	if m.objectKind != "" {
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
	crumbs = append(crumbs, tui.Crumb{Text: " › ", Style: ghost}, tui.Crumb{Text: "Events", Style: text})

	return tui.HeaderState{
		Crumbs:      crumbs,
		UpdateChip:  tui.BuildUpdateChip(theme, m.session),
		ForwardChip: tui.BuildForwardChip(theme, m.session.ForwardSummary()),
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

// counts tallies m.rows (already window/filter/fold-applied by
// recomputeVisible) into warning/normal group totals for the summary strip
// — reading the same rows Body walks, so the strip and the list can never
// disagree about what's currently shown.
func (m Model) counts() (warnings, normal int) {
	for _, r := range m.rows {
		switch r.kind {
		case rowFolded:
			normal += len(r.folded)
		case rowGroup:
			if r.group.Type == "Warning" {
				warnings++
			} else {
				normal++
			}
		}
	}
	return warnings, normal
}

// summaryLine is 9b's "▲ 4 warnings · ○ 31 normal    last hour · deduped ·
// warnings first" (docs/design README.md §9b).
func (m Model) summaryLine(theme tui.Theme, width int) string {
	warnStyle := lipgloss.NewStyle().Foreground(theme.Warn)
	dim := lipgloss.NewStyle().Foreground(theme.TextFaint)
	num := lipgloss.NewStyle().Foreground(theme.TextPrimary)

	warnings, normal := m.counts()
	left := warnStyle.Render(tui.GlyphWarning) + " " + num.Render(fmt.Sprintf("%d", warnings)) + " " + dim.Render("warnings")
	if !m.warningsOnly {
		left += "   " + dim.Render(tui.GlyphCompleted) + " " + num.Render(fmt.Sprintf("%d", normal)) + " " + dim.Render("normal")
	}
	right := dim.Render(windowLabel(m.window) + " · deduped · warnings first")
	return fillLine(padBetween(left, right, width, false, theme), width, false, theme)
}

// filterStripLine mirrors browse's own filter strip (the live "/" query and
// matched/total, plus the "N hidden by filter — esc to clear" notice once
// the query itself hides groups — docs/design system-wide interactions:
// "items never silently disappear"), substring rather than fuzzy — events
// are free-text messages, not short row names, the same reasoning podlogs'
// live filter uses (mvp-tasks.md's Phase 6 exit notes). The denominator is
// filterBaselineGroups (window/warningsOnly narrowing), not the raw group
// count, so those separate toggles never get misreported as the query's
// own doing.
func (m Model) filterStripLine(theme tui.Theme, width int) string {
	accent := lipgloss.NewStyle().Foreground(theme.Accent)
	text := lipgloss.NewStyle().Foreground(theme.Text)
	faint := lipgloss.NewStyle().Foreground(theme.TextFaint)
	dim := lipgloss.NewStyle().Foreground(theme.TextDim)

	left := accent.Render("/ ") + text.Render(m.filterQuery) + accent.Render(tui.GlyphSelBar)

	total, matched := m.filterBaselineGroups, m.filterMatchedGroups
	right := dim.Render(fmt.Sprintf("%d matched", matched))
	if matched < total {
		right = faint.Render(fmt.Sprintf("%d hidden by filter — esc to clear   ", total-matched)) + right
	}
	return fillLine(padBetween(left, right, width, false, theme), width, false, theme)
}

func windowLabel(d time.Duration) string {
	switch d {
	case 15 * time.Minute:
		return "last 15m"
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
		return m.eventRows(m.Theme(), width, height)
	case tui.TaskStateLoading:
		style := lipgloss.NewStyle().Foreground(m.Theme().Accent)
		return components.LoadingBody(m.spinner, style, m.feedback, width, height)
	default:
		return components.CenterLines([]string{m.feedback}, width, height)
	}
}

func (m Model) emptyMessage() string {
	if m.objectKind != "" {
		return "no events for " + string(m.objectKind) + "/" + m.objectName
	}
	if m.namespace == "" {
		return "no events cluster-wide"
	}
	return "no events in " + m.namespace
}

// eventRows renders the currently visible window of m.rows, growing around
// m.selected until height is filled — computed fresh every render from
// Model state alone (no persisted scroll offset), which keeps Body a pure
// function of (m, width, height).
func (m Model) eventRows(theme tui.Theme, width, height int) string {
	if len(m.rows) == 0 {
		return components.CenterLines([]string{m.emptyMessage()}, width, height)
	}
	start, end := visibleWindow(m.rows, m.selected, height, width)
	lines := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		lines = append(lines, m.renderRow(theme, m.rows[i], i == m.selected, width))
	}
	return strings.Join(lines, "\n")
}

// rowLines is how many terminal lines r occupies at width: 1 for the reason
// line plus however many lines the OBJECT/MESSAGE cell wraps to (docs/design
// README.md §9b's "MESSAGE (widest, verbatim)" — wrapped rather than
// truncated, so a long message just grows the row instead of losing text),
// 1 for the folded normal-events summary.
func rowLines(r displayRow, width int) int {
	if r.kind == rowFolded {
		return 1
	}
	return 1 + len(wrappedMessageLines(r.group, width))
}

// visibleWindow finds a [start,end) row range around selected that fits
// within height lines (rowLines-aware, since rows have mixed heights),
// growing downward first then upward while budget remains.
func visibleWindow(rows []displayRow, selected, height, width int) (start, end int) {
	n := len(rows)
	if n == 0 || height <= 0 {
		return 0, 0
	}
	selected = clamp(selected, 0, n-1)
	start, end = selected, selected+1
	used := rowLines(rows[selected], width)
	for {
		grew := false
		if end < n && used+rowLines(rows[end], width) <= height {
			used += rowLines(rows[end], width)
			end++
			grew = true
		}
		if start > 0 && used+rowLines(rows[start-1], width) <= height {
			used += rowLines(rows[start-1], width)
			start--
			grew = true
		}
		if !grew {
			break
		}
	}
	return start, end
}

func (m Model) renderRow(theme tui.Theme, r displayRow, selected bool, width int) string {
	if r.kind == rowFolded {
		return m.renderFoldedRow(theme, r.folded, selected, width)
	}
	return m.renderGroupRow(theme, r.group, selected, width)
}

// severityStyle is 9b's red-vs-yellow-vs-blue rule (docs/design README.md
// §9b): Warning groups render red when the involved object is a currently
// StatusFail pod (m.failing, load.go's best-effort cross-check), yellow
// otherwise; Normal groups are always the neutral Info color.
func (m Model) severityStyle(theme tui.Theme, g kube.EventGroup) lipgloss.Style {
	if g.Type != "Warning" {
		return lipgloss.NewStyle().Foreground(theme.Info)
	}
	if m.failing[objectName(g.Object)] {
		return lipgloss.NewStyle().Foreground(theme.Bad)
	}
	return lipgloss.NewStyle().Foreground(theme.Warn)
}

func objectName(object string) string {
	_, name, ok := strings.Cut(object, "/")
	if !ok {
		return object
	}
	return name
}

func (m Model) renderGroupRow(theme tui.Theme, g kube.EventGroup, selected bool, width int) string {
	sev := m.severityStyle(theme, g)
	dim := lipgloss.NewStyle().Foreground(theme.TextFaint)
	secondary := lipgloss.NewStyle().Foreground(theme.TextSecondary)
	if selected {
		sev = sev.Background(theme.SelBg)
		dim = dim.Background(theme.SelBg)
		secondary = secondary.Background(theme.SelBg)
	}

	glyph := tui.GlyphWarning
	if g.Type != "Warning" {
		glyph = tui.GlyphCompleted
	}
	countText := ""
	if g.Count > 1 {
		countText = fmt.Sprintf("×%d", g.Count)
	}

	left1 := selSpace(2, selected, theme) + sev.Render(glyph+" "+g.Reason)
	right1 := dim.Render(strings.TrimSpace(countText + "   " + shortEventAge(g.LastSeen, m.fetchedAt)))
	line1 := fillLine(padBetween(left1, right1, width, selected, theme), width, selected, theme)

	object := objectCell(g.Object, width)
	prefix := "    " + object + "  "
	lines := make([]string, 0, 2)
	lines = append(lines, line1)
	for i, ml := range wrappedMessageLines(g, width) {
		var lead string
		if i == 0 {
			lead = selSpace(4, selected, theme) + dim.Render(object) + selSpace(2, selected, theme)
		} else {
			lead = selSpace(lipgloss.Width(prefix), selected, theme)
		}
		lines = append(lines, fillLine(lead+secondary.Render(ml), width, selected, theme))
	}
	return strings.Join(lines, "\n")
}

// objectCell is g.Object truncated to the same width budget renderGroupRow
// and wrappedMessageLines both use — factored out so the two never disagree
// about how much room the message gets.
func objectCell(object string, width int) string {
	return components.Truncate(object, max(width/3, 10))
}

// wrappedMessageLines word-wraps g.Message to fit the remaining width after
// the "    "+object+"  " prefix, rather than truncating it away (docs/design
// README.md §9b's "MESSAGE (widest, verbatim)"). rowLines calls this too, so
// the row-height math visibleWindow relies on never disagrees with what
// renderGroupRow actually draws.
func wrappedMessageLines(g kube.EventGroup, width int) []string {
	prefixWidth := lipgloss.Width("    " + objectCell(g.Object, width) + "  ")
	msgWidth := max(width-prefixWidth, 8)
	wrapped := lipgloss.NewStyle().Width(msgWidth).Render(g.Message)
	return strings.Split(wrapped, "\n")
}

// renderFoldedRow is 9b's "▸ normal · 31 events — Pulled · Created…"
// collapsed summary line for every folded normal group (docs/design
// README.md §9b), naming up to 4 distinct reasons.
func (m Model) renderFoldedRow(theme tui.Theme, groups []kube.EventGroup, selected bool, width int) string {
	info := lipgloss.NewStyle().Foreground(theme.Info)
	dim := lipgloss.NewStyle().Foreground(theme.TextFaint)
	if selected {
		info = info.Background(theme.SelBg)
		dim = dim.Background(theme.SelBg)
	}

	seen := map[string]bool{}
	reasons := make([]string, 0, 4)
	for _, g := range groups {
		if seen[g.Reason] {
			continue
		}
		seen[g.Reason] = true
		reasons = append(reasons, g.Reason)
		if len(reasons) == 4 {
			break
		}
	}

	content := selSpace(2, selected, theme) + info.Render(tui.GlyphExpand) + " " +
		dim.Render(fmt.Sprintf("normal · %d events — %s", len(groups), strings.Join(reasons, " · ")))
	return fillLine(components.Truncate(content, width), width, selected, theme)
}

func shortEventAge(t, now time.Time) string {
	if t.IsZero() || now.IsZero() {
		return "–"
	}
	d := now.Sub(t)
	if d < 0 {
		d = 0
	}
	switch {
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
// (measuring already-styled/ANSI content), matching browse's own strip
// layout helper. Drops right when there isn't room for both. The gap itself
// is rendered through selSpace so a selected row's highlight doesn't break
// into disjoint patches around the plain-space fill (the bug docs/design
// README.md §9b's golden fixtures caught: an unstyled gap of literal spaces
// between the reason and the age column left a hole in the highlight).
func padBetween(left, right string, width int, selected bool, theme tui.Theme) string {
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return left
	}
	return left + selSpace(gap, selected, theme) + right
}

// selSpace renders n literal spaces, styled with the selection background
// when selected — used everywhere a row concatenates plain-string indent/gap
// spaces next to styled text, so a selected row's highlight is one solid bar
// rather than patches around each Render() call (unstyled string
// concatenation leaves the terminal's default background showing through).
func selSpace(n int, selected bool, theme tui.Theme) string {
	if n <= 0 {
		return ""
	}
	if !selected {
		return strings.Repeat(" ", n)
	}
	return lipgloss.NewStyle().Background(theme.SelBg).Render(strings.Repeat(" ", n))
}

// fillLine pads content out to width with trailing spaces styled through
// the selection background when selected, so a selected row's highlight
// reaches the row's right edge instead of stopping at the last glyph.
func fillLine(content string, width int, selected bool, theme tui.Theme) string {
	slack := max(width-lipgloss.Width(content), 0)
	return content + selSpace(slack, selected, theme)
}
