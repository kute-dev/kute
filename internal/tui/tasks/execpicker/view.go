package execpicker

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/components"
)

// panelWidth is the picker's fixed inner content width — mockup 10a's panel
// sizes to its own content rather than the surrounding terminal.
const panelWidth = 56

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
		{Text: "Pods", Style: dim},
		{Text: " › ", Style: ghost},
		{Text: m.podName, Style: text},
	}

	return tui.HeaderState{
		Crumbs: crumbs,
		Conn:   tui.LiveConnBadge(theme, m.conn, tui.GlyphRunning+" connected"),
	}
}

func (m Model) Strips(width int) []string { return nil }

func (m Model) Body(width, height int) string {
	theme := m.Theme()
	panelStyle := lipgloss.NewStyle().
		Foreground(theme.TextPrimary).
		Background(theme.BgPalette).
		BorderForeground(theme.BorderPalette).
		Padding(1, 2)
	return components.Card(m.panelContent(theme), panelStyle, width, height)
}

// panelContent builds 10a's picker panel: an inner header ("exec › pod" +
// container count), the container list, the "will run" documentation line
// for the highlighted container, and a blank feedback line reserved for a
// non-zero exec exit (kept even when empty so the panel's height doesn't
// jump between attempts).
func (m Model) panelContent(theme tui.Theme) string {
	var lines []string
	lines = append(lines, m.panelHeader(theme))
	lines = append(lines, "")
	for i, c := range m.containers {
		lines = append(lines, m.containerLine(theme, i, c))
	}
	lines = append(lines, "")
	lines = append(lines, m.willRunLine(theme))
	if m.feedback != "" {
		lines = append(lines, "", lipgloss.NewStyle().Foreground(theme.Bad).Render(ellipsize(m.feedback, panelWidth)))
	}
	return strings.Join(lines, "\n")
}

func (m Model) panelHeader(theme tui.Theme) string {
	left := lipgloss.NewStyle().Foreground(theme.Accent).Bold(true).Render("exec › ") +
		lipgloss.NewStyle().Foreground(theme.TextPrimary).Render(m.podName)
	right := lipgloss.NewStyle().Foreground(theme.TextFaint).Render(fmt.Sprintf("%d containers", len(m.containers)))
	gap := max(panelWidth-lipgloss.Width(left)-lipgloss.Width(right), 1)
	return left + strings.Repeat(" ", gap) + right
}

func (m Model) containerLine(theme tui.Theme, i int, c kube.ContainerInfo) string {
	selected := i == m.selected
	marker := "  "
	nameStyle := lipgloss.NewStyle().Foreground(theme.Text)
	imgStyle := lipgloss.NewStyle().Foreground(theme.TextFaint)
	stateStyle := lipgloss.NewStyle().Foreground(theme.Good)
	shellStyle := lipgloss.NewStyle().Foreground(theme.TextDim)
	if selected {
		marker = lipgloss.NewStyle().Foreground(theme.Accent).Render("▸ ")
		bg := theme.SelBg
		nameStyle = nameStyle.Background(bg).Bold(true)
		imgStyle = imgStyle.Background(bg)
		stateStyle = stateStyle.Background(bg)
		shellStyle = shellStyle.Background(bg)
	}

	glyph, text := "●", "running"
	if c.State != "" && c.State != "Running" {
		glyph, stateStyle = "◐", stateStyle.Foreground(theme.Warn)
		text = strings.ToLower(c.State)
		if c.Reason != "" {
			text += " · " + strings.ToLower(c.Reason)
		}
	}

	name := nameStyle.Render(c.Name) + "  " + imgStyle.Render(c.Image)
	state := stateStyle.Render(glyph + " " + text)
	shells := shellStyle.Render("sh, bash")

	left := marker + name
	gap := max(panelWidth-lipgloss.Width(left)-lipgloss.Width(state)-lipgloss.Width(shells)-2, 1)
	return left + strings.Repeat(" ", gap) + state + "  " + shells
}

// willRunLine shows the exact kubectl command the highlighted container's
// enter key will run (docs/design README.md §10a: "no magic, copyable
// documentation").
func (m Model) willRunLine(theme tui.Theme) string {
	label := lipgloss.NewStyle().Foreground(theme.TextGhost).Render("will run  ")
	if m.selected < 0 || m.selected >= len(m.containers) {
		return label
	}
	container := m.containers[m.selected].Name
	cmdText := kube.ExecCommandString(m.namespace, m.podName, container, "")
	return label + lipgloss.NewStyle().Foreground(theme.TextSecondary).Render(ellipsize(cmdText, panelWidth-10))
}

func ellipsize(s string, width int) string {
	if width <= 1 || lipgloss.Width(s) <= width {
		return s
	}
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	return string(r[:width-1]) + "…"
}
