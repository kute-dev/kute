package poddetail

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/config"
	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/actions"
	"github.com/kute-dev/kute/internal/tui/verbs"
)

func plain(s string) string { return ansi.Strip(s) }

type fakeLister struct {
	objs map[kube.ResourceKind][]runtime.Object
}

func (f fakeLister) ListRaw(_ context.Context, kind kube.ResourceKind, _ string) ([]runtime.Object, error) {
	return f.objs[kind], nil
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

// crashLoopPod mirrors kube/fake/fixtures.go's demoCrashLoopPod shape: a
// container currently Waiting/CrashLoopBackOff whose LastTerminationState
// carries the exit-137/OOMKilled-style last termination the 5a banner needs.
func crashLoopPod(name, ns, node string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: ns,
			Labels:            map[string]string{"app": "worker"},
			OwnerReferences:   []metav1.OwnerReference{{Kind: "ReplicaSet", Name: "worker-abc123"}},
			CreationTimestamp: metav1.Now(),
		},
		Spec: corev1.PodSpec{
			NodeName: node,
			Containers: []corev1.Container{{
				Name:  "worker",
				Image: "example.com/worker:v1",
				Resources: corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("1"),
						corev1.ResourceMemory: resource.MustParse("512Mi"),
					},
				},
			}},
			Tolerations: []corev1.Toleration{{Key: "dedicated", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule}},
		},
		Status: corev1.PodStatus{
			Phase:    corev1.PodRunning,
			QOSClass: corev1.PodQOSBurstable,
			PodIP:    "10.0.0.5",
			ContainerStatuses: []corev1.ContainerStatus{{
				Name:         "worker",
				Ready:        false,
				RestartCount: 6,
				State:        corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}},
				LastTerminationState: corev1.ContainerState{
					Terminated: &corev1.ContainerStateTerminated{
						ExitCode:   137,
						Reason:     "OOMKilled",
						FinishedAt: metav1.Now(),
					},
				},
			}},
		},
	}
}

func runningPod(name, ns, node string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, CreationTimestamp: metav1.Now()},
		Spec: corev1.PodSpec{
			NodeName:   node,
			Containers: []corev1.Container{{Name: "app", Image: "example.com/app:v1"}},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{{
				Name: "app", Ready: true,
				State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
			}},
		},
	}
}

type fakeEvents struct {
	events []kube.Event
}

func (f fakeEvents) ObjectEvents(context.Context, string, kube.ResourceKind, string) ([]kube.Event, error) {
	return f.events, nil
}

type fakeMutator struct {
	deleted      []string
	forceDeleted []string
}

func (f *fakeMutator) DeleteResource(_ context.Context, _ kube.ResourceKind, _ string, name string) error {
	f.deleted = append(f.deleted, name)
	return nil
}
func (f *fakeMutator) DeleteResourceForced(_ context.Context, _ kube.ResourceKind, _ string, name string) error {
	f.forceDeleted = append(f.forceDeleted, name)
	return nil
}
func (f *fakeMutator) RolloutRestart(context.Context, string, string) error    { return nil }
func (f *fakeMutator) Cordon(context.Context, string, bool) error              { return nil }
func (f *fakeMutator) Drain(context.Context, string) (int, error)              { return 0, nil }
func (f *fakeMutator) HelmRollback(context.Context, string, string, int) error { return nil }
func (f *fakeMutator) Scale(context.Context, kube.ResourceKind, string, string, int32) error {
	return nil
}
func (f *fakeMutator) SetImage(context.Context, kube.ResourceKind, string, string, string, string) error {
	return nil
}
func (f *fakeMutator) SetResources(context.Context, kube.ResourceKind, string, string, string, kube.ResourceEdits, bool) error {
	return nil
}
func (f *fakeMutator) PatchMeta(context.Context, kube.ResourceKind, string, string, bool, string, string, bool) error {
	return nil
}
func (f *fakeMutator) PatchSecretData(context.Context, string, string, string, string, bool) error {
	return nil
}

func TestLoadRendersTerminationBannerMetaContainersAndEvents(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {crashLoopPod("worker-0", "default", "node-a")},
	}}
	events := fakeEvents{events: []kube.Event{{Type: "Warning", Reason: "BackOff", Message: "Back-off restarting failed container"}}}
	m := New(Config{Session: newSession(), Lister: lister, Events: events, Namespace: "default", Name: "worker-0"})
	m.SetSize(120, 40)
	m = step(t, m, m.Init()())

	if m.state != tui.TaskStateReady {
		t.Fatalf("state = %s, want ready (feedback %q)", m.state, m.feedback)
	}
	if m.pod.LastTermination == nil {
		t.Fatal("expected LastTermination to be populated from LastTerminationState")
	}

	view := plain(m.Render())
	for _, want := range []string{
		"worker-0", "CrashLoopBackOff", "6 restarts",
		"Last termination", "OOMKilled", "exit 137", "Next backoff ~5m",
		"node-a", "10.0.0.5", "Burstable", "ReplicaSet/worker-abc123",
		"CONTAINERS", "worker", "example.com/worker:v1",
		"EVENTS", "BackOff",
		"LABELS", "app=worker",
		"TOLERATIONS", "dedicated (exists):NoSchedule",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
}

