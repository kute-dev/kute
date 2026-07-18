package tui

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// TestPaletteStylesMapsEveryFieldToItsDesignToken pins the exact
// Theme-token-to-palette.Styles-field mapping against
// docs/design/README.md §Design Tokens (cross-checked with the mockup HTML,
// which is the ultimate source for values the README table doesn't spell
// out per-field, e.g. 12a/12b's alias-letter highlight). This is the "pixel perfect" pin
// for a component that (being Theme-agnostic itself) can't be tested via a
// rendered/colored golden fixture — the conversion in paletteStyles is the
// only place the token choice lives.
func TestPaletteStylesMapsEveryFieldToItsDesignToken(t *testing.T) {
	t.Parallel()

	for name, theme := range map[string]Theme{"Dark": Dark(), "Light": Light()} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := paletteStyles(theme)

			// Every style carries its region's background — the panel
			// floats over the dimmed table, and any span or fill without an
			// explicit bg lets the terminal's own background bleed through
			// (inner ANSI resets cancel any outer wrap).
			assertBg(t, "Body", s.Body, theme.BgPalette)
			assertBg(t, "Input", s.Input, theme.BgInput)
			for field, style := range map[string]lipgloss.Style{
				"Prompt": s.Prompt, "Cursor": s.Cursor, "Placeholder": s.Placeholder,
				"Query": s.Query, "Hint": s.Hint,
			} {
				assertBg(t, field, style, theme.BgInput)
			}
			for field, style := range map[string]lipgloss.Style{
				"Match": s.Match, "Normal": s.Normal, "Dim": s.Dim, "Muted": s.Muted,
				"Detail": s.Detail, "RightOK": s.RightOK, "RightWarn": s.RightWarn,
				"RightBad": s.RightBad, "Rule": s.Rule, "RecentRule": s.RecentRule,
				"RecentLabel": s.RecentLabel, "RecentItem": s.RecentItem,
				"FooterDetail": s.FooterDetail, "FooterKey": s.FooterKey,
				"FooterEm": s.FooterEm, "AliasLabel": s.AliasLabel,
			} {
				assertBg(t, field, style, theme.BgPalette)
			}

			assertFg(t, "Prompt", s.Prompt, theme.Accent)
			assertFg(t, "Cursor", s.Cursor, theme.Accent)
			assertFg(t, "Placeholder", s.Placeholder, theme.TextFaint)
			assertFg(t, "Query", s.Query, theme.Text)
			assertFg(t, "Hint", s.Hint, theme.TextFaint)
			assertFg(t, "Match", s.Match, theme.AccentHi)
			assertFg(t, "Normal", s.Normal, theme.TextPrimary)
			assertFg(t, "Dim", s.Dim, theme.TextGhost)
			// 12a draws chip-less kinds (StatefulSets, Jobs…) one step
			// darker than the chip rows: #676780/TextDim.
			assertFg(t, "Muted", s.Muted, theme.TextDim)
			// Detail backs both 2b's type label/right-status and 12a's
			// normal-count text — the mockup uses the same #55556e/TextFaint
			// for both (Kute Spec.dc.html's 12a ranked-list rows).
			assertFg(t, "Detail", s.Detail, theme.TextFaint)
			// 12b's fuzzy resource rows carry a live status glyph on the
			// right, in the status hues.
			assertFg(t, "RightOK", s.RightOK, theme.Good)
			assertFg(t, "RightWarn", s.RightWarn, theme.Warn)
			assertFg(t, "RightBad", s.RightBad, theme.Bad)
			assertFg(t, "SelBar", s.SelBar, theme.Accent)
			assertBg(t, "SelBar", s.SelBar, theme.SelBg)
			// The selected row's label brightens to Text, matching the 12a
			// mockup's selected Pods row (and 2a's selected NAME cell).
			assertFg(t, "SelRow", s.SelRow, theme.Text)
			assertBg(t, "SelRow", s.SelRow, theme.SelBg)
			assertBg(t, "SelBg", s.SelBg, theme.SelBg)
			// The input row's bottom border and the key row's top border, and
			// RECENT's/the footer's dividers, use the text ramp's ghost
			// tones (TextGhost/TextGhost2) — the same tokens chrome.go uses
			// for its own band rules, since the mockup's darker Border/
			// BorderSubtle read as too dark without a dialog background
			// fill behind them.
			assertFg(t, "Rule", s.Rule, theme.TextGhost)
			assertFg(t, "RecentRule", s.RecentRule, theme.TextGhost2)
			// 2b's RECENT line: "RECENT" + · separators #55556e/TextFaint,
			// the kind names themselves #9a9ab2/TextSecondary.
			assertFg(t, "RecentLabel", s.RecentLabel, theme.TextFaint)
			assertFg(t, "RecentItem", s.RecentItem, theme.TextSecondary)
			// The palette key row shares the panel's own BgPalette (not the
			// app keybar's BgChrome) so it stays one continuous fill with
			// the rest of the dialog: keys Accent, labels TextDim.
			assertFg(t, "KeyRow", s.KeyRow, theme.TextDim)
			assertBg(t, "KeyRow", s.KeyRow, theme.BgPalette)
			assertFg(t, "KeyRowKey", s.KeyRowKey, theme.Accent)
			assertBg(t, "KeyRowKey", s.KeyRowKey, theme.BgPalette)
			assertFg(t, "FooterDetail", s.FooterDetail, theme.TextDim)
			// The footer's accent spans (12a's "alias" word, 12b's ↵ and
			// namespace) and 12b's emphasized destination kind.
			assertFg(t, "FooterKey", s.FooterKey, theme.Accent)
			assertFg(t, "FooterEm", s.FooterEm, theme.Text)
			// 7a's PROD escalation tag and 6a's "all namespaces" row both
			// float over the palette body, so they carry BgPalette too.
			assertBg(t, "ProdTag", s.ProdTag, theme.BgPalette)
			assertFg(t, "ProdTag", s.ProdTag, theme.ProdText)
			assertBg(t, "AllNS", s.AllNS, theme.BgPalette)
			assertFg(t, "AllNS", s.AllNS, theme.Info)

			// 12a/12b's alias-letter highlight reuses Match (AccentHi bold on
			// BgPalette) — no separate chip token. 12b's "alias match" label
			// is Accent in the mockup (#a78bfa), not AccentHi.
			assertFg(t, "AliasLabel", s.AliasLabel, theme.Accent)
		})
	}
}

func assertFg(t *testing.T, field string, style lipgloss.Style, want lipgloss.Color) {
	t.Helper()
	if got := style.GetForeground(); got != lipgloss.TerminalColor(want) {
		t.Errorf("%s foreground = %v, want %v", field, got, want)
	}
}

func assertBg(t *testing.T, field string, style lipgloss.Style, want lipgloss.Color) {
	t.Helper()
	if got := style.GetBackground(); got != lipgloss.TerminalColor(want) {
		t.Errorf("%s background = %v, want %v", field, got, want)
	}
}
