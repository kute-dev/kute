package nodedetail

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/components"
	"github.com/kute-dev/kute/internal/tui/verbs"
)

// ansiFGCode renders a single character through style and extracts the
// color code lipgloss wraps it in, so color assertions compare against the
// theme's own resolved value rather than a hand-copied hex guess.
func ansiFGCode(t *testing.T, style lipgloss.Style) string {
	t.Helper()
	rendered := style.Render("X")
	re := regexp.MustCompile(`\x1b\[([0-9;]+)mX`)
	m := re.FindStringSubmatch(rendered)
	if m == nil {
		t.Fatalf("could not extract an ANSI color code from %q", rendered)
	}
	return m[1]
}

type fakeLister struct {
	objs map[kube.ResourceKind][]runtime.Object
}

func (f fakeLister) ListRaw(_ context.Context, kind kube.ResourceKind, _ string) ([]runtime.Object, error) {
	return f.objs[kind], nil
}

func plain(s string) string { return ansi.Strip(s) }

func testNode(name string, taints ...corev1.Taint) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       corev1.NodeSpec{Taints: taints},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
				{Type: corev1.NodeMemoryPressure, Status: corev1.ConditionTrue, Message: "system is low on memory"},
			},
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("4"),
				corev1.ResourceMemory: resource.MustParse("8Gi"),
				corev1.ResourcePods:   resource.MustParse("30"),
			},
		},
	}
}

func schedPod(ns, name, node string, memRequest string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: corev1.PodSpec{
			NodeName: node,
			Containers: []corev1.Container{{
				Name: "c",
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse(memRequest)},
				},
			}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{{Ready: true}}},
	}
}

func newSession() *tui.Session {
	return &tui.Session{Theme: tui.Dark(), Location: tui.Location{Context: "test-cluster"}}
}

func step(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			if c != nil {
				m = step(t, m, c())
			}
		}
		return m
	}
	updated, cmd := m.Update(msg)
	next := *updated.(*Model)
	if cmd != nil {
		return step(t, next, cmd())
	}
	return next
}

func TestLoadRendersConditionsAllocationAndPods(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindNode: {testNode("node-a", corev1.Taint{Key: "dedicated", Value: "gpu", Effect: corev1.TaintEffectNoSchedule})},
		kube.KindPod: {
			schedPod("default", "big", "node-a", "2Gi"),
			schedPod("default", "small", "node-a", "512Mi"),
			schedPod("default", "elsewhere", "other-node", "1Gi"),
		},
	}}
	m := New(Config{Session: newSession(), Lister: lister, NodeName: "node-a"})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	if m.state != tui.TaskStateReady {
		t.Fatalf("state = %s, want ready (feedback %q)", m.state, m.feedback)
	}
	if len(m.pods) != 2 {
		t.Fatalf("pods = %d, want 2 (elsewhere's pod must be excluded)", len(m.pods))
	}
	if m.pods[0].pod.Name != "big" {
		t.Fatalf("expected memory-desc sort to put 'big' first, got %q", m.pods[0].pod.Name)
	}
	if m.allocated.memBytes == 0 {
		t.Fatal("expected allocated memBytes to sum both scheduled pods' requests")
	}

	view := plain(m.Render())
	for _, want := range []string{"node-a", "CONDITIONS", "MemoryPressure", "ALLOCATED", "TAINTS", "dedicated=gpu", "big", "small"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
}

// TestNotReadyConditionRendersRedNotYellow pins 11b: a NotReady condition
// must render the same Bad/red color 11a's own nodes list uses for the
// identical NotReady signal, not Warn/yellow.
func TestNotReadyConditionRendersRedNotYellow(t *testing.T) {
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(old)

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-a"},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionFalse}},
		},
	}
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{kube.KindNode: {node}}}
	m := New(Config{Session: newSession(), Lister: lister, NodeName: "node-a"})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	view := m.Render()
	re := regexp.MustCompile(`\x1b\[([0-9;]+)mReady false`)
	match := re.FindStringSubmatch(view)
	if match == nil {
		t.Fatalf("could not find the styled 'Ready false' condition line:\n%s", plain(view))
	}
	theme := tui.Dark()
	badCode := ansiFGCode(t, lipgloss.NewStyle().Foreground(theme.Bad))
	warnCode := ansiFGCode(t, lipgloss.NewStyle().Foreground(theme.Warn))
	if !strings.Contains(match[1], badCode) {
		t.Errorf("NotReady condition color = %q, want to contain Bad %q", match[1], badCode)
	}
	if strings.Contains(match[1], warnCode) {
		t.Errorf("NotReady condition color = %q, should not be Warn %q", match[1], warnCode)
	}
}

