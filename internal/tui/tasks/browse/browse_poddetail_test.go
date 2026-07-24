package browse

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
)

// TestEnterOpensPodDetail exercises 5a's wiring from browse's Pods list:
// 'enter' hands the selected pod's name plus the visible list's ordered
// names + cursor to OpenPodDetail (browse.OpenPodDetailFunc), so 5a's j/k
// can move to the next/prev pod without leaving detail.
func TestEnterOpensPodDetail(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {pod("default", "api-0"), pod("default", "worker-0")},
	}}
	var openedName string
	var openedSiblings []string
	var openedIndex int
	m := New(Config{
		Session: newSession(), Lister: lister,
		OpenPodDetail: func(p kube.Pod, siblings []string, index int, w, h int) (tea.Model, tea.Cmd) {
			openedName = p.Name
			openedSiblings = siblings
			openedIndex = index
			return stubTask{}, nil
		},
	})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	updated, _ := m.Update(tea.KeyPressMsg{Text: "enter"})
	if openedName != "api-0" {
		t.Fatalf("expected api-0 to be opened, got %q", openedName)
	}
	if len(openedSiblings) != 2 || openedSiblings[openedIndex] != "api-0" {
		t.Fatalf("expected siblings to include api-0 at its own index, got %v index %d", openedSiblings, openedIndex)
	}
	if _, ok := updated.(stubTask); !ok {
		t.Fatalf("expected Update to return the pushed stub task, got %T", updated)
	}
}

// TestEnterCommitsFilterThenOpensPodDetail covers enter's two-step behavior
// while filtering: the first enter never opens a destination, even for a
// kind (Pods) that has one — it commits the filter instead (query/rows stay
// narrowed, but keys stop being captured as typing). A second enter, now
// routed through the normal unfiltered path, opens the selected pod's
// detail exactly as it would with no filter at all.
func TestEnterCommitsFilterThenOpensPodDetail(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {pod("default", "api-0"), pod("default", "worker-0")},
	}}
	var openedName string
	m := New(Config{
		Session: newSession(), Lister: lister,
		OpenPodDetail: func(p kube.Pod, siblings []string, index int, w, h int) (tea.Model, tea.Cmd) {
			openedName = p.Name
			return stubTask{}, nil
		},
	})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	updated, _ := m.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	m = *updated.(*Model)
	for _, r := range "api" {
		updated, _ = m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = *updated.(*Model)
	}
	if !m.filterActive || len(m.visible) != 1 || m.visible[0].row.Name != "api-0" {
		t.Fatalf("expected the filter to narrow to just api-0, got active=%v visible=%+v", m.filterActive, m.visible)
	}

	updated, _ = m.Update(tea.KeyPressMsg{Text: "enter"})
	m = *updated.(*Model)
	if openedName != "" {
		t.Fatalf("expected the first enter to commit the filter, not open api-0's detail, got %q", openedName)
	}
	if !m.filterActive || !m.filterListFocused || m.filterInput.Value() != "api" {
		t.Fatalf("filterActive=%v filterListFocused=%v filterQuery=%q, want all committed", m.filterActive, m.filterListFocused, m.filterInput.Value())
	}

	final, _ := m.Update(tea.KeyPressMsg{Text: "enter"})
	if openedName != "api-0" {
		t.Fatalf("expected the second enter to open api-0's detail, got %q", openedName)
	}
	if _, ok := final.(stubTask); !ok {
		t.Fatalf("expected Update to return the pushed stub task, got %T", final)
	}
}

// TestPodKeybarShowsOpenAndLogsWhenWired confirms the Pods-kind keybar
// group carries both verbs.Open and verbs.Logs once both callbacks are set
// (keys.go's Keybar), matching 11a's precedent for the Nodes group.
func TestPodKeybarShowsOpenAndLogsWhenWired(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {pod("default", "api-0")},
	}}
	m := New(Config{
		Session: newSession(), Lister: lister,
		OpenPodDetail: func(kube.Pod, []string, int, int, int) (tea.Model, tea.Cmd) { return stubTask{}, nil },
		OpenLogs:      func(kube.Pod, int, int) (tea.Model, tea.Cmd) { return stubTask{}, nil },
	})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	kb := m.Keybar()
	var hasOpen, hasLogs bool
	for _, g := range kb.Groups {
		for _, h := range g {
			if h.Key == "↵" {
				hasOpen = true
			}
			if h.Key == "l" {
				hasLogs = true
			}
		}
	}
	if !hasOpen || !hasLogs {
		t.Fatalf("expected both open and logs hints in Pods keybar, got %+v", kb.Groups)
	}
}

// TestYOpensYAMLViewOnAnyKind confirms 'y' opens 8a for the selected row on
// a non-Pod kind (docs/design README.md: "y opens the YAML view on any
// selected object, any kind" — not gated the way OpenPodDetail/OpenLogs are).
func TestYOpensYAMLViewOnAnyKind(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindConfigMap: {configMap("default", "app-config")},
	}}
	var openedKind kube.ResourceKind
	var openedNS, openedName string
	session := newSession()
	session.Location.Kind = kube.KindConfigMap
	m := New(Config{
		Session: session, Lister: lister,
		OpenYAML: func(kind kube.ResourceKind, ns, name string, w, h int) (tea.Model, tea.Cmd) {
			openedKind, openedNS, openedName = kind, ns, name
			return stubTask{}, nil
		},
	})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	updated, _ := m.Update(tea.KeyPressMsg{Text: "y"})
	if openedKind != kube.KindConfigMap || openedNS != "default" || openedName != "app-config" {
		t.Fatalf("expected ConfigMap default/app-config opened, got kind=%s ns=%s name=%s", openedKind, openedNS, openedName)
	}
	if _, ok := updated.(stubTask); !ok {
		t.Fatalf("expected Update to return the pushed stub task, got %T", updated)
	}
}
