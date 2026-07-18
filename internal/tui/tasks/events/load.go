package events

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/resources"
)

// load fetches the raw events for the screen's scope (object-scoped via
// ObjectEvents when objectKind is set, else namespace-scoped via
// NamespaceEvents), dedupes them, and best-effort cross-checks which
// involved pods are currently StatusFail — the "actively-failing object"
// signal 9b's red-vs-yellow warning coloring needs (docs/design README.md
// §9b: "red reserved for events tied to an actively-failing object").
func (m Model) load() tea.Cmd {
	src := m.events
	lister := m.lister
	namespace := m.namespace
	objectKind := m.objectKind
	objectName := m.objectName
	timeout := m.timeout
	reg := resources.Registry{}
	if m.session != nil {
		reg = m.session.Registry
	}

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		var raw []kube.Event
		var err error
		if objectKind != "" {
			raw, err = src.ObjectEvents(ctx, namespace, objectKind, objectName)
		} else {
			raw, err = src.NamespaceEvents(ctx, namespace)
		}
		if err != nil {
			return loadedMsg{err: err}
		}

		return loadedMsg{
			groups:  kube.DedupeEvents(raw),
			failing: failingPods(ctx, lister, reg, namespace),
		}
	}
}

// failingPods is a best-effort namespace scan for the red/yellow warning
// cross-check — nil (every warning renders yellow) when lister isn't wired
// or the Pod descriptor can't be resolved, the same "degrade gracefully"
// precedent browse's own busiestOtherNamespace/otherKindsIn hints use.
func failingPods(ctx context.Context, lister resources.RawLister, reg resources.Registry, namespace string) map[string]bool {
	if lister == nil {
		return nil
	}
	desc, ok := reg.Descriptor(kube.KindPod)
	if !ok {
		return nil
	}
	rows, err := resources.List(ctx, lister, desc, namespace)
	if err != nil {
		return nil
	}
	failing := make(map[string]bool, len(rows))
	for _, r := range rows {
		if r.Status == resources.StatusFail {
			failing[r.Name] = true
		}
	}
	return failing
}
