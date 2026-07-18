package tui

import "github.com/charmbracelet/lipgloss"

// Theme is the semantic color source for every inverted-layout screen (the
// redesign in mvp-plan.md). Every view renders through a Theme value — no hex
// literal ever appears in view code. Both Dark() and Light() populate every
// field; a theme swap is a struct swap, never a per-color
// lipgloss.AdaptiveColor. See docs/design/README.md §Design Tokens for the
// source table.
type Theme struct {
	// Backgrounds
	Bg, BgChrome, BgStrip, BgSidebar, BgLog lipgloss.Color

	// BgPalette/BgInput back every floating dialog panel — goto/namespace/
	// context palette, the help overlay, exec/forward pickers — and are the
	// empty lipgloss.Color in both themes (a lipgloss.Color("") resolves to
	// no background escape at all, same as lipgloss.NoColor{}), so dialogs
	// render on the terminal's own background instead of a themed fill.
	// Never give these an actual hex value; add a new token instead if some
	// future dialog surface needs one.
	BgPalette, BgInput lipgloss.Color

	// Borders
	Border, BorderSubtle, BorderPalette lipgloss.Color

	// Text ramp (contrast descends from Text to TextGhost2; on light themes
	// the ramp still runs bright-to-faint in the same role order — never
	// re-ordered).
	Text, TextPrimary, TextSecondary, TextDim, TextFaint, TextGhost, TextGhost2 lipgloss.Color

	// Accent + selection
	Accent, AccentHi, SelBg lipgloss.Color

	// MarkBg is 20a's bulk-operations marked-row tint — quieter than SelBg,
	// since a marked row's cursor-independent state must read as distinct
	// from (and never overpower) the selection background.
	MarkBg lipgloss.Color

	// Status
	Good, Warn, Bad, BadSoft, BadText, BadMuted, Info lipgloss.Color

	// Error surfaces
	ErrBannerBg, ErrBannerBorder, ErrCardBg, ErrCardBorder lipgloss.Color

	// Bars, YAML syntax, PROD tag, ALL NS pill
	BarTrack                              lipgloss.Color
	YamlKey, YamlStr, YamlPunct, YamlFold lipgloss.Color
	ProdBorder, ProdText                  lipgloss.Color
	AllNsPillBg, AllNsPillText            lipgloss.Color

	// ConfirmBorder/ConfirmHeaderBg/ConfirmPillBg back the destructive-confirm
	// modal only (components/confirmmodal.go, Phase 5). Red borders are
	// reserved exclusively for that surface — never reuse these tokens
	// elsewhere. ConfirmHeaderBg is the modal panel's own fill and, like
	// BgPalette/BgInput above, is the empty lipgloss.Color in both themes so
	// the modal renders on the terminal's own background; ConfirmPillBg
	// (the persistent keybar's CONFIRM pill, not part of the floating panel)
	// keeps a real color.
	ConfirmBorder, ConfirmHeaderBg, ConfirmPillBg lipgloss.Color

	// Muted is the 4a-offline color ramp. Each theme declares its own muted
	// values (dark: dim; light: wash toward gray) rather than computing them
	// from the live colors above.
	Muted struct {
		Good, Warn, Bad, Info, Text lipgloss.Color
	}
}