// TestConnStateDrivesHeaderBadge pins that the header badge reflects the
// real connection state (mock 5a) instead of the hardcoded "watching · live"
// it used to show: connected renders green-with-latency, an outage flips it
// to the red disconnected badge.
func TestConnStateDrivesHeaderBadge(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {runningPod("api-0", "default", "node-a")},
	}}
	m := New(Config{Session: newSession(), Lister: lister, Namespace: "default", Name: "api-0"})
	m.SetSize(120, 40)
	m = step(t, m, m.Init()())

	m = step(t, m, kube.ConnStateMsg{Phase: kube.ConnConnected, Latency: 12 * time.Millisecond})
	if view := plain(m.Render()); !strings.Contains(view, "connected · 12ms") {
		t.Fatalf("expected connected badge with latency:\n%s", view)
	}

	m = step(t, m, kube.ConnStateMsg{Phase: kube.ConnReconnecting, Err: "dial timeout"})
	if view := plain(m.Render()); !strings.Contains(view, "disconnected") {
		t.Fatalf("expected disconnected badge mid-outage:\n%s", view)
	}
}

// TestKeybarGoesOfflineAndHidesDelete pins the cross-cutting 4a fix
// (docs/design README.md §52, §301): poddetail must show the OFFLINE pill
// and drop its own mutating verb (delete) from the keybar while
// disconnected, not just browse.
func TestKeybarGoesOfflineAndHidesDelete(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {runningPod("api-0", "default", "node-a")},
	}}
	mut := &fakeMutator{}
	m := New(Config{Session: newSession(), Lister: lister, Mutator: mut, Namespace: "default", Name: "api-0"})
	m.SetSize(120, 40)
	m = step(t, m, m.Init()())

	kb := m.Keybar()
	if kb.PillText != "POD" {
		t.Fatalf("PillText = %q before any outage, want POD", kb.PillText)
	}

	m = step(t, m, kube.ConnStateMsg{Phase: kube.ConnReconnecting, Err: "dial timeout"})
	kb = m.Keybar()
	if kb.Pill != tui.ModeOffline || kb.PillText != "OFFLINE" {
		t.Fatalf("Pill/PillText = %v/%q while offline, want ModeOffline/OFFLINE", kb.Pill, kb.PillText)
	}
	for _, g := range kb.Groups {
		for _, h := range g {
			if h.Key == verbs.Delete.Key {
				t.Fatalf("expected delete hint hidden while offline, got groups %+v", kb.Groups)
			}
		}
	}

	m = step(t, m, kube.ConnStateMsg{Phase: kube.ConnConnected})
	kb = m.Keybar()
	if kb.PillText != "POD" {
		t.Fatalf("PillText = %q after reconnect, want POD", kb.PillText)
	}
}

// failingEvents makes the best-effort events fetch fail — the EVENTS grid
// must say "events unavailable", never a misleading "no events".
type failingEvents struct{}

func (failingEvents) ObjectEvents(context.Context, string, kube.ResourceKind, string) ([]kube.Event, error) {
	return nil, errors.New("client rate limiter: context deadline exceeded")
}

func TestEventsFetchFailureRendersUnavailableNotEmpty(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {runningPod("api-0", "default", "node-a")},
	}}
	m := New(Config{Session: newSession(), Lister: lister, Events: failingEvents{}, Namespace: "default", Name: "api-0"})
	m.SetSize(120, 40)
	m = step(t, m, m.Init()())

	if m.state != tui.TaskStateReady {
		t.Fatalf("state = %s, want ready — an events failure must not fail the load", m.state)
	}
	view := plain(m.Render())
	if !strings.Contains(view, "events unavailable") {
		t.Fatalf("expected 'events unavailable':\n%s", view)
	}
	if strings.Contains(view, "no events") {
		t.Fatalf("failed fetch must not render as 'no events':\n%s", view)
	}
}

