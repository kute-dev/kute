package helmhistory

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

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

// Header is 18a's history breadcrumb: "… › <namespace> › <release> ›
// History" — the same trailing-segment shape 5b's "… › pod › logs" uses for
// a sub-view of an object.
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
		{Text: m.namespace, Style: lipgloss.NewStyle().Foreground(theme.Accent)},
		{Text: " › ", Style: ghost},
		{Text: m.name, Style: dim},
		{Text: " › ", Style: ghost},
		{Text: "History", Style: text},
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

func (m Model) Strips(width int) []string {
	if m.state != tui.TaskStateReady {
		return nil
	}
	dim := lipgloss.NewStyle().Foreground(theme(m).TextFaint)
	count := lipgloss.NewStyle().Foreground(theme(m).TextPrimary)
	word := "revisions"
	if len(m.revisions) == 1 {
		word = "revision"
	}
	line := count.Render(fmt.Sprintf("%d", len(m.revisions))) + " " + dim.Render(word+" · newest first · current highlighted")
	return []string{insetStripLine(line, width)}
}

func theme(m Model) tui.Theme { return m.Theme() }

func insetStripLine(line string, width int) string {
	return components.Pad(strings.Repeat(" ", tui.FrameInset)+line, width)
}

func (m Model) Body(width, height int) string {
	if m.actions.Active() && m.actions.Tier() == actions.TierModal {
		// TierInline (non-prod: "Rollback inherits 8b friction" — inline
		// y/N, never a modal) keeps rendering the rail underneath, with the
		// confirm text in the keybar's RightNote (keys.go) — only the PROD
		// escalation gets this floating card.
		return m.confirmBody(width, height)
	}
	switch m.state {
	case tui.TaskStateEmpty:
		return components.CenterLines([]string{"no revisions found — the release secrets may have been deleted"}, width, height)
	case tui.TaskStateReady:
		return m.railBody(m.Theme(), width, height)
	case tui.TaskStateLoading:
		style := lipgloss.NewStyle().Foreground(m.Theme().Accent)
		return components.LoadingBody(m.spinner, style, m.feedback, width, height)
	default:
		return components.CenterLines([]string{m.feedback}, width, height)
	}
}

func (m Model) confirmBody(width, height int) string {
	theme := m.Theme()
	title, detail := "Confirm", ""
	if pending := m.actions.Pending(); pending != nil {
		title = pending.Label
		detail = "will run: " + rollbackCommand(pending.Scope.Namespace, pending.Scope.ResourceName, pending.Scope.Revision)
	}
	styles := components.ConfirmStyles{
		Border: lipgloss.NewStyle().Foreground(theme.ConfirmBorder).Background(theme.ConfirmHeaderBg),
		Title:  lipgloss.NewStyle().Foreground(theme.Text).Bold(true).Background(theme.ConfirmHeaderBg),
		Detail: lipgloss.NewStyle().Foreground(theme.TextSecondary).Background(theme.ConfirmHeaderBg),
		Rule:   lipgloss.NewStyle().Foreground(theme.TextGhost).Background(theme.ConfirmHeaderBg),
		Key:    lipgloss.NewStyle().Foreground(theme.Accent).Background(theme.ConfirmHeaderBg),
		Label:  lipgloss.NewStyle().Foreground(theme.TextDim).Background(theme.ConfirmHeaderBg),
	}
	return components.ConfirmCard(title, detail, styles, width, height)
}

var railColumns = []components.Column{
	{Title: "", Min: 2},
	{Title: "REV", Min: 13},
	{Title: "STATUS", Min: 16},
	{Title: "CHART", Min: 22, Flex: true},
	{Title: "UPDATED", Min: 10, Align: components.AlignRight},
}

// railBody renders 16b's revision rail: newest-first, one line per
// revision, the current (index 0) revision's REV cell bright/bold instead
// of the dim treatment every other revision gets — "the current one
// highlighted" (docs/design README.md §16b).
func (m Model) railBody(theme tui.Theme, width, height int) string {
	good := lipgloss.NewStyle().Foreground(theme.Good)
	warn := lipgloss.NewStyle().Foreground(theme.Warn)
	bad := lipgloss.NewStyle().Foreground(theme.Bad)
	neutral := lipgloss.NewStyle().Foreground(theme.Info)
	dim := lipgloss.NewStyle().Foreground(theme.TextDim)
	current := lipgloss.NewStyle().Foreground(theme.Text).Bold(true)

	rows := make([]components.Row, 0, len(m.revisions))
	for i, rev := range m.revisions {
		class := helmStatusClass(rev.Status)
		glyphStyle, glyph := good, tui.GlyphRunning
		switch class {
		case "warn":
			glyphStyle, glyph = warn, tui.GlyphPending
		case "fail":
			glyphStyle, glyph = bad, tui.GlyphFailed
		case "neutral":
			glyphStyle, glyph = neutral, tui.GlyphCompleted
		}
		revStyle := dim
		revText := fmt.Sprintf("%d", rev.Revision)
		if i == 0 {
			revStyle = current
			revText += " (current)"
		}
		updated := "–"
		if !rev.Updated.IsZero() {
			updated = shortAge(time.Since(rev.Updated)) + " ago"
		}
		rows = append(rows, components.Row{Cells: []components.Cell{
			{Text: glyph, Style: glyphStyle},
			{Text: revText, Style: revStyle},
			{Text: rev.StatusCell(), Style: glyphStyle},
			{Text: rev.Chart + " " + rev.ChartVersion, Style: dim},
			{Text: updated, Style: dim},
		}})
	}

	t := components.Table{
		Columns:     railColumns,
		Rows:        rows,
		Selected:    m.selected,
		Offset:      0,
		Width:       width,
		Height:      max(height-2, 1),
		HeaderStyle: lipgloss.NewStyle().Foreground(theme.TextFaint),
		SortStyle:   lipgloss.NewStyle().Foreground(theme.Accent),
		SelBarStyle: lipgloss.NewStyle().Foreground(theme.Accent).Background(theme.SelBg),
		SelRowStyle: lipgloss.NewStyle().Background(theme.SelBg),
		FooterStyle: lipgloss.NewStyle().Foreground(theme.TextGhost),
	}
	return "\n" + t.Render() + "\n" + t.FooterLine(width)
}

// helmStatusClass mirrors resources.helmReleaseStatusClass without an
// import (resources is the caller of this package's sibling, not the other
// way — see model.go's package doc on the seam boundary), returning the
// same three-letter tokens railBody's glyph switch reads.
func helmStatusClass(status string) string {
	switch {
	case status == "deployed":
		return "ok"
	case strings.HasPrefix(status, "pending-"):
		return "warn"
	case status == "failed":
		return "fail"
	default:
		return "neutral"
	}
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

// rollbackCommand mirrors kube.HelmRollbackCommandString without importing
// kube's exec-shelling helm.go for just the string (this view stays
// side-effect-free) — duplicated per the repo's small-pure-helper
// convention.
func rollbackCommand(namespace, name string, toRevision int) string {
	if toRevision > 0 {
		return fmt.Sprintf("helm rollback %s %d -n %s", name, toRevision, namespace)
	}
	return fmt.Sprintf("helm rollback %s -n %s", name, namespace)
}
