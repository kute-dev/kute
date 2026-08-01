package fluxdetail

import (
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/verbs"
)

// Keybar is §31a's: esc/↵ always, the §30a verbs when a write seam is
// wired, and tab only once there is a folded tail to expand.
func (m Model) Keybar() tui.Keybar {
	if m.state != tui.TaskStateReady {
		return tui.Keybar{Pill: tui.ModeBrowse, PillText: m.pillText()}
	}
	groups := [][]tui.KeyHint{{{Key: "esc", Label: "back"}}}
	if len(m.inventory) > 0 {
		groups[0] = append(groups[0], tui.KeyHint{Key: "↵", Label: "open object"})
	}

	if m.mutator != nil && !m.offline() {
		suspend := verbs.FluxSuspend
		if m.suspended {
			suspend.Label = "resume"
		}
		groups = append(groups, []tui.KeyHint{
			{Key: verbs.FluxReconcile.Key, Label: verbs.FluxReconcile.Label},
			{Key: suspend.Key, Label: suspend.Label},
		})
	}
	nav := []tui.KeyHint{}
	if m.chn.SourceName != "" {
		nav = append(nav, tui.KeyHint{Key: verbs.FluxSource.Key, Label: verbs.FluxSource.Label})
	}
	nav = append(nav, verbs.YAML.Hint())
	groups = append(groups, nav)

	if m.foldedCount() > 0 || m.invExpanded {
		groups = append(groups, []tui.KeyHint{{Key: "tab", Label: "expand/collapse ready"}})
	}
	return tui.Keybar{
		Pill:       tui.ModeBrowse,
		PillText:   m.pillText(),
		Groups:     groups,
		RightNote:  m.feedback,
		RightHints: append(tui.UpdateRightHints(m.session), verbs.Help.Hint()),
	}
}

// pillText is the kind's own short name, per §30a/§31a.
func (m Model) pillText() string {
	switch m.kind {
	case "Kustomization":
		return "KUSTOMIZE"
	default:
		return "FLUX"
	}
}
