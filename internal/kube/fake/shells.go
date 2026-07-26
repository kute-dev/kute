package fake

import (
	"context"
	"strings"
)

// DetectShells is the demo-mode stand-in for kube.DetectShells, so 10a's
// shells column renders real-looking states in --demo instead of the "–"
// a nil detector would produce (CLAUDE.md: "the fake provider must stay
// feature-complete for tests/demo mode").
//
// Deterministic, and deliberately not uniform — the demo pod's two
// containers exercise two different answers: an application container
// reports bash and sh, a sidecar (typically a scratch/distroless image)
// reports only sh. A container named for a distroless image reports none at
// all, so the "no shell" state is reachable too.
func (c *Cluster) DetectShells(_ context.Context, _, _, container string) ([]string, error) {
	switch {
	case strings.Contains(container, "distroless"):
		return nil, nil
	case strings.Contains(container, "sidecar"):
		return []string{"sh"}, nil
	default:
		return []string{"bash", "sh"}, nil
	}
}
