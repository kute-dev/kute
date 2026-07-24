package forwardpicker

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/components"
)

// panelWidth is the picker's fixed inner content width, matching 10a's
// execpicker.
const panelWidth = 60

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

	crumbs := []tui.Crumb{
		{Text: "kute", Style: accent},
		{Text: " │ ", Style: ghost},
		{Text: "⇄ forward › ", Style: dim},
		{Text: m.target.Name, Style: text},
	}

	var chip tui.ConnBadge
	if m.session != nil {
		// docs/design README.md §13d: every screen's Header() carries the
		// ambient forward chip — this one and execpicker's own were the
		// two omitting it.
		chip = tui.BuildForwardChip(theme, m.session.ForwardSummary())
	}
	return tui.HeaderState{
		Crumbs:      crumbs,
		UpdateChip:  tui.BuildUpdateChip(theme, m.session),
		ForwardChip: chip,
		Conn:        tui.LiveConnBadge(theme, m.conn, tui.GlyphRunning+" connected"),
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

func (m Model) panelContent(theme tui.Theme) string {
	if m.state == tui.TaskStateError {
		return lipgloss.NewStyle().Foreground(theme.Bad).Render(ellipsize(m.feedback, panelWidth))
	}
	if m.state == tui.TaskStateLoading {
		return lipgloss.NewStyle().Foreground(theme.TextDim).Render(m.feedback)
	}

	var lines []string
	lines = append(lines, m.panelHeader(theme))
	lines = append(lines, "")
	for i, row := range m.rows {
		lines = append(lines, m.portLine(theme, i, row))
	}
	lines = append(lines, "")
	lines = append(lines, m.willRunLine(theme))
	return strings.Join(lines, "\n")
}

func (m Model) panelHeader(theme tui.Theme) string {
	left := lipgloss.NewStyle().Foreground(theme.Accent).Bold(true).Render("⇄ forward › ") +
		lipgloss.NewStyle().Foreground(theme.TextPrimary).Render(m.target.Name)
	kind := strings.ToLower(string(m.target.Kind))
	right := lipgloss.NewStyle().Foreground(theme.TextFaint).Render(fmt.Sprintf("%s · %s · %d ports", kind, m.target.Namespace, len(m.rows)))
	gap := max(panelWidth-lipgloss.Width(left)-lipgloss.Width(right), 1)
	return left + strings.Repeat(" ", gap) + right
}

func (m Model) portLine(theme tui.Theme, i int, row portRow) string {
	selected := i == m.selected
	marker := "  "
	glyphStyle := lipgloss.NewStyle().Foreground(theme.Good)
	nameStyle := lipgloss.NewStyle().Foreground(theme.Text)
	containerStyle := lipgloss.NewStyle().Foreground(theme.TextFaint)
	localStyle := lipgloss.NewStyle().Foreground(theme.TextSecondary)
	if selected {
		bg := theme.SelBg
		marker = lipgloss.NewStyle().Foreground(theme.Accent).Render("▸ ")
		nameStyle = nameStyle.Background(bg).Bold(true)
		containerStyle = containerStyle.Background(bg)
		localStyle = localStyle.Background(bg)
		glyphStyle = glyphStyle.Background(bg)
	}

	label := fmt.Sprintf("%d", row.Port)
	if row.Name != "" {
		label += "/" + row.Name
	}
	name := nameStyle.Render(label)
	if row.Container != "" {
		name += "  " + containerStyle.Render("container "+row.Container)
	}

	local := m.localPortText(row)
	localText := localStyle.Render(local)
	if row.editing {
		cursor := lipgloss.NewStyle().Foreground(theme.Accent).Render(tui.GlyphSelBar)
		localText = lipgloss.NewStyle().Foreground(theme.Text).Render("localhost:"+row.editBuf) + cursor
	}

	left := marker + glyphStyle.Render("●") + " " + name
	gap := max(panelWidth-lipgloss.Width(left)-lipgloss.Width(localText)-1, 1)
	return left + strings.Repeat(" ", gap) + localText
}

func (m Model) localPortText(row portRow) string {
	text := fmt.Sprintf("localhost:%d", row.localPort)
	if row.busyFrom != 0 {
		text = fmt.Sprintf("%d busy → %d", row.busyFrom, row.localPort)
	}
	return text
}

// willRunLine shows the exact kubectl invocation the highlighted port's
// enter key is equivalent to (docs/design README.md §13a: "no magic,
// copyable documentation") — the real dial goes through client-go, not this
// subprocess, but the pod/ports it names are exact.
func (m Model) willRunLine(theme tui.Theme) string {
	label := lipgloss.NewStyle().Foreground(theme.TextGhost).Render("will run  ")
	if m.selected < 0 || m.selected >= len(m.rows) {
		return label
	}
	if m.resolvedPod == "" {
		return label + lipgloss.NewStyle().Foreground(theme.Bad).Render("could not resolve a backing pod")
	}
	row := m.rows[m.selected]
	cmdText := kube.PortForwardCommandString(m.target.Namespace, m.resolvedPod, row.localPort, row.Port)
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