// TestTerminationAgeIsHumanized pins the banner's "· 19d ago" shape — the
// screenshot bug rendered LastTermination.Age's raw Go duration
// ("456h29m47s ago").
func TestTerminationAgeIsHumanized(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{45 * time.Second, "45s"},
		{4 * time.Minute, "4m"},
		{3 * time.Hour, "3h"},
		{456*time.Hour + 29*time.Minute + 47*time.Second, "19d"},
	}
	for _, tc := range cases {
		if got := shortDur(tc.d); got != tc.want {
			t.Fatalf("shortDur(%s) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

func TestNoEventsRendersEmptyNotBlank(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {runningPod("api-0", "default", "node-a")},
	}}
	m := New(Config{Session: newSession(), Lister: lister, Namespace: "default", Name: "api-0"})
	m.SetSize(120, 40)
	m = step(t, m, m.Init()())

	if m.state != tui.TaskStateReady {
		t.Fatalf("state = %s, want ready (feedback %q)", m.state, m.feedback)
	}
	if !strings.Contains(plain(m.Render()), "no events") {
		t.Fatalf("expected empty-events placeholder:\n%s", plain(m.Render()))
	}
}

func TestGonePodShowsBannerAndAnyKeyGoesBack(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{}}
	m := New(Config{Session: newSession(), Lister: lister, Namespace: "default", Name: "ghost"})
	m.SetSize(120, 40)
	m = step(t, m, m.Init()())

	if !m.gone {
		t.Fatal("expected gone=true for a pod missing from the cache")
	}
	if !m.CapturingInput() {
		t.Fatal("expected CapturingInput true while gone (every key becomes back)")
	}
	if !strings.Contains(plain(m.Render()), "Pod deleted") {
		t.Fatalf("expected gone banner:\n%s", plain(m.Render()))
	}

	_, cmd := m.Update(tea.KeyPressMsg{Text: "x"})
	if cmd == nil {
		t.Fatal("expected any key to return a command while gone")
	}
	if _, ok := cmd().(tui.BackMsg); !ok {
		t.Fatalf("expected tui.BackMsg, got %T", cmd())
	}
}

func TestEscSendsBackMsg(t *testing.T) {
	m := New(Config{Session: newSession(), Namespace: "default", Name: "api-0"})
	_, cmd := m.Update(tea.KeyPressMsg{Text: "esc"})
	if cmd == nil {
		t.Fatal("expected esc to return a command")
	}
	if _, ok := cmd().(tui.BackMsg); !ok {
		t.Fatalf("expected tui.BackMsg, got %T", cmd())
	}
}

func TestDeleteConfirmExecuteAndCancel(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {runningPod("api-0", "default", "node-a")},
	}}
	mut := &fakeMutator{}
	m := New(Config{Session: newSession(), Lister: lister, Mutator: mut, Namespace: "default", Name: "api-0"})
	m.SetSize(120, 40)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Text: "ctrl+d"})
	if !m.actions.Active() {
		t.Fatal("expected ctrl+d to open a delete confirmation")
	}

	// n cancels without deleting.
	cancelled := step(t, m, tea.KeyPressMsg{Text: "n"})
	if cancelled.actions.Active() {
		t.Fatal("expected n to close the confirmation")
	}
	if len(mut.deleted) != 0 {
		t.Fatalf("expected no delete on cancel, got %v", mut.deleted)
	}

	confirmed := step(t, m, tea.KeyPressMsg{Text: "y"})
	if len(mut.deleted) != 1 || mut.deleted[0] != "api-0" {
		t.Fatalf("expected api-0 deleted, got %v", mut.deleted)
	}
	_ = confirmed
}

