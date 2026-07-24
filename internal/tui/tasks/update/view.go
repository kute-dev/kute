package update

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/components"
	"github.com/kute-dev/kute/internal/update"
)

func (m Model) View() tea.View { return tea.NewView(m.Render()) }

func (m Model) Render() string { return tui.Frame(m.width, m.height, m) }

func (m Model) Theme() tui.Theme {
	if m.session != nil {
		return m.session.Theme
	}
	return tui.Dark()
}

// Header is "kute │ Update" — no ForwardChip/Conn badge, unlike every
// other screen: 28b's mockup is the one header that omits them, showing
// only the version summary in the right slot (a zero-value ConnBadge
// already renders nothing, per renderHeaderV2).
func (m Model) Header() tui.HeaderState {
	theme := m.Theme()
	accent := lipgloss.NewStyle().Foreground(theme.Accent).Bold(true)
	ghost := lipgloss.NewStyle().Foreground(theme.TextGhost)
	text := lipgloss.NewStyle().Foreground(theme.Text).Bold(true)

	crumbs := []tui.Crumb{
		{Text: "kute", Style: accent},
		{Text: " │ ", Style: ghost},
		{Text: "Update", Style: text},
	}
	return tui.HeaderState{
		Crumbs:   crumbs,
		SyncNote: m.headerRight(theme),
	}
}

func (m Model) headerRight(theme tui.Theme) string {
	dim := lipgloss.NewStyle().Foreground(theme.TextDim)
	cur := lipgloss.NewStyle().Foreground(theme.TextPrimary)
	latestStyle := lipgloss.NewStyle().Foreground(theme.Warn)

	switch m.state() {
	case tui.TaskStateLoading:
		return dim.Render("checking for updates…")
	case tui.TaskStateReady:
		release, _ := m.available()
		return dim.Render("you run ") + cur.Render(m.currentVersion()) +
			dim.Render(" · latest ") + latestStyle.Render(release.Version) +
			dim.Render(" · released "+ageString(release.PublishedAt, m.now()))
	default: // empty
		switch {
		case m.checkDisabled():
			return dim.Render("you run ") + cur.Render(m.currentVersion()) + dim.Render(" · update checks disabled")
		case m.checkErr() != nil:
			return dim.Render("you run ") + cur.Render(m.currentVersion()) + dim.Render(" · couldn't check for updates")
		default:
			return dim.Render("you run ") + cur.Render(m.currentVersion()) + dim.Render(" · up to date")
		}
	}
}

func (m Model) Strips(int) []string { return nil }

func (m Model) Body(width, height int) string {
	theme := m.Theme()
	switch m.state() {
	case tui.TaskStateLoading:
		return components.CenterLines([]string{"checking for updates…"}, width, height)
	case tui.TaskStateReady:
		return m.renderAvailable(theme, width, height)
	default:
		return m.renderEmpty(theme, width, height)
	}
}

// renderEmpty is 28b's empty state: "<current> is the latest" in green plus
// the last-checked timestamp (docs/design README.md §28b) — or one of two
// cases the spec doesn't otherwise name, checked first: update.check
// disabled in config, or the most recent check (Init's own bypass fetch, or
// a prior 'r') resolved with an error.
func (m Model) renderEmpty(theme tui.Theme, width, height int) string {
	switch {
	case m.checkDisabled():
		return m.renderCheckDisabled(theme, width, height)
	case m.checkErr() != nil:
		return m.renderCheckFailed(theme, width, height)
	}
	good := lipgloss.NewStyle().Foreground(theme.Good)
	dim := lipgloss.NewStyle().Foreground(theme.TextDim)

	lines := []string{good.Render(m.currentVersion() + " is the latest")}
	if checked := m.lastChecked(); !checked.IsZero() {
		lines = append(lines, dim.Render("checked "+ageString(checked, m.now())))
	}
	return components.CenterLines(lines, width, height)
}

// renderCheckFailed covers a check (ambient, Init's own bypass fetch, or a
// prior 'r') that resolved with an error — offline, airgapped, GitHub
// unreachable/rate-limited. 28a's chip silently swallows this (docs/design
// README.md §28a: "no chip, no retry storm"), but a panel the user opened
// by hand can't just sit on "checking for updates…" forever with no way
// out, so it surfaces the failure and the same 'r' retry the up-to-date
// empty state offers.
func (m Model) renderCheckFailed(theme tui.Theme, width, height int) string {
	bad := lipgloss.NewStyle().Foreground(theme.Bad)
	dim := lipgloss.NewStyle().Foreground(theme.TextDim)

	lines := []string{bad.Render("couldn't check for updates — offline?")}
	lines = append(lines, dim.Render("r to retry"))
	return components.CenterLines(lines, width, height)
}

// renderCheckDisabled covers update.check: false — no 'r' hint here (unlike
// renderCheckFailed): retrying would just hit the same disabled guard
// updateCheckCmd itself already applies, so there's nothing to invite.
func (m Model) renderCheckDisabled(theme tui.Theme, width, height int) string {
	dim := lipgloss.NewStyle().Foreground(theme.TextDim)
	lines := []string{dim.Render("update checks are disabled")}
	return components.CenterLines(lines, width, height)
}

func (m Model) lastChecked() time.Time {
	if m.session == nil {
		return time.Time{}
	}
	return m.session.State.UpdateCheck.LastChecked
}

