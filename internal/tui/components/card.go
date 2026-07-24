package components

import "charm.land/lipgloss/v2"

// Card renders content inside a rounded border and centers the resulting
// block within width×height — the 4b RBAC-403 card, 4c/10b setup screens,
// and any other centered block. style should already carry
// BorderForeground/Background/Padding (e.g. from Theme.ErrCardBorder/
// ErrCardBg — Card itself stays Theme-agnostic); Card adds the rounded
// border shape and the centering.
//
// If style carries an explicit Width, it's widened by 2 before the border
// is applied: lipgloss v2's Width now counts the border itself (v1 added
// the border on top of Width), so callers can keep specifying the
// pre-border content+padding width they always have.
func Card(content string, style lipgloss.Style, width, height int) string {
	if w := style.GetWidth(); w > 0 {
		style = style.Width(w + 2)
	}
	box := style.Border(lipgloss.RoundedBorder()).Render(content)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}