// TestCtrlKArmsForceDeleteInsideInlineConfirm covers the non-prod path from
// poddetail: ctrl-k stages force-delete inside the same inline y/N confirm
// (not the PROD modal) — a bare ctrl-k runs nothing, "n" backs out to the
// plain prompt instead of cancelling, and a second "y" after re-arming
// executes DeleteResourceForced.
func TestCtrlKArmsForceDeleteInsideInlineConfirm(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {runningPod("api-0", "default", "node-a")},
	}}
	mut := &fakeMutator{}
	m := New(Config{Session: newSession(), Lister: lister, Mutator: mut, Namespace: "default", Name: "api-0"})
	m.SetSize(120, 40)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Text: "ctrl+d"})
	m = step(t, m, tea.KeyPressMsg{Text: "ctrl+k"})
	if m.actions.Tier() != actions.TierInline {
		t.Fatalf("expected force-delete to stay staged at TierInline, got %v", m.actions.Tier())
	}
	if !m.actions.ForceArmed() {
		t.Fatal("expected ctrl+k to arm force-delete")
	}
	if len(mut.deleted) != 0 || len(mut.forceDeleted) != 0 {
		t.Fatalf("expected ctrl+k alone to run nothing, deleted=%v forceDeleted=%v", mut.deleted, mut.forceDeleted)
	}
	kb := m.Keybar()
	if kb.PillText != "FORCE DELETE" {
		t.Fatalf("expected the FORCE DELETE pill once armed, got %q", kb.PillText)
	}
	if !strings.Contains(kb.RightNote, "--grace-period=0 --force") {
		t.Fatalf("expected the synced force-delete will-run line, got %q", kb.RightNote)
	}

	m = step(t, m, tea.KeyPressMsg{Text: "n"})
	if !m.actions.Active() || m.actions.ForceArmed() {
		t.Fatalf("expected n to disarm back to the plain prompt, not cancel: active=%v armed=%v", m.actions.Active(), m.actions.ForceArmed())
	}

	m = step(t, m, tea.KeyPressMsg{Text: "ctrl+k"})
	m = step(t, m, tea.KeyPressMsg{Text: "y"})
	if len(mut.forceDeleted) != 1 || mut.forceDeleted[0] != "api-0" {
		t.Fatalf("forceDeleted = %v, want [api-0]", mut.forceDeleted)
	}
	if len(mut.deleted) != 0 {
		t.Fatalf("expected the plain delete path untouched, got %v", mut.deleted)
	}
}

// TestDeleteInProdRequiresTypedName exercises 8b's PROD escalation
// end-to-end from poddetail: ctrl-d opens the type-the-name modal (not the
// inline y/N prompt), enter no-ops until the pod's name is typed in full,
// and ctrl-k escalates to force-delete.
func TestDeleteInProdRequiresTypedName(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {runningPod("api-0", "default", "node-a")},
	}}
	mut := &fakeMutator{}
	sess := newSession()
	sess.Config = config.Config{ProdContexts: []string{"test-cluster"}}
	m := New(Config{Session: sess, Lister: lister, Mutator: mut, Namespace: "default", Name: "api-0"})
	m.SetSize(120, 40)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Text: "ctrl+d"})
	if !m.actions.Active() || m.actions.Tier() != actions.TierModal {
		t.Fatalf("expected ctrl+d in a prod context to open the type-the-name modal, tier=%v", m.actions.Tier())
	}

	// A bare "y" must NOT confirm in the modal (unlike the inline tier) —
	// it types the letter "y" into the buffer instead.
	m = step(t, m, tea.KeyPressMsg{Text: "y"})
	if len(mut.deleted) != 0 {
		t.Fatalf("expected 'y' to type, not confirm, in the modal: %v", mut.deleted)
	}
	m = step(t, m, tea.KeyPressMsg{Text: "enter"})
	if len(mut.deleted) != 0 {
		t.Fatalf("expected enter to no-op before the name matches: %v", mut.deleted)
	}

	m = step(t, m, tea.KeyPressMsg{Text: "esc"})
	if m.actions.Active() {
		t.Fatal("expected esc to cancel the modal")
	}

	// Re-open and this time escalate to force-delete via ctrl-k.
	m = step(t, m, tea.KeyPressMsg{Text: "ctrl+d"})
	m = step(t, m, tea.KeyPressMsg{Text: "ctrl+k"})
	for _, r := range "api-0" {
		m = step(t, m, tea.KeyPressMsg{Text: string(r)})
	}
	m = step(t, m, tea.KeyPressMsg{Text: "enter"})
	if len(mut.forceDeleted) != 1 || mut.forceDeleted[0] != "api-0" {
		t.Fatalf("expected api-0 force-deleted, got %v", mut.forceDeleted)
	}
	if len(mut.deleted) != 0 {
		t.Fatalf("expected the plain delete path untouched, got %v", mut.deleted)
	}
}

func TestSiblingNavigationMovesAndClamps(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {
			runningPod("a", "default", "node-a"),
			runningPod("b", "default", "node-a"),
		},
	}}
	m := New(Config{
		Session: newSession(), Lister: lister, Namespace: "default", Name: "a",
		Siblings: []string{"a", "b"}, SiblingIndex: 0,
	})
	m.SetSize(120, 40)
	m = step(t, m, m.Init()())

	// k at the start is a no-op.
	before := m
	m = step(t, m, tea.KeyPressMsg{Text: "k"})
	if m.name != before.name || m.siblingIndex != before.siblingIndex {
		t.Fatalf("expected k at the start to no-op, got name=%q index=%d", m.name, m.siblingIndex)
	}

	m = step(t, m, tea.KeyPressMsg{Text: "j"})
	if m.name != "b" || m.siblingIndex != 1 {
		t.Fatalf("expected j to move to sibling 'b', got name=%q index=%d", m.name, m.siblingIndex)
	}
	if m.state != tui.TaskStateReady || m.pod.Name != "b" {
		t.Fatalf("expected reload for sibling 'b', got state=%s pod=%q", m.state, m.pod.Name)
	}

	// j at the end is a no-op.
	afterLast := m
	m = step(t, m, tea.KeyPressMsg{Text: "j"})
	if m.name != afterLast.name || m.siblingIndex != afterLast.siblingIndex {
		t.Fatalf("expected j at the end to no-op, got name=%q index=%d", m.name, m.siblingIndex)
	}
}

