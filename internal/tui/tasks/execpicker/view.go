package execpicker

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

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
	dim := lipgloss.NewStyle().Foreground(theme.TextDim)
	ghost := lipgloss.NewStyle().Foreground(theme.TextGhost)
	ghost2 := lipgloss.NewStyle().Foreground(theme.TextGhost2)
	text := lipgloss.NewStyle().Foreground(theme.Text).Bold(true)

	ctxName := "cluster unavailable"
	if m.session != nil && m.session.Location.Context != "" {
		ctxName = m.session.Location.Context
	}

	crumbs := append(tui.BrandCrumbs(theme),
		tui.Crumb{Text: " │ ", Style: ghost2},
		tui.Crumb{Text: ctxName, Style: dim},
		tui.Crumb{Text: " › ", Style: ghost},
		tui.Crumb{Text: "Pods", Style: dim},
		tui.Crumb{Text: " › ", Style: ghost},
		tui.Crumb{Text: m.podName, Style: text},
	)

	var chip tui.ConnBadge
	if m.session != nil {
		// docs/design README.md §13d: every screen's Header() carries the
		// ambient forward chip — this one and forwardpicker's own were the
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

// panelContent builds 10a's picker panel: an inner header ("exec › pod" +
// container count), the container list — name/state/shells on one line, the
// image on its own line right below (so a long image never pushes STATUS/
// SHELLS out of their fixed columns on the name line) — then either the
// "will run" documentation line for the highlighted container or — when
// that container is already known to have no shell — §41a's "no sh or
// bash" note in its place (a kubectl exec preview would be a lie there), and
// a blank feedback line reserved for a non-zero exec exit (kept even when
// empty so the panel's height doesn't jump between attempts).
func (m Model) panelContent(theme tui.Theme) string {
	var lines []string
	lines = append(lines, m.panelHeader(theme))
	lines = append(lines, "")
	for i, c := range m.containers {
		lines = append(lines, m.containerLines(theme, i, c)...)
	}
	lines = append(lines, "")
	if m.selectedShellless() {
		lines = append(lines, m.noShellLine(theme)...)
	} else {
		lines = append(lines, m.willRunLine(theme))
	}
	if m.feedback != "" {
		lines = append(lines, "", lipgloss.NewStyle().Foreground(theme.Bad).Render(ellipsize(m.feedback, panelWidth)))
	}
	return strings.Join(lines, "\n")
}

// panelHeader truncates the pod name so "exec › <pod>" plus the right-hand
// count never exceeds panelWidth — every other line in the panel (container
// rows, will-run, feedback) is already clamped to panelWidth, and Body's
// panelStyle carries no explicit Width, so lipgloss sizes the card to its
// widest line. A long pod name left unclamped here (real StatefulSet pods
// routinely run past 40 characters) was the one line that could stretch the
// whole card past panelWidth, leaving every fixed-width row below it short
// with blank space trailing under "N containers".
func (m Model) panelHeader(theme tui.Theme) string {
	const labelText = "exec › "
	right := lipgloss.NewStyle().Foreground(theme.TextFaint).Render(fmt.Sprintf("%d containers", len(m.containers)))
	avail := panelWidth - lipgloss.Width(labelText) - lipgloss.Width(right) - 1 // reserve >=1 gap
	podName := components.Truncate(m.podName, max(avail, 1))
	left := lipgloss.NewStyle().Foreground(theme.Accent).Bold(true).Render(labelText) +
		lipgloss.NewStyle().Foreground(theme.TextPrimary).Render(podName)
	gap := max(panelWidth-lipgloss.Width(left)-lipgloss.Width(right), 1)
	return left + strings.Repeat(" ", gap) + right
}

// Container row columns — fixed widths so STATUS and SHELLS start at the
// same column on every row regardless of how long a container's name or
// status/shells text happens to be (docs/design v.0.11.0.dc.html §41a:
// container statuses "one under another"). Only the name column flexes/
// truncates on the first line; marker(2) + nameColWidth + 2 + statusColWidth
// + 2 + shellsColWidth sums to panelWidth. The image gets its own indented
// line below the name so a long image reference never crowds STATUS/SHELLS
// off their columns.
const (
	nameColWidth   = 26
	statusColWidth = 13
	shellsColWidth = 11
	imageIndent    = 2
)

// containerLines renders one container as two rows: name · state · shells,
// then the image indented to align under the name. Both lines share the
// same selection background so the highlighted container still reads as one
// block.
func (m Model) containerLines(theme tui.Theme, i int, c kube.ContainerInfo) []string {
	selected := i == m.selected
	// rowFill paints the row's gap cells with the row's own background —
	// SelBg when selected, transparent (BgPalette is empty) otherwise —
	// because an outer Background wrap is cancelled by each inner span's
	// ANSI reset, leaving plain spaces to fall back to the terminal's
	// background (palette's fillSpaces is the same convention).
	var rowFill lipgloss.Style
	marker := "  "
	nameStyle := lipgloss.NewStyle().Foreground(theme.Text)
	imgStyle := lipgloss.NewStyle().Foreground(theme.TextFaint)
	stateStyle := lipgloss.NewStyle().Foreground(theme.Good)
	shellsText := m.shellsText(c.Name)
	shellStyle := lipgloss.NewStyle().Foreground(theme.TextDim)
	if shellsText == "no shell" {
		shellStyle = lipgloss.NewStyle().Foreground(theme.Warn)
	}
	if selected {
		rowFill = lipgloss.NewStyle().Background(theme.SelBg)
		marker = lipgloss.NewStyle().Foreground(theme.Accent).Background(theme.SelBg).Render("▸ ")
		bg := theme.SelBg
		nameStyle = nameStyle.Background(bg).Bold(true)
		imgStyle = imgStyle.Background(bg)
		stateStyle = stateStyle.Background(bg)
		shellStyle = shellStyle.Background(bg)
	}
	fill := func(n int) string {
		if n <= 0 {
			return ""
		}
		if selected {
			return rowFill.Render(strings.Repeat(" ", n))
		}
		return strings.Repeat(" ", n)
	}

	glyph, text := "●", "running"
	if c.State != "" && c.State != "Running" {
		glyph, stateStyle = "▲", stateStyle.Foreground(theme.Warn)
		text = strings.ToLower(c.State)
		if c.Reason != "" {
			text += " · " + strings.ToLower(c.Reason)
		}
	}

	name := components.Truncate(c.Name, nameColWidth)
	nameWidth := lipgloss.Width(name)
	state := components.Truncate(glyph+" "+text, statusColWidth)
	shells := components.Truncate(shellsText, shellsColWidth)

	nameLine := marker + nameStyle.Render(name) + fill(nameColWidth-nameWidth) + fill(2) +
		stateStyle.Render(state) + fill(statusColWidth-lipgloss.Width(state)) + fill(2) +
		fill(shellsColWidth-lipgloss.Width(shells)) + shellStyle.Render(shells)

	img := c.Image
	if c.IsSidecar {
		img += " sidecar"
	}
	imgAvail := panelWidth - imageIndent
	img = truncateImageRef(img, imgAvail)
	imgLine := fill(imageIndent) + imgStyle.Render(img) + fill(imgAvail-lipgloss.Width(img))

	return []string{nameLine, imgLine}
}

// truncateImageRef fits an image reference into width cells for the picker's
// dedicated image line. A digest suffix (`@sha256:<64 hex chars>`, 71
// characters no one reads at this width) is dropped whenever the reference
// also carries an explicit tag — the tag is the version signal a reader
// actually wants, and keeping both just to truncate one of them away is
// worse than dropping the redundant one outright. What's left is ellipsized
// from the front rather than the back: a long registry/repo path is the part
// worth eliding, since the tag (or, lacking one, the digest) at the end is
// what answers "which version is this".
func truncateImageRef(img string, width int) string {
	if at := strings.Index(img, "@sha256:"); at >= 0 {
		if repo := img[:at]; strings.Contains(repo[strings.LastIndex(repo, "/")+1:], ":") {
			img = repo
		}
	}
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(img) <= width {
		return img
	}
	r := []rune(img)
	if width <= 1 {
		return "…"
	}
	return "…" + string(r[len(r)-(width-1):])
}

// shellsText renders one row's right-aligned shells cell (docs/design
// README.md §10a: "detected shells right-aligned — bash preferred"). Four
// honest states, since a guess here would contradict the will-run line:
// "checking…" while the probe is in flight, the detected shells in
// preference order, "no shell" for a container that genuinely has none
// (distroless — worth knowing before pressing enter), and "–" when the probe
// couldn't run at all (no kubectl, no pods/exec permission, connection down).
func (m Model) shellsText(container string) string {
	res, ok := m.detected[container]
	switch {
	case m.shells == nil || (ok && res.err != nil):
		return "–"
	case !ok:
		return "checking…"
	case len(res.shells) == 0:
		return "no shell"
	default:
		return strings.Join(res.shells, ", ")
	}
}

// willRunLine shows the exact kubectl command the highlighted container's
// enter key will run (docs/design README.md §10a: "no magic, copyable
// documentation"). A command longer than the panel wraps onto continuation
// lines indented under the label rather than ellipsizing, matching
// debugpanel's willRunLine — the command is meant to be copyable, and
// truncating it silently drops real content (e.g. the shell path on a long
// namespace/pod/container name).
func (m Model) willRunLine(theme tui.Theme) string {
	const labelText = "will run  "
	label := lipgloss.NewStyle().Foreground(theme.TextGhost).Render(labelText)
	if m.selected < 0 || m.selected >= len(m.containers) {
		return label
	}
	container := m.containers[m.selected].Name
	cmdText := kube.ExecCommandString(m.namespace, m.podName, container, m.preferredShell(m.selected))
	cmdStyle := lipgloss.NewStyle().Foreground(theme.TextSecondary)
	indent := strings.Repeat(" ", lipgloss.Width(labelText))
	wrapped := components.Wrap(cmdText, panelWidth-lipgloss.Width(labelText))
	lines := make([]string, len(wrapped))
	for i, l := range wrapped {
		prefix := label
		if i > 0 {
			prefix = indent
		}
		lines[i] = prefix + cmdStyle.Render(strings.TrimRight(l, " "))
	}
	return strings.Join(lines, "\n")
}

// noShellLine replaces willRunLine when the highlighted container is
// already known to have no shell (docs/design v.0.11.0.dc.html §41a: a
// kubectl exec preview would be a lie there) — the fork's own explanation:
// which container has nothing to exec into, and that debug attaches a shell
// alongside it rather than replacing anything.
func (m Model) noShellLine(theme tui.Theme) []string {
	container := m.selectedContainerName()
	warn := lipgloss.NewStyle().Foreground(theme.Warn)
	dim := lipgloss.NewStyle().Foreground(theme.TextDim)
	notice := fmt.Sprintf("no sh or bash in %s — nothing to exec into", container)
	explain := "debug attaches a shell alongside it, sharing the pod's process namespace"
	var lines []string
	for _, l := range components.Wrap(notice, panelWidth) {
		lines = append(lines, warn.Render(strings.TrimRight(l, " ")))
	}
	for _, l := range components.Wrap(explain, panelWidth) {
		lines = append(lines, dim.Render(strings.TrimRight(l, " ")))
	}
	return lines
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
