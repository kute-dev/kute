package components

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

// Card centers a padded block within width×height — the 4b RBAC-403 card,
// 4c/10b setup screens, 10a/13a's picker panels, and any other centered
// block. style should already carry BorderForeground/Background/Padding
// (e.g. from Theme.ErrCardBorder/ErrCardBg — Card itself stays
// Theme-agnostic); Card supplies the rounded border shape and the centering.
//
// The style's own Background decides whether a border is drawn at all, because
// the two can't coexist cleanly. A box-drawing glyph inks only part of its
// cell and the halves aren't separately addressable, so a bordered card with a
// fill can only pick which artifact to show: a foreground-only stroke leaves a
// one-cell seam of page color between it and the fill, and BorderBackground
// closes that seam only by bleeding the fill one cell *outside* the stroke
// (the same trade tasks/poddetail's error banner documents). So:
//
//   - Filled panels (Theme.ErrCardBg) drop the border and let the fill be the
//     shape. BorderForeground is left untouched, not dead — it applies again
//     the moment the background does go empty.
//   - Transparent panels (Theme.BgPalette and friends, deliberately the empty
//     color.Color so the dialog shows the terminal's own background) keep the
//     border, which is the only thing giving them an edge. There is no fill to
//     seam against.
//
// A bordered card with an explicit Width is widened by 2 first: lipgloss v2's
// Width counts the border itself (v1 added it on top), so callers can keep
// specifying the pre-border content+padding width they always have. A
// borderless one takes that Width as-is, which is already what it means.
func Card(content string, style lipgloss.Style, width, height int) string {
	if isNoColor(style.GetBackground()) {
		if w := style.GetWidth(); w > 0 {
			style = style.Width(w + 2)
		}
		style = style.Border(lipgloss.RoundedBorder())
	}
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, style.Render(content))
}

// isNoColor reports whether c is lipgloss's unset-color sentinel. The getters
// return it rather than nil for a property that was never set, and
// lipgloss.Color("") — how the Theme spells a deliberately transparent
// surface — normalizes to it too.
func isNoColor(c color.Color) bool {
	_, ok := c.(lipgloss.NoColor)
	return ok || c == nil
}
