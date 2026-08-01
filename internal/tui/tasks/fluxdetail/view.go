package fluxdetail

import (
	"fmt"
	"image/color"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

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
	}
	if m.namespace != "" {
		crumbs = append(crumbs,
			tui.Crumb{Text: " › ", Style: ghost},
			tui.Crumb{Text: m.namespace, Style: lipgloss.NewStyle().Foreground(theme.Accent)})
	}
	crumbs = append(crumbs,
		tui.Crumb{Text: " › ", Style: ghost},
		tui.Crumb{Text: m.desc.Display, Style: dim},
		tui.Crumb{Text: " › ", Style: ghost},
		tui.Crumb{Text: m.name, Style: text})

	return tui.HeaderState{
		Crumbs:      crumbs,
		UpdateChip:  tui.BuildUpdateChip(theme, m.session),
		ForwardChip: tui.BuildForwardChip(theme, m.session.ForwardSummary()),
		Conn:        tui.LiveConnBadge(theme, m.conn, tui.GlyphRunning+" connected"),
	}
}

func (m Model) Strips(width int) []string { return nil }

func (m Model) Body(width, height int) string {
	switch m.state {
	case tui.TaskStateReady:
		return m.readyBody(width)
	case tui.TaskStateLoading:
		style := lipgloss.NewStyle().Foreground(m.Theme().Accent)
		return components.LoadingBody(m.spinner, style, m.feedback, width, height)
	default:
		return components.CenterLines([]string{m.feedback}, width, height)
	}
}

// readyBody stacks §31a's three bands in the design's order: failure card,
// chain, inventory. 5a's promotion rule — the reason it is broken outranks
// everything else on the screen.
func (m Model) readyBody(width int) string {
	var b strings.Builder
	if card := m.failureCard(width); card != "" {
		b.WriteString(card)
		b.WriteString("\n\n")
	}
	b.WriteString(m.chainGrid(width))
	if inv := m.inventoryBlock(width); inv != "" {
		b.WriteString("\n")
		b.WriteString(inv)
	}
	return b.String()
}

// failureCard is §31a's top band — 4a's red-tint banner idiom, not a
// destructive-confirm border (red *borders* stay reserved for those).
func (m Model) failureCard(width int) string {
	if m.fail == nil {
		if m.suspended {
			return m.suspendedCard(width)
		}
		return ""
	}
	theme := m.Theme()
	head := lipgloss.NewStyle().Foreground(theme.Bad).Bold(true)
	body := lipgloss.NewStyle().Foreground(theme.BadMuted)
	hint := lipgloss.NewStyle().Foreground(theme.TextDim)

	title := "RECONCILE FAILED"
	if m.fail.Reason != "" {
		title += " · " + m.fail.Reason
	}
	if !m.fail.Since.IsZero() {
		title += " · " + shortAge(m.now.Sub(m.fail.Since)) + " ago"
	}
	if retry := m.retryIn(); retry != "" {
		title += " · retry in " + retry
	}

	lines := []string{head.Render(title)}
	// Verbatim — §31a and §14d both: the message text IS the diagnosis.
	for _, l := range wrap(m.fail.Message, width-4) {
		lines = append(lines, body.Render(l))
	}
	if m.fail.CulpritName != "" {
		lead := fmt.Sprintf("%s opens %s/%s", tui.GlyphExpand, m.fail.Culprit, m.fail.CulpritName)
		if m.fail.Detail != "" {
			lead += " · " + m.fail.Detail
		}
		lines = append(lines, hint.Render(lead))
	}
	return strings.Join(lines, "\n")
}

// suspendedCard is the paused counterpart: not a failure, but the one thing
// worth saying above everything else when reconciliation is off.
func (m Model) suspendedCard(width int) string {
	theme := m.Theme()
	head := lipgloss.NewStyle().Foreground(theme.Warn).Bold(true)
	body := lipgloss.NewStyle().Foreground(theme.Muted.Warn)
	lines := []string{head.Render("SUSPENDED · reconciliation is paused")}
	if drift := m.driftNote(); drift != "" {
		lines = append(lines, body.Render(drift))
	}
	return strings.Join(lines, "\n")
}

