package browse

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
)

func crashPod(ns, name string) *corev1.Pod {
	p := pod(ns, name)
	p.Status.ContainerStatuses[0].State = corev1.ContainerState{
		Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"},
	}
	return p
}

func pendingPod(ns, name string) *corev1.Pod {
	p := pod(ns, name)
	p.Status.Phase = corev1.PodPending
	p.Status.ContainerStatuses[0].Ready = false
	p.Status.ContainerStatuses[0].State = corev1.ContainerState{
		Waiting: &corev1.ContainerStateWaiting{Reason: "ContainerCreating"},
	}
	return p
}

func TestDefaultSortIsUnhealthyFirst(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {
			pod("default", "healthy-a"),
			crashPod("default", "crashing"),
			pod("default", "healthy-b"),
			pendingPod("default", "starting"),
		},
	}}
	m := New(Config{Session: newSession(), Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	if m.state != tui.TaskStateReady {
		t.Fatalf("state = %s, want ready", m.state)
	}
	if len(m.visible) != 4 {
		t.Fatalf("visible = %d, want 4", len(m.visible))
	}
	if m.visible[0].row.Name != "crashing" {
		t.Fatalf("first row = %q, want crashing (Fail ranks first)", m.visible[0].row.Name)
	}
	if m.visible[1].row.Name != "starting" {
		t.Fatalf("second row = %q, want starting (Warn ranks second)", m.visible[1].row.Name)
	}
}

func TestFilterNarrowsAndEscClears(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {pod("default", "api-1"), pod("default", "worker-2")},
	}}
	m := New(Config{Session: newSession(), Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	updated, _ := m.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	m = *updated.(*Model)
	if !m.filterActive {
		t.Fatal("expected filterActive after '/'")
	}

	for _, r := range "api" {
		updated, _ = m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = *updated.(*Model)
	}
	if len(m.visible) != 1 || m.visible[0].row.Name != "api-1" {
		t.Fatalf("filtered visible = %+v, want just api-1", m.visible)
	}
	view := ansi.Strip(m.Render())
	if !strings.Contains(view, "hidden by filter") {
		t.Fatalf("expected hidden-by-filter notice:\n%s", view)
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = *updated.(*Model)
	if m.filterActive || m.filterQuery != "" {
		t.Fatalf("esc should clear filter, got active=%v query=%q", m.filterActive, m.filterQuery)
	}
	if len(m.visible) != 2 {
		t.Fatalf("visible after clear = %d, want 2", len(m.visible))
	}
}

func TestFilterTypingAcceptsJAndK(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {pod("default", "worker-2"), pod("default", "api-1")},
	}}
	m := New(Config{Session: newSession(), Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	updated, _ := m.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	m = *updated.(*Model)
	for _, r := range "work" {
		updated, _ = m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = *updated.(*Model)
	}
	if m.filterQuery != "work" {
		t.Fatalf("filterQuery = %q, want %q ('k' must type, not move selection)", m.filterQuery, "work")
	}
	if len(m.visible) != 1 || m.visible[0].row.Name != "worker-2" {
		t.Fatalf("filtered visible = %+v, want just worker-2", m.visible)
	}
}

func TestFilterAltJKMovesSelectionWithoutTyping(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {pod("default", "api-1"), pod("default", "api-2")},
	}}
	m := New(Config{Session: newSession(), Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	updated, _ := m.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	m = *updated.(*Model)
	if m.selected != 0 {
		t.Fatalf("selected = %d, want 0 before moving", m.selected)
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: 'j', Mod: tea.ModAlt})
	m = *updated.(*Model)
	if m.selected != 1 {
		t.Fatalf("selected = %d, want 1 after alt+j", m.selected)
	}
	if m.filterQuery != "" {
		t.Fatalf("filterQuery = %q, want empty (alt+j must move, not type)", m.filterQuery)
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: 'k', Mod: tea.ModAlt})
	m = *updated.(*Model)
	if m.selected != 0 {
		t.Fatalf("selected = %d, want 0 after alt+k", m.selected)
	}
	if m.filterQuery != "" {
		t.Fatalf("filterQuery = %q, want empty (alt+k must move, not type)", m.filterQuery)
	}
}

