package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

const (
	DefaultWidth  = 80
	DefaultHeight = 24
	HeaderHeight  = 1
	LegendHeight  = 1
)

type Size struct {
	Width  int
	Height int
}

func NormalizeSize(width, height int) Size {
	if width <= 0 {
		width = DefaultWidth
	}
	if height <= 0 {
		height = DefaultHeight
	}

	return Size{Width: width, Height: height}
}

func BodyHeight(height int) int {
	available := NormalizeSize(DefaultWidth, height).Height - HeaderHeight - LegendHeight
	if available < 1 {
		return 1
	}

	return available
}

// Truncate shortens value to a visible width of width, ignoring any ANSI
// escape sequences so colored strings measure by their rendered width.
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

	return ansi.Truncate(value, width, "...")
}

// PadLine right-pads value to a visible width of width. Width is measured with
// ANSI escapes stripped, so already-colored, full-width lines pass through
// unchanged instead of being miscounted and clipped.
func PadLine(value string, width int) string {
	value = Truncate(value, width)
	cellWidth := ansi.StringWidth(value)
	if cellWidth >= width {
		return value
	}

	return value + strings.Repeat(" ", width-cellWidth)
}

func ChromeWidth(width int) int {
	width = NormalizeSize(width, DefaultHeight).Width
	if width < 20 {
		return width
	}
	return width - 2
}