// retryIn renders the countdown to Flux's next attempt. Computed from
// m.now, which a tick refreshes — never a clock read inside render.
func (m Model) retryIn() string {
	if m.fail == nil || m.fail.RetryAt.IsZero() {
		return ""
	}
	d := m.fail.RetryAt.Sub(m.now)
	if d <= 0 {
		return "now"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm %02ds", int(d.Minutes()), int(d.Seconds())%60)
}

// driftNote states whether what is applied matches what the source offers.
// A boolean, never a commit count: counting commits needs git log, and kute
// reads no git remote (docs/flux-plan.md G1).
func (m Model) driftNote() string {
	if m.chn.SourceRevision == "" || m.chn.AppliedRevision == "" || !m.chn.DriftComparable {
		// Not comparable is not "in sync": a HelmRelease records a chart
		// version where its source records an index digest, so the only
		// honest answer is no answer (kube.FluxTracksSourceRevision).
		return ""
	}
	if m.chn.SourceRevision == m.chn.AppliedRevision {
		return "in sync with " + m.chn.SourceDisplay
	}
	return "source ahead · " + m.chn.SourceDisplay + " is at " + m.chn.SourceRevision
}

// chainGrid is §31a's middle band: what this object is wired to, and
// whether what it applied is what the source offers.
func (m Model) chainGrid(width int) string {
	theme := m.Theme()
	key := lipgloss.NewStyle().Foreground(theme.TextFaint)
	val := lipgloss.NewStyle().Foreground(theme.TextSecondary)
	good := lipgloss.NewStyle().Foreground(theme.Good)
	warn := lipgloss.NewStyle().Foreground(theme.Warn)

	row := func(k, v string) string {
		return key.Render(pad(k, 18)) + v
	}

	var lines []string
	if m.chn.SourceDisplay != "" {
		parts := []string{m.chn.SourceDisplay}
		if m.chn.Path != "" {
			parts = append(parts, m.chn.Path)
		}
		if m.chn.Interval != "" {
			parts = append(parts, "interval "+m.chn.Interval)
		}
		lines = append(lines, row("source", val.Render(strings.Join(parts, " · "))))
	}
	if m.chn.SourceRevision != "" {
		lines = append(lines, row("source revision", val.Render(m.chn.SourceRevision)))
	}
	if m.chn.AppliedRevision != "" {
		applied := val.Render(m.chn.AppliedRevision)
		switch {
		// Not comparable earns no verdict at all — neither "in sync" nor
		// "source ahead". A HelmRelease's applied revision is a chart
		// version, its source's is an index digest
		// (kube.FluxTracksSourceRevision).
		case m.chn.SourceRevision == "" || !m.chn.DriftComparable:
		case m.chn.AppliedRevision == m.chn.SourceRevision:
			// §31a's line that kills the most common Flux misdiagnosis:
			// applied-but-unhealthy is not the same failure as not-applied.
			applied += good.Render(" · in sync")
			if m.fail != nil {
				applied += val.Render(" — applied, failing health checks")
			}
		default:
			applied += warn.Render(" · source ahead")
		}
		lines = append(lines, row("applied revision", applied))
	}
	for i, dep := range m.chn.DependsOn {
		label := "depends on"
		if i > 0 {
			label = ""
		}
		text := val.Render(dep.Name)
		if dep.Suspended {
			text += warn.Render(" · suspended")
		}
		lines = append(lines, row(label, text))
	}
	return strings.Join(lines, "\n")
}

// inventoryBlock is §31a's third band: what this reconciler manages,
// unhealthy first with the healthy tail folded.
func (m Model) inventoryBlock(width int) string {
	theme := m.Theme()
	label := lipgloss.NewStyle().Foreground(theme.TextFaint)
	faint := lipgloss.NewStyle().Foreground(theme.TextFaint)

	if m.chn.InventoryMissing != "" {
		return label.Render("INVENTORY") + "\n" + faint.Render("  "+m.chn.InventoryMissing)
	}
	if len(m.inventory) == 0 {
		return ""
	}

	notReady := 0
	for _, it := range m.inventory {
		if it.Status == resources.StatusFail {
			notReady++
		}
	}
	head := fmt.Sprintf("INVENTORY · %d OBJECTS", len(m.inventory))
	if notReady > 0 {
		head += fmt.Sprintf(" · %d NOT READY", notReady)
	}

	lines := []string{label.Render(head)}
	for i, it := range m.visibleInventory() {
		lines = append(lines, m.inventoryRow(it, i == m.selected))
	}
	if folded := m.foldedCount(); folded > 0 {
		lines = append(lines, faint.Render(fmt.Sprintf("  + %d ready · %s expand", folded, tui.GlyphTab)))
	}
	return strings.Join(lines, "\n")
}

func (m Model) inventoryRow(it inventoryItem, selected bool) string {
	theme := m.Theme()
	glyphStyle := lipgloss.NewStyle().Foreground(statusColor(theme, it.Status))
	nameStyle := lipgloss.NewStyle().Foreground(theme.Text)
	kindStyle := lipgloss.NewStyle().Foreground(theme.TextDim)
	readyStyle := lipgloss.NewStyle().Foreground(statusColor(theme, it.Status))

	bar := "  "
	if selected {
		bar = lipgloss.NewStyle().Foreground(theme.Accent).Render(tui.GlyphSelBar) + " "
		nameStyle = nameStyle.Bold(true)
	}
	return bar + glyphStyle.Render(it.Glyph) + " " +
		nameStyle.Render(pad(it.Name, 32)) +
		kindStyle.Render(pad(string(it.Kind), 18)) +
		readyStyle.Render(it.Ready)
}

func statusColor(theme tui.Theme, c resources.StatusClass) color.Color {
	switch c {
	case resources.StatusOK:
		return theme.Good
	case resources.StatusWarn:
		return theme.Warn
	case resources.StatusFail:
		return theme.Bad
	default:
		return theme.TextFaint
	}
}

func pad(s string, w int) string {
	if len(s) >= w {
		if w > 1 {
			return s[:w-1] + " "
		}
		return s
	}
	return s + strings.Repeat(" ", w-len(s))
}

func wrap(s string, width int) []string {
	if width <= 0 || len(s) <= width {
		return []string{s}
	}
	var out []string
	for len(s) > width {
		cut := strings.LastIndex(s[:width], " ")
		if cut <= 0 {
			cut = width
		}
		out = append(out, strings.TrimSpace(s[:cut]))
		s = strings.TrimSpace(s[cut:])
	}
	if s != "" {
		out = append(out, s)
	}
	return out
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
