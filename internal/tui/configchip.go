package tui

import "charm.land/lipgloss/v2"

// BuildConfigChip renders the header's config.yaml warning: nothing when the
// user config loaded cleanly (or doesn't exist), otherwise a persistent
// chip for as long as the session runs — a broken hand edit must never
// change behaviour silently (config.Config.LoadErr). Red when prodContexts
// itself couldn't be read, because every context is then being treated as
// PROD; yellow when only other keys were dropped. The full error goes to
// the diagnostics log, not here: a header chip has room for one fact.
func BuildConfigChip(theme Theme, session *Session) ConnBadge {
	if session == nil || session.Config.LoadErr == nil {
		return ConnBadge{}
	}
	if session.Config.ProdUnknown() {
		return ConnBadge{
			Text:  GlyphWarning + " config.yaml unreadable · all PROD",
			Style: lipgloss.NewStyle().Foreground(theme.Bad),
		}
	}
	return ConnBadge{
		Text:  GlyphWarning + " config.yaml has errors",
		Style: lipgloss.NewStyle().Foreground(theme.Warn),
	}
}
