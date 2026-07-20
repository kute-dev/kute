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
	return miniBar(used, denom, width, styles, 1)
}

// MiniBarBadAt is MiniBar with a caller-chosen Bad threshold instead of the
// default "at/over denom" (100%) — 5a's MEM bar turns red at 96% usage
// (docs/design README.md §75), a stricter threshold than every other
// MiniBar consumer's (2a's relative pod bar, 11a/11b's node capacity bars),
// so it's opt-in per call site rather than a change to MiniBar's default.
func MiniBarBadAt(used, denom int64, width int, styles BarStyles, badAt float64) string {
	return miniBar(used, denom, width, styles, badAt)
}

func miniBar(used, denom int64, width int, styles BarStyles, badAt float64) string {
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

	// docs/design/v.0.2.0.dc.html's 25a mockup renders its own P95 USAGE bars
	// with ▮ (filled) / ▯ (track) — the solid/hollow square pair, not the
	// half-block glyphs §11a's older prose describes (this component's other
	// consumers — 2a's relative pod bar, 5a's CPU/MEM bars — share this same
	// glyph pair).
	bar := fillStyleFor(ratio, badAt, styles).Render(strings.Repeat("▮", filled))
	if empty := width - filled; empty > 0 {
		bar += styles.Track.Render(strings.Repeat("▯", empty))
	}
	return bar
}

// fillStyleFor picks the fill span's style by usage ratio: Bad at/over
// badAt, Warn at/above 70%, Fill (accent) otherwise.
func fillStyleFor(ratio, badAt float64, styles BarStyles) lipgloss.Style {
	switch {
	case ratio >= badAt:
		return styles.Bad
	case ratio >= 0.7:
		return styles.Warn
	default:
		return styles.Fill
	}
}