// TestAllocationTextTurnsYellowWhenHot pins 11b: the "used / total" text
// next to a bar must also turn yellow at the same ≥70% "hot" threshold the
// bar's own fill segment already uses — previously only the bar changed
// color.
func TestAllocationTextTurnsYellowWhenHot(t *testing.T) {
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(old)

	theme := tui.Dark()
	// 8m / 10m = 80% ⇒ hot.
	hot := allocationBarLine("cpu", 8, 10, theme, func(v int64) string { return fmt.Sprintf("%dm", v) })
	// 1m / 10m = 10% ⇒ not hot.
	cool := allocationBarLine("cpu", 1, 10, theme, func(v int64) string { return fmt.Sprintf("%dm", v) })

	warnCode := ansiFGCode(t, lipgloss.NewStyle().Foreground(theme.Warn))
	dimCode := ansiFGCode(t, lipgloss.NewStyle().Foreground(theme.TextDim))

	hotTextCode := textColorBefore(t, hot, "8m / 10m")
	coolTextCode := textColorBefore(t, cool, "1m / 10m")
	if !strings.Contains(hotTextCode, warnCode) {
		t.Errorf("hot usage text color = %q, want to contain Warn %q", hotTextCode, warnCode)
	}
	if !strings.Contains(coolTextCode, dimCode) {
		t.Errorf("cool usage text color = %q, want to contain TextDim %q", coolTextCode, dimCode)
	}
}

// textColorBefore extracts the ANSI color code directly preceding word in
// line, same technique as ansiFGCode but reading an already-rendered line
// instead of rendering fresh.
func textColorBefore(t *testing.T, line, word string) string {
	t.Helper()
	re := regexp.MustCompile(regexp.QuoteMeta("\x1b[") + `([0-9;]+)m` + regexp.QuoteMeta(word))
	m := re.FindStringSubmatch(line)
	if m == nil {
		t.Fatalf("could not find a styled %q run in line:\n%q", word, line)
	}
	return m[1]
}

// TestLoadingStateRender pins 11b's applied-15a shape: the shell
// (breadcrumb, facts-panel titles, pods columns, esc-back key) paints in the
// first frame, the header/strip name what's loading, and skeleton content
// fills both panes — never a bare spinner-only blank screen (docs/design
// README.md §15a, applied to a detail screen the same way browse's own
// loading state is).
func TestLoadingStateRender(t *testing.T) {
	m := New(Config{Session: newSession(), Lister: fakeLister{}, NodeName: "node-a"})
	m.SetSize(120, 30)
	if m.state != tui.TaskStateLoading {
		t.Fatalf("state = %s, want loading", m.state)
	}
	view := plain(m.Render())

	for _, want := range []string{
		"Nodes", "node-a", // shell breadcrumb
		"loading node-a",                                                  // header timer
		"fetching node-a…", "conditions, allocation & pods load together", // strip
		"CONDITIONS", "ALLOCATED / ALLOCATABLE", "TAINTS", // real facts-panel titles
		"NAME", "NAMESPACE", "MEM", "CPU", "AGE", // real pods-table columns
		"esc", "back", // live nav key
		"facts & pods enable when data lands", // disabled-verbs note
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("loading view missing %q:\n%s", want, view)
		}
	}
	// Node/row-scoped verbs (cordon, drain, yaml, exec…) must not appear
	// while the node/pods don't exist yet.
	for _, unwanted := range []string{"cordon", "drain", "yaml", "exec", "node shell", "events"} {
		if strings.Contains(view, unwanted) {
			t.Fatalf("loading view unexpectedly shows verb %q:\n%s", unwanted, view)
		}
	}
}

// TestLoadingStateHeaderTimerAdvances checks 11b's header "· 0.4s" counting
// timer actually ticks off SpinnerTickMsg rather than staying frozen at 0s —
// same contract as browse's own loading timer.
func TestLoadingStateHeaderTimerAdvances(t *testing.T) {
	m := New(Config{Session: newSession(), Lister: fakeLister{}, NodeName: "node-a"})
	m.SetSize(120, 30)
	m.loadStartedAt = m.loadStartedAt.Add(-2 * time.Second)

	updated, _ := m.Update(components.SpinnerTickMsg(time.Now()))
	view := plain(updated.(*Model).Render())
	if !strings.Contains(view, "loading node-a · 2.") {
		t.Fatalf("expected header timer to show ~2s elapsed:\n%s", view)
	}
}

func TestUnknownNodeReturnsError(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{}}
	m := New(Config{Session: newSession(), Lister: lister, NodeName: "ghost"})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	if m.state != tui.TaskStateError {
		t.Fatalf("state = %s, want error", m.state)
	}
}

func TestEscSendsBackMsg(t *testing.T) {
	m := New(Config{Session: newSession(), NodeName: "node-a"})
	_, cmd := m.Update(tea.KeyPressMsg{Text: "esc"})
	if cmd == nil {
		t.Fatal("expected esc to return a command")
	}
	if _, ok := cmd().(tui.BackMsg); !ok {
		t.Fatalf("expected tui.BackMsg, got %T", cmd())
	}
}

