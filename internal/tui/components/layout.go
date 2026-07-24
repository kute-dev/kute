package components

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Truncate ellipsizes value to width cells. A value already word-wrapped
// into multiple lines (embedded "\n", e.g. setup's raw-error box after
// lipgloss.Style.Width triggers its own wrap) is truncated line-by-line —
// ansi.StringWidth has no concept of line breaks and would otherwise measure
// the wrapped lines' combined length as one run, truncating the whole block
// down to a bare "…" and silently dropping every line after the first
// (docs/design README.md §4c).
func Truncate(value string, width int) string {
	if strings.Contains(value, "\n") {
		lines := strings.Split(value, "\n")
		for i, l := range lines {
			lines[i] = Truncate(l, width)
		}
		return strings.Join(lines, "\n")
	}
	if width <= 0 {
		return ""
	}
	if ansi.StringWidth(value) <= width {
		return value
	}
	if width <= 3 {
		return ansi.Truncate(value, width, "")
	}
	return ansi.Truncate(value, width, "…")
}

// Pad truncates/pads value to exactly width cells, per line — see
// Truncate's doc comment on multi-line handling.
func Pad(value string, width int) string {
	if strings.Contains(value, "\n") {
		lines := strings.Split(value, "\n")
		for i, l := range lines {
			lines[i] = Pad(l, width)
		}
		return strings.Join(lines, "\n")
	}
	value = Truncate(value, width)
	cellWidth := ansi.StringWidth(value)
	if cellWidth >= width {
		return value
	}
	return value + strings.Repeat(" ", width-cellWidth)
}

func NonColorMarker(active bool) string {
	if active {
		return "▸"
	}
	return " "
}

// CenterLines horizontally centers each line within width and vertically
// centers the whole block by leading with blank lines, leaving the caller
// (Frame's fitBody) to pad the remainder at the bottom — used by empty/error/
// loading bodies (10c, 4b, …) that show a short explainer over an otherwise
// full-height area.
func CenterLines(lines []string, width, height int) string {
	style := lipgloss.NewStyle().Width(width).Align(lipgloss.Center)
	top := (height - len(lines)) / 2
	if top < 0 {
		top = 0
	}
	out := make([]string, 0, top+len(lines))
	blank := style.Render("")
	for i := 0; i < top; i++ {
		out = append(out, blank)
	}
	for _, l := range lines {
		out = append(out, style.Render(l))
	}
	return strings.Join(out, "\n")
}
