package tui

import "github.com/charmbracelet/lipgloss"

// Styles is the derived style set for the inverted-layout redesign
// (mvp-plan.md), built once per Theme by NewStyles. A theme swap rebuilds
// the whole set in one place; screens hold a Styles value (reached via the
// root shell/Session, see mvp-plan.md §0.9) rather than building
// lipgloss.Style values from Theme fields themselves. Render stays pure:
// f(model, theme, size).
type Styles struct {
	// Chrome surfaces
	Chrome  lipgloss.Style // bg BgChrome — header/keybar lines
	Strip   lipgloss.Style // bg BgStrip — summary/health/stale strips
	Sidebar lipgloss.Style // bg BgSidebar
	Log     lipgloss.Style // bg BgLog
	Palette lipgloss.Style // bg BgPalette — palette/modal panels
	Input   lipgloss.Style // bg BgInput — palette input row

	// Text ramp
	Text          lipgloss.Style
	TextPrimary   lipgloss.Style
	TextSecondary lipgloss.Style
	Dim           lipgloss.Style // TextDim
	Faint         lipgloss.Style // TextFaint
	Ghost         lipgloss.Style // TextGhost
	Ghost2        lipgloss.Style // TextGhost2

	// Accent + selection
	Accent      lipgloss.Style
	AccentHi    lipgloss.Style
	SelectedRow lipgloss.Style // bg SelBg, full selected row

	// Status
	OK   lipgloss.Style // Good
	Warn lipgloss.Style
	Fail lipgloss.Style // Bad
	Info lipgloss.Style

	// Error surfaces
	ErrBanner lipgloss.Style // bg ErrBannerBg, border ErrBannerBorder
	ErrCard   lipgloss.Style // bg ErrCardBg, border ErrCardBorder

	// Bars, YAML syntax
	BarTrack  lipgloss.Style
	YamlKey   lipgloss.Style
	YamlStr   lipgloss.Style
	YamlPunct lipgloss.Style
	YamlFold  lipgloss.Style

	// PROD tag, ALL NS pill
	Prod      lipgloss.Style // border ProdBorder, text ProdText
	AllNsPill lipgloss.Style // bg AllNsPillBg, text AllNsPillText

	// Confirm surfaces — the app's only red-bordered surface (destructive
	// confirm only; never reuse elsewhere).
	ConfirmBorder   lipgloss.Style
	ConfirmHeaderBg lipgloss.Style
	ConfirmPillBg   lipgloss.Style

	// Muted (4a offline) ramp, mirrors Theme.Muted.
	Muted struct {
		Good, Warn, Bad, Info, Text lipgloss.Style
	}
}

// NewStyles derives a full Styles set from a Theme value. Called once at
// startup (and again on --theme override / config change) rather than
// building styles ad hoc in view code.
func NewStyles(t Theme) Styles {
	var s Styles

	s.Chrome = lipgloss.NewStyle().Background(t.BgChrome)
	s.Strip = lipgloss.NewStyle().Background(t.BgStrip)
	s.Sidebar = lipgloss.NewStyle().Background(t.BgSidebar)
	s.Log = lipgloss.NewStyle().Background(t.BgLog)
	s.Palette = lipgloss.NewStyle().Background(t.BgPalette)
	s.Input = lipgloss.NewStyle().Background(t.BgInput)

	s.Text = lipgloss.NewStyle().Foreground(t.Text)
	s.TextPrimary = lipgloss.NewStyle().Foreground(t.TextPrimary)
	s.TextSecondary = lipgloss.NewStyle().Foreground(t.TextSecondary)
	s.Dim = lipgloss.NewStyle().Foreground(t.TextDim)
	s.Faint = lipgloss.NewStyle().Foreground(t.TextFaint)
	s.Ghost = lipgloss.NewStyle().Foreground(t.TextGhost)
	s.Ghost2 = lipgloss.NewStyle().Foreground(t.TextGhost2)

	s.Accent = lipgloss.NewStyle().Foreground(t.Accent)
	s.AccentHi = lipgloss.NewStyle().Foreground(t.AccentHi)
	s.SelectedRow = lipgloss.NewStyle().Background(t.SelBg)

	s.OK = lipgloss.NewStyle().Foreground(t.Good)
	s.Warn = lipgloss.NewStyle().Foreground(t.Warn)
	s.Fail = lipgloss.NewStyle().Foreground(t.Bad)
	s.Info = lipgloss.NewStyle().Foreground(t.Info)

	s.ErrBanner = lipgloss.NewStyle().Background(t.ErrBannerBg).BorderForeground(t.ErrBannerBorder)
	s.ErrCard = lipgloss.NewStyle().Background(t.ErrCardBg).BorderForeground(t.ErrCardBorder)

	s.BarTrack = lipgloss.NewStyle().Foreground(t.BarTrack)
	s.YamlKey = lipgloss.NewStyle().Foreground(t.YamlKey)
	s.YamlStr = lipgloss.NewStyle().Foreground(t.YamlStr)
	s.YamlPunct = lipgloss.NewStyle().Foreground(t.YamlPunct)
	s.YamlFold = lipgloss.NewStyle().Foreground(t.YamlFold)

	s.Prod = lipgloss.NewStyle().Foreground(t.ProdText).BorderForeground(t.ProdBorder)
	s.AllNsPill = lipgloss.NewStyle().Background(t.AllNsPillBg).Foreground(t.AllNsPillText)

	s.ConfirmBorder = lipgloss.NewStyle().BorderForeground(t.ConfirmBorder)
	s.ConfirmHeaderBg = lipgloss.NewStyle().Background(t.ConfirmHeaderBg)
	s.ConfirmPillBg = lipgloss.NewStyle().Background(t.ConfirmPillBg)

	s.Muted.Good = lipgloss.NewStyle().Foreground(t.Muted.Good)
	s.Muted.Warn = lipgloss.NewStyle().Foreground(t.Muted.Warn)
	s.Muted.Bad = lipgloss.NewStyle().Foreground(t.Muted.Bad)
	s.Muted.Info = lipgloss.NewStyle().Foreground(t.Muted.Info)
	s.Muted.Text = lipgloss.NewStyle().Foreground(t.Muted.Text)

	return s
}
