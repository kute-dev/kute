package components

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Compose dims base — every line re-rendered plain through dimStyle, since
// terminals can't do real opacity (mvp-plan.md §0.5; dimStyle is built by
// the caller from Theme.TextGhost) — and splices panel on top, horizontally
// centered, anchored at row top (clamped so the panel stays on screen). A
// fixed anchor rather than vertical centering keeps the panel from jumping
// as its height changes with each keystroke's result count — the mockups
// hang the palette a couple of rows below the header, not mid-screen.
// Splicing is line-by-line and ANSI-aware (github.com/charmbracelet/x/ansi),
// not lipgloss layering, so panel keeps its own colors while the backdrop
// reads as dimmed.
func Compose(base, panel string, width, height, top int, dimStyle lipgloss.Style) string {
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = 1
	}

	baseLines := fitLines(strings.Split(base, "\n"), width, height)
	panelLines := strings.Split(panel, "\n")
	panelWidth := 0
	for _, l := range panelLines {
		if w := ansi.StringWidth(l); w > panelWidth {
			panelWidth = w
		}
	}
	panelHeight := len(panelLines)

	top = max(min(top, height-panelHeight), 0)
	left := max((width-panelWidth)/2, 0)

	out := make([]string, height)
	for i, line := range baseLines {
		dimmed := dimStyle.Render(ansi.Strip(line))
		if i < top || i >= top+panelHeight {
			out[i] = dimmed
			continue
		}
		out[i] = spliceLine(dimmed, panelLines[i-top], left, width)
	}
	return strings.Join(out, "\n")
}

// cornerMargin is ComposeCorner's fixed inset from the bottom-right edge —
// matches FrameInset's spirit (chrome content never touches the terminal
// edge) without importing tui just for the constant.
const cornerMargin = 2

// ComposeCorner splices panel onto base's bottom-right corner, ANSI-aware,
// with no dimming of the rest of base — unlike Compose, which is built for a
// dimmed backdrop + centered modal. The one caller today is the --keycast
// chip (mvp-plan.md's keycast mode), which must stay visible over any other
// overlay Compose already spliced in, so it composites last and never dims
// what's underneath it.
func ComposeCorner(base, panel string, width, height int) string {
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = 1
	}
	if panel == "" {
		return base
	}

	baseLines := fitLines(strings.Split(base, "\n"), width, height)
	panelWidth := ansi.StringWidth(panel)

	row := height - 1 - cornerMargin
	if row < 0 || row >= height {
		return strings.Join(baseLines, "\n")
	}
	left := max(width-cornerMargin-panelWidth, 0)

	baseLines[row] = spliceLine(baseLines[row], panel, left, width)
	return strings.Join(baseLines, "\n")
}

// fitLines pads/truncates lines to exactly height rows, each Pad-ed to
// width, so Compose always returns a rectangular block.
func fitLines(lines []string, width, height int) []string {
	out := make([]string, height)
	for i := range out {
		if i < len(lines) {
			out[i] = Pad(lines[i], width)
			continue
		}
		out[i] = Pad("", width)
	}
	return out
}

// spliceLine overlays panelLine onto base starting at column left,
// ANSI-aware, and pads the result back out to width.
func spliceLine(base, panelLine string, left, width int) string {
	panelWidth := ansi.StringWidth(panelLine)
	line := ansi.Cut(base, 0, left) + panelLine
	if rightStart := left + panelWidth; rightStart < width {
		line += ansi.Cut(base, rightStart, width)
	}
	return Pad(line, width)
}
