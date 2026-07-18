package components

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func Truncate(value string, width int) string {
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

func Pad(value string, width int) string {
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