// renderAvailable is 28b's main state: the CHANGELOG list plus the bordered
// install-command box. The changelog fills whatever vertical room is left
// once the install box and the blank line above it are budgeted, rather
// than a fixed row cap — a taller terminal shows more of the release's
// actual changelog, only truncating to a "… N more" trailer when the
// entries genuinely don't fit.
func (m Model) renderAvailable(theme tui.Theme, width, height int) string {
	info, _ := m.info()
	installLines := m.installBox(theme, info.Install, width)
	changelogBudget := max(height-len(installLines)-1, 0) // -1: the blank separator line

	var lines []string
	lines = append(lines, m.changelogLines(theme, info.Changelog, width, changelogBudget)...)
	lines = append(lines, "")
	lines = append(lines, installLines...)

	for len(lines) < height {
		lines = append(lines, "")
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	return strings.Join(lines, "\n")
}

// changelogLines renders the CHANGELOG label plus as many entries as fit
// within rowBudget (the label line itself, plus every entry/trailer row —
// renderAvailable computes rowBudget from the actual body height). A "… N
// more" trailer replaces the last row only when the entries don't all fit.
func (m Model) changelogLines(theme tui.Theme, entries []update.ChangelogEntry, width, rowBudget int) []string {
	label := lipgloss.NewStyle().Foreground(theme.TextFaint)
	release, _ := m.available()
	lines := []string{"  " + label.Render("CHANGELOG · "+release.Version)}
	if rowBudget <= 0 {
		return lines
	}

	maxRows := rowBudget - 1 // the label line above already spent one row
	shown := entries
	more := 0
	if len(shown) > maxRows {
		keep := max(maxRows-1, 0) // reserve the last row for the trailer
		shown = entries[:keep]
		more = len(entries) - keep
	}
	for _, e := range shown {
		lines = append(lines, "  "+changelogRow(theme, e.Type, e.Text, width))
	}
	if more > 0 {
		trailer := fmt.Sprintf("… %d more · o opens release notes in browser", more)
		lines = append(lines, "  "+changelogRow(theme, "", trailer, width))
	}
	return lines
}

func changelogTagStyle(theme tui.Theme, kind string) lipgloss.Style {
	switch kind {
	case "fix":
		return lipgloss.NewStyle().Foreground(theme.Bad)
	case "new":
		return lipgloss.NewStyle().Foreground(theme.Good)
	case "perf":
		return lipgloss.NewStyle().Foreground(theme.Info)
	default:
		return lipgloss.NewStyle().Foreground(theme.TextFaint)
	}
}

func changelogRow(theme tui.Theme, kind, text string, width int) string {
	text2 := lipgloss.NewStyle().Foreground(theme.TextPrimary)
	tag := changelogTagStyle(theme, kind)
	tagCol := lipgloss.NewStyle().Width(8).Render(tag.Render(kind))
	rest := components.Truncate(text, max(width-14, 8))
	return tagCol + text2.Render(rest)
}

// installBox is 28b's bordered "installed via" card: a header row
// (manager pill + detection note + "kute never updates itself") over the
// literal command row ("$ <command>" + "y copies · runs in your shell, not
// here").
func (m Model) installBox(theme tui.Theme, install update.InstallInfo, width int) []string {
	// content is the text width inside the box's border, with the same
	// 1-column gutter on both sides a lipgloss Padding(0,1) would add —
	// built by hand (rather than via Style.Width+Padding) so the border
	// wraps tightly around lines already sized exactly right; letting
	// lipgloss apply Width *and* Padding on top of already-sized content
	// double-counts the padding and wraps the last word onto its own line.
	content := max(width-2*tui.FrameInset-4, 20) // -4: border (2) + 1-col gutter each side (2)

	label := lipgloss.NewStyle().Foreground(theme.TextFaint)
	pill := lipgloss.NewStyle().Background(theme.SelBg).Foreground(theme.AccentHi).Bold(true)
	dim := lipgloss.NewStyle().Foreground(theme.TextDim)
	faint := lipgloss.NewStyle().Foreground(theme.TextFaint)
	prompt := lipgloss.NewStyle().Foreground(theme.Accent)
	cmd := lipgloss.NewStyle().Foreground(theme.Text).Bold(true)

	headerLeft := label.Render("installed via") + " " + pill.Render(" "+install.Manager+" ") + " " + dim.Render("detected from the binary path")
	headerLine := " " + padBetween(headerLeft, faint.Render("kute never updates itself"), content) + " "

	cmdLeft := prompt.Render("$") + " " + cmd.Render(install.Command)
	cmdLine := " " + padBetween(cmdLeft, faint.Render("y copies · runs in your shell, not here"), content) + " "

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.BorderPalette).
		Render(headerLine + "\n" + cmdLine)

	rendered := strings.Split(box, "\n")
	out := make([]string, len(rendered))
	margin := strings.Repeat(" ", tui.FrameInset)
	for i, l := range rendered {
		out[i] = margin + l
	}
	return out
}

// padBetween places left-aligned left and right-aligned right within width
// (measuring already-styled/ANSI content) — same shape as every other task
// package's own copy of this helper (events, browse).
func padBetween(left, right string, width int) string {
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return left
	}
	return left + strings.Repeat(" ", gap) + right
}

// ageString renders the complete "2d ago"/"3h ago"/"just now" duration
// phrase shared by the header's "released X" and the empty state's
// "checked X" — callers never append their own trailing "ago" (that would
// double up on the "just now" case). now is always a Model field captured
// at construction time (New), never a live clock read here, per the
// render-purity invariant.
func ageString(t, now time.Time) string {
	if t.IsZero() || now.IsZero() {
		return "some time ago"
	}
	d := max(now.Sub(t), 0)
	if d < time.Minute {
		return "just now"
	}
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
