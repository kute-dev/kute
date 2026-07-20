package overview

import (
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/verbs"
)

// Keybar composes the bottom band from verb references where one already
// exists (Timeline/Events/Help are shared cross-screen verbs); `↹ next
// panel` is screen-local, the same "no cross-screen verb to register"
// precedent tasks/whocan's own v/k keys already establish.
func (m Model) Keybar() tui.Keybar {
	if m.state != tui.TaskStateReady {
		return tui.Keybar{Pill: tui.ModeBrowse, PillText: "OVERVIEW"}
	}

	groups := [][]tui.KeyHint{
		{{Key: "esc", Label: "back"}},
		{{Key: tui.GlyphTab, Label: "next panel"}, {Key: "↵", Label: "open"}},
		{verbs.Timeline.Hint(), verbs.Events.Hint()},
	}

	return tui.Keybar{
		Pill:       tui.ModeBrowse,
		PillText:   "OVERVIEW",
		Groups:     groups,
		RightHints: append(tui.UpdateRightHints(m.session), verbs.Help.Hint()),
	}
}
