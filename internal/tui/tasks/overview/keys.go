package overview

import (
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/verbs"
)

// Keybar composes the bottom band from verb references where one already
// exists (Help is a shared cross-screen verb); `↹ next panel`/`↵ open` are
// screen-local, the same "no cross-screen verb to register" precedent
// tasks/whocan's own v/k keys already establish. Timeline/Events (t/e) are
// global grammar as of v.0.3.0.dc.html §29a, so they no longer render here —
// they're taught once in the ? overlay's GLOBAL column.
func (m Model) Keybar() tui.Keybar {
	if m.state != tui.TaskStateReady {
		return tui.Keybar{Pill: tui.ModeBrowse, PillText: "OVERVIEW"}
	}

	groups := [][]tui.KeyHint{
		{{Key: "esc", Label: "back"}},
		{{Key: tui.GlyphTab, Label: "next panel"}, {Key: "↵", Label: "open"}},
	}

	return tui.Keybar{
		Pill:       tui.ModeBrowse,
		PillText:   "OVERVIEW",
		Groups:     groups,
		RightHints: append(tui.UpdateRightHints(m.session), verbs.Help.Hint()),
	}
}