func TestOpenLogsHandoff(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {runningPod("api-0", "default", "node-a")},
	}}
	var openedFor string
	openLogs := func(pod kube.Pod, _, _ int) (tea.Model, tea.Cmd) {
		openedFor = pod.Name
		return sentinelTask{}, nil
	}
	m := New(Config{Session: newSession(), Lister: lister, OpenLogs: openLogs, Namespace: "default", Name: "api-0"})
	m.SetSize(120, 40)
	m = step(t, m, m.Init()())

	updated, _ := m.Update(tea.KeyPressMsg{Text: "l"})
	if _, ok := updated.(sentinelTask); !ok {
		t.Fatalf("expected 'l' to hand off to the logs task, got %T", updated)
	}
	if openedFor != "api-0" {
		t.Fatalf("openLogs called for %q, want api-0", openedFor)
	}
}

func TestOpenEventsHandoff(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {runningPod("api-0", "default", "node-a")},
	}}
	var openedKind kube.ResourceKind
	var openedNS, openedName string
	openEvents := func(kind kube.ResourceKind, ns, name string, _, _ int) (tea.Model, tea.Cmd) {
		openedKind, openedNS, openedName = kind, ns, name
		return sentinelTask{}, nil
	}
	m := New(Config{Session: newSession(), Lister: lister, OpenEvents: openEvents, Namespace: "default", Name: "api-0"})
	m.SetSize(120, 40)
	m = step(t, m, m.Init()())

	updated, _ := m.Update(tea.KeyPressMsg{Text: "e"})
	if _, ok := updated.(sentinelTask); !ok {
		t.Fatalf("expected 'e' to hand off to the events task, got %T", updated)
	}
	if openedKind != kube.KindPod || openedNS != "default" || openedName != "api-0" {
		t.Fatalf("openEvents called with (%s, %s, %s), want (Pod, default, api-0)", openedKind, openedNS, openedName)
	}
}

// TestOpenForwardHandoff pins the cross-cutting missing-verb fix (docs/design
// README.md §304, §308: "on any object row") — 'f' must push the forward
// picker for the loaded pod, the same as browse's own Pod rows already do.
func TestOpenForwardHandoff(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {runningPod("api-0", "default", "node-a")},
	}}
	var openedTarget kube.ForwardTarget
	openForward := func(target kube.ForwardTarget, _, _ int) (tea.Model, tea.Cmd) {
		openedTarget = target
		return sentinelTask{}, nil
	}
	m := New(Config{Session: newSession(), Lister: lister, OpenForward: openForward, Namespace: "default", Name: "api-0"})
	m.SetSize(120, 40)
	m = step(t, m, m.Init()())

	updated, _ := m.Update(tea.KeyPressMsg{Text: "f"})
	if _, ok := updated.(sentinelTask); !ok {
		t.Fatalf("expected 'f' to hand off to the forward picker, got %T", updated)
	}
	want := kube.ForwardTarget{Kind: kube.KindPod, Namespace: "default", Name: "api-0"}
	if openedTarget != want {
		t.Fatalf("openForward called with %+v, want %+v", openedTarget, want)
	}
}

func TestStatusClassShowsTerminatingOverStalePhase(t *testing.T) {
	// A deleted pod keeps its last real phase ("Running") until the kubelet
	// finishes tearing it down — Deleting must win regardless of Status.
	pod := kube.Pod{Status: string(corev1.PodRunning), Deleting: true}
	glyph, class, text := statusClass(pod)
	if glyph != "◌" || class != "warn" || text != "Terminating" {
		t.Fatalf("deleting pod should show ◌/warn/Terminating, got %s/%s/%s", glyph, class, text)
	}
}

type sentinelTask struct{}

func (sentinelTask) Init() tea.Cmd                       { return nil }
func (sentinelTask) Update(tea.Msg) (tea.Model, tea.Cmd) { return sentinelTask{}, nil }
func (sentinelTask) View() tea.View                      { return tea.View{} }
