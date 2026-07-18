package components

import "github.com/charmbracelet/lipgloss"

// Card renders content inside a rounded border and centers the resulting
// block within width×height — the 4b RBAC-403 card, 4c/10b setup screens,
// and any other centered block. style should already carry
// BorderForeground/Background/Padding (e.g. from Theme.ErrCardBorder/
// ErrCardBg — Card itself stays Theme-agnostic); Card adds the rounded
// border shape and the centering.
func Card(content string, style lipgloss.Style, width, height int) string {
	box := style.Border(lipgloss.RoundedBorder()).Render(content)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}
