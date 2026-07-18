package browse

import (
	"context"
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/resources"
)

// otherKindHint is one "N kind" fragment for the 10c "g other kinds" line.
type otherKindHint struct {
	label string
	count int
}

// emptyHints is the live data behind 10c's three ways out. Zero value
// renders each way-out line without its trailing "— ..." detail, so a
// hint that failed to load (or genuinely has nothing to suggest) degrades
// to a plain, still-truthful line rather than blocking the empty state.
type emptyHints struct {
	altNamespace string
	altCount     int
	allCount     int
	otherKinds   []otherKindHint
}

// loadEmptyHints computes the three 10c ways-out against the informer
// cache: the busiest other namespace for this kind, the all-namespaces
// total, and up to two other kinds with resources in the current
// namespace. Cluster-scoped kinds (Nodes, Namespaces) have no
// namespace-relative "ways out", so this returns nil for them.
func (m Model) loadEmptyHints() tea.Cmd {
	if m.desc.ClusterScoped {
		return nil
	}
	lister := m.lister
	reg := resources.Registry{}
	if m.session != nil {
		reg = m.session.Registry
	}
	kind := m.kind
	namespace := m.namespace
	timeout := m.timeout

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		hints := emptyHints{
			otherKinds: otherKindsIn(ctx, lister, reg, kind, namespace),
		}
		hints.altNamespace, hints.altCount = busiestOtherNamespace(ctx, lister, reg, kind, namespace)
		if n, err := resources.Count(ctx, lister, kind, ""); err == nil {
			hints.allCount = n
		}

		return emptyHintsMsg{kind: kind, hints: hints}
	}
}

// busiestOtherNamespace finds the namespace (other than the current one)
// with the most instances of kind, for the "n switch namespace — X has N
// pods" hint.
func busiestOtherNamespace(ctx context.Context, lister resources.RawLister, reg resources.Registry, kind kube.ResourceKind, current string) (string, int) {
	nsDesc, ok := reg.Descriptor(kube.KindNamespace)
	if !ok {
		return "", 0
	}
	nsRows, err := resources.List(ctx, lister, nsDesc, "")
	if err != nil {
		return "", 0
	}
	best, bestCount := "", 0
	for _, row := range nsRows {
		if row.Name == "" || row.Name == current {
			continue
		}
		n, err := resources.Count(ctx, lister, kind, row.Name)
		if err != nil || n <= bestCount {
			continue
		}
		best, bestCount = row.Name, n
	}
	return best, bestCount
}

// otherKindsIn returns up to two non-zero, non-cluster-scoped kinds (other
// than exclude) present in namespace, ordered by count descending, for the
// "g other kinds — this namespace has 2 configmaps, 1 secret" hint.
func otherKindsIn(ctx context.Context, lister resources.RawLister, reg resources.Registry, exclude kube.ResourceKind, namespace string) []otherKindHint {
	var found []otherKindHint
	for _, group := range resources.DefaultGroups() {
		for _, k := range group.Kinds {
			if k == exclude {
				continue
			}
			desc, ok := reg.Descriptor(k)
			if !ok || desc.ClusterScoped {
				continue
			}
			n, err := resources.Count(ctx, lister, k, namespace)
			if err != nil || n == 0 {
				continue
			}
			found = append(found, otherKindHint{label: kindNoun(desc, n), count: n})
		}
	}
	sort.SliceStable(found, func(i, j int) bool { return found[i].count > found[j].count })
	if len(found) > 2 {
		found = found[:2]
	}
	return found
}

// kindNoun renders "2 configmaps" / "1 secret": every built-in Descriptor.Display
// is already the plural noun (registry.go), so the singular is just that
// noun minus its trailing "s".
func kindNoun(desc resources.Descriptor, n int) string {
	singular := strings.ToLower(strings.TrimSuffix(desc.Display, "s"))
	if n == 1 {
		return fmt.Sprintf("%d %s", n, singular)
	}
	return fmt.Sprintf("%d %ss", n, singular)
}
