package routetable

import (
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/verbs"
)

// Keybar composes the bottom band from verb references only, per the
// registry invariant. Pill is ROUTES for the Ingress/HTTPRoute flavors
// (docs/design README.md §23a), GATEWAY for Gateway's own listener view.
func (m Model) Keybar() tui.Keybar {
	pill, pillText := tui.ModeBrowse, "ROUTES"
	if m.flavor == flavorGateway {
		pillText = "GATEWAY"
	}

	if m.state == tui.TaskStateLoading {
		return tui.Keybar{
			Pill:      pill,
			PillText:  pillText,
			Groups:    [][]tui.KeyHint{{{Key: "esc", Label: "back"}}},
			RightNote: "facts & rows enable when data lands",
		}
	}

	groups := [][]tui.KeyHint{{{Key: "esc", Label: "back"}}}
	if m.rowCount() > 0 {
		switch m.flavor {
		case flavorGateway:
			groups = append(groups, []tui.KeyHint{verbs.Open.Hint()})
		case flavorRoute:
			hints := []tui.KeyHint{verbs.Open.Hint()}
			if m.parentGatewayName != "" {
				hints = append(hints, verbs.OpenParentGateway.Hint())
			}
			groups = append(groups, hints)
		case flavorIngress:
			hints := []tui.KeyHint{verbs.Open.Hint(), verbs.CopyRouteURL.Hint()}
			if len(m.tlsFacts) > 0 {
				if m.tlsFocused {
					hints = append(hints, verbs.OpenTLSSecret.Hint())
				} else {
					hints = append(hints, verbs.FocusTLSStrip.Hint())
				}
			}
			groups = append(groups, hints)
		}
	}
	// §23a/§23b's "Y copy yaml · e events" group — acts on the viewed
	// object itself, so it's offered whenever the seam is wired, independent
	// of rowCount (unlike the row-scoped verbs above).
	var objectHints []tui.KeyHint
	if m.yaml != nil {
		objectHints = append(objectHints, verbs.CopyRouteYAML.Hint())
	}
	if m.openEvents != nil {
		objectHints = append(objectHints, verbs.Events.Hint())
	}
	if len(objectHints) > 0 {
		groups = append(groups, objectHints)
	}

	return tui.Keybar{
		Pill:     pill,
		PillText: pillText,
		Groups:   groups,
	}
}

// CapturingInput reports whether routetable has an open free-text input —
// it never does (a read-only routing table, no filter/confirm), so the root
// shell's global shortcuts always reach it as-is.
func (m Model) CapturingInput() bool { return false }
