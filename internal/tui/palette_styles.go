package tui

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/kute-dev/kute/internal/tui/components/palette"
)

// paletteStyles builds a palette.Styles value from Theme. This conversion
// lives in tui (not palette) so palette stays Theme-agnostic and
// import-cycle-free — the root shell holds a *palette.Model (mvp-plan.md
// §0.9), so palette cannot import tui.
//
// Every style carries its region's background (BgInput input row, BgPalette
// body and key row) so every span/pad in a region resolves to the same fill
// — an outer wrap can't do this instead, since each inner span's ANSI reset
// would cancel it. BgInput/BgPalette are themselves the empty lipgloss.Color
// (see theme.go), so today this fill is a no-op and the panel renders on
// the terminal's own background; the per-span plumbing stays so a future
// theme can give dialogs a real fill again without restructuring this file.
// The key row deliberately shares BgPalette rather than the main chrome's
// BgChrome — the two used to read as the same dark tone, but with BgPalette
// now transparent a BgChrome-filled key row would show up as a solid bar
// under an otherwise see-through panel.
func paletteStyles(t Theme) palette.Styles {
	body := lipgloss.NewStyle().Background(t.BgPalette)
	input := lipgloss.NewStyle().Background(t.BgInput)
	return palette.Styles{
		Frame:        lipgloss.NewStyle().BorderForeground(t.BorderPalette).BorderBackground(t.BgPalette).Background(t.BgPalette),
		Body:         body,
		Input:        input,
		Prompt:       input.Foreground(t.Accent).Bold(true),
		Cursor:       input.Foreground(t.Accent),
		Placeholder:  input.Foreground(t.TextFaint),
		Query:        input.Foreground(t.Text),
		Hint:         input.Foreground(t.TextFaint),
		Match:        body.Foreground(t.AccentHi).Bold(true),
		Normal:       body.Foreground(t.TextPrimary),
		Dim:          body.Foreground(t.TextGhost),
		Muted:        body.Foreground(t.TextDim),
		Detail:       body.Foreground(t.TextFaint),
		RightOK:      body.Foreground(t.Good),
		RightWarn:    body.Foreground(t.Warn),
		RightBad:     body.Foreground(t.Bad),
		SelBar:       lipgloss.NewStyle().Foreground(t.Accent).Background(t.SelBg),
		SelRow:       lipgloss.NewStyle().Background(t.SelBg).Foreground(t.Text),
		SelBg:        lipgloss.NewStyle().Background(t.SelBg),
		// Rule/RecentRule use the text ramp's ghost tones, matching
		// chrome.go's own band rules (primaryRule/stripRule) — Border/
		// BorderSubtle read as too dark once a dialog has no background
		// fill of its own to sit on top of (BgPalette's now the empty
		// lipgloss.Color, see theme.go).
		Rule:         body.Foreground(t.TextGhost),
		RecentRule:   body.Foreground(t.TextGhost2),
		RecentLabel:  body.Foreground(t.TextFaint),
		RecentItem:   body.Foreground(t.TextSecondary),
		KeyRow:       body.Foreground(t.TextDim),
		KeyRowKey:    body.Foreground(t.Accent),
		FooterDetail: body.Foreground(t.TextDim),
		FooterKey:    body.Foreground(t.Accent),
		FooterEm:     body.Foreground(t.Text),
		ProdTag:      body.Foreground(t.ProdText).Bold(true),
		AllNS:        body.Foreground(t.Info),
		AliasLabel:   body.Foreground(t.Accent),
	}
}
