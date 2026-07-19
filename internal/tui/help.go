package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/kute-dev/kute/internal/tui/components"
)

// This file renders the 7b help overlay (docs/design README.md §7b): a
// floating ~79%-width panel over the dimmed table, three columns — the
// active screen's own keybar groups (so it always matches what's on
// screen, mvp-plan.md §0.4), plus the fixed SCOPE and GLOBAL columns.

// helpWidth is the panel's outer width: ~79% of the screen, floored/capped
// like palette.Width.
func helpWidth(screenWidth int) int {
	return min(max(int(float64(screenWidth)*0.79), 40), screenWidth)
}

// flattenHints joins a Keybar's groups into one column, in order.
func flattenHints(groups [][]KeyHint) []KeyHint {
	var out []KeyHint
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

// renderHelp draws the 7b panel for the currently active Screen. scope/global
// are Session.HelpScope/HelpGlobal (built at the composition root from the
// verbs registry — see session.go's doc comment on those fields).
func renderHelp(theme Theme, view Screen, scope, global []KeyHint, screenWidth int) string {
	width := helpWidth(screenWidth)
	frameWidth := max(width-2, 20)

	fg := func(c lipgloss.Color) lipgloss.Style {
		return lipgloss.NewStyle().Foreground(c).Background(theme.BgPalette)
	}
	accent := fg(theme.Accent).Bold(true)
	headStyle := fg(theme.AccentHi).Bold(true)
	keyStyle := fg(theme.Accent)
	labelStyle := fg(theme.TextDim)
	faint := fg(theme.TextFaint)
	fill := fg(theme.BgPalette) // background-only, for pads/gaps

	kb := view.Keybar()
	viewTitle := strings.ToUpper(kb.PillText)
	if viewTitle == "" {
		viewTitle = "VIEW"
	}

	title := accent.Render("? help") + "  " + faint.Render(fmt.Sprintf("keys for %s view · globals below", viewTitle))
	// TextGhost, not BorderSubtle — matches chrome.go's own band rules and
	// palette's Rule; BorderSubtle reads as too dark without a dialog
	// background fill behind it (BgPalette's the empty lipgloss.Color).
	rule := fg(theme.TextGhost).Render(strings.Repeat("─", frameWidth))

	colGap := 3
	// innerWidth is the row's real budget: helpInset reserves a 1-cell
	// margin on each side, so columns sized against frameWidth itself came
	// out two cells too wide and got truncated with a stray "…" (7b: help.go
	// docs/design README.md §7b).
	innerWidth := frameWidth - 2
	colWidth := max((innerWidth-2*colGap)/3, 12)
	cols := [][]string{
		helpColumn(viewTitle+" VIEW", flattenHints(kb.Groups), headStyle, keyStyle, labelStyle, fill, colWidth),
		helpColumn("SCOPE", scope, headStyle, keyStyle, labelStyle, fill, colWidth),
		helpColumn("GLOBAL", global, headStyle, keyStyle, labelStyle, fill, colWidth),
	}
	rows := 0
	for _, c := range cols {
		rows = max(rows, len(c))
	}
	for i, c := range cols {
		for len(c) < rows {
			c = append(c, helpFill(fill, colWidth))
		}
		cols[i] = c
	}

	gap := helpFill(fill, colGap)
	lines := []string{helpInset(title, frameWidth, fill), rule}
	for r := range rows {
		lines = append(lines, helpInset(cols[0][r]+gap+cols[1][r]+gap+cols[2][r], frameWidth, fill))
	}
	lines = append(lines, rule)
	closeHint := keyStyle.Render("esc") + labelStyle.Render(" close")
	closeGap := max(frameWidth-2-lipgloss.Width(closeHint), 0)
	lines = append(lines, helpInset(helpFill(fill, closeGap)+closeHint, frameWidth, fill))

	frame := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.BorderPalette).
		BorderBackground(theme.BgPalette).
		Background(theme.BgPalette).
		Width(frameWidth)
	return frame.Render(strings.Join(lines, "\n"))
}

// helpColumn renders one 7b column: an uppercase heading followed by up to
// one row per hint, each padded to width so every column in a row lines up.
func helpColumn(heading string, hints []KeyHint, headStyle, keyStyle, labelStyle, fill lipgloss.Style, width int) []string {
	lines := []string{helpPad(headStyle.Render(strings.ToUpper(heading)), width, fill)}
	for _, h := range hints {
		style := keyStyle
		if h.Disabled {
			style = labelStyle
		}
		line := style.Render(h.Key) + labelStyle.Render(" "+h.Label)
		lines = append(lines, helpPad(line, width, fill))
	}
	return lines
}

func helpFill(fill lipgloss.Style, n int) string {
	if n <= 0 {
		return ""
	}
	return fill.Render(strings.Repeat(" ", n))
}

// helpPad truncates/pads styled content to exactly width cells, padding
// through fill so it carries the panel background (an outer wrap can't fix
// a bare space's missing background — each span's ANSI reset cancels it).
func helpPad(content string, width int, fill lipgloss.Style) string {
	content = components.Truncate(content, width)
	return content + helpFill(fill, width-lipgloss.Width(content))
}

// helpInset adds the panel's 1-cell side margins to a row already padded to
// width-2.
func helpInset(content string, width int, fill lipgloss.Style) string {
	return helpFill(fill, 1) + helpPad(content, width-2, fill) + helpFill(fill, 1)
}