// Dark is the dark-theme token set (docs/design/README.md §Design Tokens,
// dark column).
func Dark() Theme {
	t := Theme{
		Bg:        "#0b0b10",
		BgChrome:  "#0e0e15",
		BgStrip:   "#0c0c12",
		BgSidebar: "#0a0a0f",
		BgLog:     "#08080d",
		BgPalette: "", // dialogs render on the terminal's own background
		BgInput:   "",

		Border:        "#26263a",
		BorderSubtle:  "#1c1c2c",
		BorderPalette: "#3b3b58",

		Text:          "#f0f0fa",
		TextPrimary:   "#d8d8e8",
		TextSecondary: "#9a9ab2",
		TextDim:       "#676780",
		TextFaint:     "#55556e",
		TextGhost:     "#44445c",
		TextGhost2:    "#33334a",

		Accent:   "#a78bfa",
		AccentHi: "#c4b5fd",
		SelBg:    "#1d1633",
		MarkBg:   "#14101f",

		Good:     "#34d17b",
		Warn:     "#e8c74a",
		Bad:      "#ef6a6a",
		BadSoft:  "#ef8a8a",
		BadText:  "#f0b7b7",
		BadMuted: "#c98a8a",
		Info:     "#6aa8ef",

		ErrBannerBg:     "#2a1518",
		ErrBannerBorder: "#4a2228",
		ErrCardBg:       "#16121a",
		ErrCardBorder:   "#3a2a30",

		BarTrack: "#1c1c2c",

		YamlKey:   "#c98fde",
		YamlStr:   "#b8d78f",
		YamlPunct: "#55556e",
		YamlFold:  "#44445c",

		ProdBorder: "#4a2a2a",
		ProdText:   "#ef9a9a",

		AllNsPillBg:   "#12203a",
		AllNsPillText: "#8ab8ef",

		ConfirmBorder:   "#5c2a2a",
		ConfirmHeaderBg: "", // modal renders on the terminal's own background
		ConfirmPillBg:   "#2a1418",
	}
	t.Muted.Good = "#2a7a52"
	t.Muted.Warn = "#8a742e"
	t.Muted.Bad = "#8a4444"
	t.Muted.Info = "#3f6a94"
	t.Muted.Text = "#55556e"
	return t
}

// Light is the light-theme token set (docs/design/README.md §Design Tokens,
// light column). Values are decided equivalents, not naive inversions —
// status colors darken/saturate to hold contrast on light backgrounds.
func Light() Theme {
	t := Theme{
		Bg:        "#f7f7fa",
		BgChrome:  "#eef0f4",
		BgStrip:   "#f2f3f7",
		BgSidebar: "#f5f5f9",
		BgLog:     "#fbfbfd",
		BgPalette: "", // dialogs render on the terminal's own background
		BgInput:   "",

		Border:        "#d5d7e0",
		BorderSubtle:  "#e4e6ee",
		BorderPalette: "#b9bcd0",

		Text:          "#14141c",
		TextPrimary:   "#2a2a38",
		TextSecondary: "#565668",
		TextDim:       "#8a8a9e",
		TextFaint:     "#9a9aae",
		TextGhost:     "#b4b4c6",
		TextGhost2:    "#c6c6d4",

		Accent:   "#6b46d9",
		AccentHi: "#5936b8",
		SelBg:    "#ece5fb",
		MarkBg:   "#f5f1fc",

		Good:     "#148a4e",
		Warn:     "#a87b0a",
		Bad:      "#cc3a3a",
		BadSoft:  "#d95c5c",
		BadText:  "#a02c2c",
		BadMuted: "#b06868",
		Info:     "#2a6fce",

		ErrBannerBg:     "#fbe9ea",
		ErrBannerBorder: "#e5b7bb",
		ErrCardBg:       "#f6f1f4",
		ErrCardBorder:   "#dcc9cd",

		BarTrack: "#e2e4ec",

		YamlKey:   "#8a3fb8",
		YamlStr:   "#4c7a1e",
		YamlPunct: "#9a9aae",
		YamlFold:  "#b4b4c6",

		ProdBorder: "#e3b9b9",
		ProdText:   "#b03030",

		AllNsPillBg:   "#e0ebfa",
		AllNsPillText: "#1f5cad",

		ConfirmBorder:   "#d98a8a",
		ConfirmHeaderBg: "", // modal renders on the terminal's own background
		ConfirmPillBg:   "#f5dede",
	}
	t.Muted.Good = "#5aa87e"
	t.Muted.Warn = "#b39a56"
	t.Muted.Bad = "#c98686"
	t.Muted.Info = "#6f95c4"
	t.Muted.Text = "#9a9aae"
	return t
}