// TestSelectionScrollsIntoView pins tableDataRows to the real rendered
// viewport: after walking selection to the last of many rows on a short
// terminal, the selected row must actually be on screen (this once lagged —
// the budget over-counted by the FooterLine row, letting the selection sit
// one row below the visible window).
func TestSelectionScrollsIntoView(t *testing.T) {
	objs := make([]runtime.Object, 0, 12)
	for _, name := range []string{
		"pod-01", "pod-02", "pod-03", "pod-04", "pod-05", "pod-06",
		"pod-07", "pod-08", "pod-09", "pod-10", "pod-11", "pod-12",
	} {
		objs = append(objs, pod("default", name))
	}
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{kube.KindPod: objs}}
	m := New(Config{Session: newSession(), Lister: lister})
	m.SetSize(120, 11) // body 5 → table header + footer + 3 data rows
	m = step(t, m, m.Init()())

	for range 11 {
		m = step(t, m, tea.KeyPressMsg{Text: "j"})
	}
	if got := m.selectedName(); got != "pod-12" {
		t.Fatalf("selected = %q, want pod-12", got)
	}
	var selectedLine string
	for line := range strings.SplitSeq(plain(m.Render()), "\n") {
		if strings.HasPrefix(line, "▎") {
			selectedLine = line
		}
	}
	if !strings.Contains(selectedLine, "pod-12") {
		t.Fatalf("selected row must be inside the rendered viewport, got selection line %q in:\n%s", selectedLine, plain(m.Render()))
	}
}

func TestCapturingInputTracksFilterActive(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {pod("default", "api-1")},
	}}
	m := New(Config{Session: newSession(), Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	if m.CapturingInput() {
		t.Fatal("CapturingInput() should be false before '/' is pressed")
	}
	updated, _ := m.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	m = *updated.(*Model)
	if !m.CapturingInput() {
		t.Fatal("CapturingInput() should be true while filtering")
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = *updated.(*Model)
	if m.CapturingInput() {
		t.Fatal("CapturingInput() should be false after esc clears the filter")
	}
}

type fakeMetrics struct {
	metrics map[string]kube.PodMetrics
}

func (f fakeMetrics) PodMetricsByNamespace(context.Context, string) (map[string]kube.PodMetrics, error) {
	return f.metrics, nil
}

func TestPodMetricsRenderAsBars(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {pod("default", "api-1")},
	}}
	metrics := fakeMetrics{metrics: map[string]kube.PodMetrics{
		"api-1": {CPU: "45m", MEM: "128Mi", CPUMilli: 45, MemBytes: 128 * 1024 * 1024},
	}}
	m := New(Config{Session: newSession(), Lister: lister, Metrics: metrics})
	m.SetSize(120, 36)
	// Drive the row load and one metrics poll directly rather than through
	// step(m.Init()...): Init's metrics branch also schedules a recurring
	// metricsTickMsg (the real 2s poll loop), which step's cmd-draining
	// would follow forever in this synchronous test harness.
	m = step(t, m, m.load()())
	m = step(t, m, m.loadMetrics(m.metricsEpoch)())

	view := ansi.Strip(m.Render())
	if !strings.Contains(view, "45m") || !strings.Contains(view, "128Mi") {
		t.Fatalf("expected live CPU/MEM values in view:\n%s", view)
	}
}

func TestLogsKeyOpensLogsScreen(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {pod("default", "api-1")},
	}}
	opened := false
	m := New(Config{Session: newSession(), Lister: lister, OpenLogs: func(p kube.Pod, w, h int) (tea.Model, tea.Cmd) {
		opened = true
		if p.Name != "api-1" || p.Namespace != "default" {
			t.Fatalf("unexpected pod passed to OpenLogs: %+v", p)
		}
		return fakeTask{}, nil
	}})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'l', Text: "l"})
	if !opened {
		t.Fatal("expected OpenLogs to be called")
	}
	if _, ok := updated.(fakeTask); !ok {
		t.Fatalf("expected Update to return the pushed logs task, got %T", updated)
	}
}

type fakeTask struct{}

func (fakeTask) Init() tea.Cmd                       { return nil }
func (fakeTask) Update(tea.Msg) (tea.Model, tea.Cmd) { return fakeTask{}, nil }
func (fakeTask) View() tea.View                      { return tea.NewView("") }

func TestResourceChangedDebouncesReload(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {pod("default", "api-1")},
	}}
	m := New(Config{Session: newSession(), Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	updated, cmd := m.Update(kube.ResourceChangedMsg{Kind: kube.KindPod})
	m = *updated.(*Model)
	if cmd == nil {
		t.Fatal("expected a scheduled reload command")
	}
	msg := cmd()
	due, ok := msg.(reloadDueMsg)
	if !ok {
		t.Fatalf("expected reloadDueMsg, got %T", msg)
	}
	if due.epoch != m.reloadEpoch {
		t.Fatalf("epoch = %d, want %d", due.epoch, m.reloadEpoch)
	}
}
