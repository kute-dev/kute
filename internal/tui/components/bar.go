package components

import (
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// BarStyles are the pre-styled spans MiniBar composes from. Screens build
// these from a Theme value (Track: Theme.BarTrack, Fill: Theme.Accent,
// Warn: Theme.Warn, Bad: Theme.Bad — docs/design/README.md §Design Tokens),
// keeping this component Theme-agnostic like Table.
type BarStyles struct {
	Track lipgloss.Style
	Fill  lipgloss.Style
	Warn  lipgloss.Style
	Bad   lipgloss.Style
}

// MiniBar draws a used/denom bar width cells wide: Theme.BarTrack track,
// Fill (accent) below 70%, Warn at/above 70%, Bad at/over denom (mockup 2a's
// CPU/MEM columns, generalizing the pre-redesign pods screen's resourceZoneBar
// — that legacy copy was deleted along with the screen in Phase 1 rather
// than rewired mid-migration). denom <= 0 means metrics are unavailable:
// MiniBar returns a right-aligned "–" instead of a bar.
func MiniBar(used, denom int64, width int, styles BarStyles) string {
	if width <= 0 {
		return ""
	}
	if denom <= 0 {
		return styles.Track.Render(padLeft("–", width))
	}

	ratio := float64(used) / float64(denom)
	filled := int(math.Ceil(ratio * float64(width)))
	if used > 0 && filled < 1 {
		filled = 1
	}
	if filled > width {
		filled = width
	}
	if filled < 0 {
		filled = 0
	}

	bar := fillStyleFor(ratio, styles).Render(strings.Repeat("■", filled))
	if empty := width - filled; empty > 0 {
		bar += styles.Track.Render(strings.Repeat("□", empty))
	}
	return bar
}

// fillStyleFor picks the fill span's style by usage ratio: Bad at/over
// denom, Warn at/above 70%, Fill (accent) otherwise.
func fillStyleFor(ratio float64, styles BarStyles) lipgloss.Style {
	switch {
	case ratio >= 1:
		return styles.Bad
	case ratio >= 0.7:
		return styles.Warn
	default:
		return styles.Fill
	}
}