func TestDKeyConfirmsThenDrains(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindNode: {testNode("node-a")},
		kube.KindPod:  {schedPod("default", "big", "node-a", "1Gi")},
	}}
	mut := &fakeMutator{}
	m := New(Config{Session: newSession(), Lister: lister, NodeName: "node-a", Mutator: mut})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Text: "D"})
	if !m.actions.Active() {
		t.Fatal("expected D to open a drain confirmation")
	}
	if !strings.Contains(plain(m.Render()), "1 pods will be evicted") {
		t.Fatalf("expected evicted-pod count in confirm body:\n%s", plain(m.Render()))
	}

	m = step(t, m, tea.KeyPressMsg{Text: "y"})
	if len(mut.drained) != 1 || mut.drained[0] != "node-a" {
		t.Fatalf("expected node-a drained, got %v", mut.drained)
	}
}

// TestKeybarGoesOfflineAndHidesCordonDrain pins the cross-cutting 4a fix
// (docs/design README.md §52, §301): nodedetail must show the OFFLINE pill
// and drop cordon/drain from the keybar while disconnected, not just browse.
func TestKeybarGoesOfflineAndHidesCordonDrain(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindNode: {testNode("node-a")},
	}}
	mut := &fakeMutator{}
	m := New(Config{Session: newSession(), Lister: lister, NodeName: "node-a", Mutator: mut})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, kube.ConnStateMsg{Phase: kube.ConnReconnecting, Err: "dial timeout"})
	kb := m.Keybar()
	if kb.Pill != tui.ModeOffline || kb.PillText != "OFFLINE" {
		t.Fatalf("Pill/PillText = %v/%q while offline, want ModeOffline/OFFLINE", kb.Pill, kb.PillText)
	}
	for _, g := range kb.Groups {
		for _, h := range g {
			if h.Key == verbs.Cordon.Key || h.Key == verbs.Drain.Key {
				t.Fatalf("expected cordon/drain hints hidden while offline, got groups %+v", kb.Groups)
			}
		}
	}

	m = step(t, m, kube.ConnStateMsg{Phase: kube.ConnConnected})
	kb = m.Keybar()
	if kb.PillText != "NODE" {
		t.Fatalf("PillText = %q after reconnect, want NODE", kb.PillText)
	}
}

func TestOpenEventsHandoff(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindNode: {testNode("node-a")},
	}}
	var openedKind kube.ResourceKind
	var openedName string
	openEvents := func(kind kube.ResourceKind, ns, name string, _, _ int) (tea.Model, tea.Cmd) {
		openedKind, openedName = kind, name
		return sentinelTask{}, nil
	}
	m := New(Config{Session: newSession(), Lister: lister, NodeName: "node-a", OpenEvents: openEvents})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	updated, _ := m.Update(tea.KeyPressMsg{Text: "e"})
	if _, ok := updated.(sentinelTask); !ok {
		t.Fatalf("expected 'e' to hand off to the events task, got %T", updated)
	}
	if openedKind != kube.KindNode || openedName != "node-a" {
		t.Fatalf("openEvents called with (%s, %s), want (Node, node-a)", openedKind, openedName)
	}
}

// TestOpenForwardHandoff pins the cross-cutting missing-verb fix (docs/design
// README.md §304, §308: "on any object row") — 'f' must push the forward
// picker for the selected pod row, the same as browse's own Pod rows
// already do.
func TestOpenForwardHandoff(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindNode: {testNode("node-a")},
		kube.KindPod:  {schedPod("default", "big", "node-a", "1Gi")},
	}}
	var openedTarget kube.ForwardTarget
	openForward := func(target kube.ForwardTarget, _, _ int) (tea.Model, tea.Cmd) {
		openedTarget = target
		return sentinelTask{}, nil
	}
	m := New(Config{Session: newSession(), Lister: lister, NodeName: "node-a", OpenForward: openForward})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	updated, _ := m.Update(tea.KeyPressMsg{Text: "f"})
	if _, ok := updated.(sentinelTask); !ok {
		t.Fatalf("expected 'f' to hand off to the forward picker, got %T", updated)
	}
	want := kube.ForwardTarget{Kind: kube.KindPod, Namespace: "default", Name: "big"}
	if openedTarget != want {
		t.Fatalf("openForward called with %+v, want %+v", openedTarget, want)
	}
}

type sentinelTask struct{}

func (sentinelTask) Init() tea.Cmd                       { return nil }
func (sentinelTask) Update(tea.Msg) (tea.Model, tea.Cmd) { return sentinelTask{}, nil }
func (sentinelTask) View() tea.View                      { return tea.View{} }

type fakeMutator struct {
	drained []string
}

func (f *fakeMutator) DeleteResource(context.Context, kube.ResourceKind, string, string) error {
	return nil
}
func (f *fakeMutator) DeleteResourceForced(context.Context, kube.ResourceKind, string, string) error {
	return nil
}
func (f *fakeMutator) RolloutRestart(context.Context, string, string) error    { return nil }
func (f *fakeMutator) Cordon(context.Context, string, bool) error              { return nil }
func (f *fakeMutator) HelmRollback(context.Context, string, string, int) error { return nil }
func (f *fakeMutator) Scale(context.Context, kube.ResourceKind, string, string, int32) error {
	return nil
}
func (f *fakeMutator) Drain(_ context.Context, node string) (int, error) {
	f.drained = append(f.drained, node)
	return 1, nil
}
