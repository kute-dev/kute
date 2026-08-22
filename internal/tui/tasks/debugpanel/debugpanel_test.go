package debugpanel

import (
	"context"
	"errors"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/config"
	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/state"
	"github.com/kute-dev/kute/internal/tui"
)

// fakePodLister is a minimal resources.RawLister test double for §41d's
// findNodeDebugPodCmd — only Pod is ever queried, so it ignores kind/
// namespace entirely, mirroring nodedetail's own fakeLister.
type fakePodLister struct {
	pods []runtime.Object
	err  error
}

func (f fakePodLister) ListRaw(context.Context, kube.ResourceKind, string) ([]runtime.Object, error) {
	return f.pods, f.err
}

// fakeMutator is a non-nil kube.Mutator stand-in for tests that only need
// actions.Controller.Begin to get past its "mutator configured" check —
// embedding the (nil) interface satisfies every method signature without
// implementing any of them, which is fine as long as the test never
// actually confirms the action.
type fakeMutator struct{ kube.Mutator }

func nodeDebuggerPod(namespace, name, node string, created time.Time) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name, CreationTimestamp: metav1.Time{Time: created}},
		Spec:       corev1.PodSpec{NodeName: node},
	}
}

func nonProdSession() *tui.Session {
	return &tui.Session{Theme: tui.Dark(), Location: tui.Location{Context: "dev"}}
}

func prodSession() *tui.Session {
	return &tui.Session{
		Theme:    tui.Dark(),
		Location: tui.Location{Context: "prod-cluster"},
		Config:   config.Config{ProdContexts: []string{"prod-cluster"}},
	}
}

func podContainers() []kube.ContainerInfo {
	return []kube.ContainerInfo{
		{Name: "app", Image: "app:1.0", State: "Running"},
		{Name: "sidecar", Image: "sidecar:1.0", State: "Running", IsSidecar: true},
	}
}

// TestNewNodeTargetDefaults confirms the panel opens prefilled with the
// retired 's' NodeShell verb's exact defaults (Legacy NodeShell retirement:
// an unmodified enter outside PROD must reproduce the identical launch).
func TestNewNodeTargetDefaults(t *testing.T) {
	t.Parallel()
	m := New(Config{Session: nonProdSession(), IsNode: true, Name: "node-a", NodePodCount: 12})
	if m.nodeImage != kube.DefaultNodeShellImage {
		t.Errorf("nodeImage = %q, want %q", m.nodeImage, kube.DefaultNodeShellImage)
	}
	if m.nodeProfile != kube.ProfileSysadmin {
		t.Errorf("nodeProfile = %q, want %q", m.nodeProfile, kube.ProfileSysadmin)
	}
	if m.nodePodCount != 12 {
		t.Errorf("nodePodCount = %d, want 12", m.nodePodCount)
	}
}

// TestNewNodeTargetUsesConfiguredImage confirms config.Config.NodeShellImage
// (the retired 's' NodeShell verb's own override knob, still documented on
// the config field) reaches the panel's default image, rather than being
// silently ignored in favor of kube.DefaultNodeShellImage.
func TestNewNodeTargetUsesConfiguredImage(t *testing.T) {
	t.Parallel()
	m := New(Config{Session: nonProdSession(), IsNode: true, Name: "node-a", NodeShellImage: "registry.internal/busybox:1.37"})
	if m.nodeImage != "registry.internal/busybox:1.37" {
		t.Errorf("nodeImage = %q, want configured override", m.nodeImage)
	}
}

// TestNewPodTargetDefaultsToAttach confirms a running pod defaults to
// modeAttach.
func TestNewPodTargetDefaultsToAttach(t *testing.T) {
	t.Parallel()
	m := New(Config{Session: nonProdSession(), Namespace: "default", Name: "api-0", Containers: podContainers()})
	if m.mode != modeAttach {
		t.Errorf("mode = %v, want modeAttach for a running pod", m.mode)
	}
	if m.attachImage != kube.DefaultDebugImage {
		t.Errorf("attachImage = %q, want %q", m.attachImage, kube.DefaultDebugImage)
	}
	if got := m.attachTargetContainer().Name; got != "app" {
		t.Errorf("default attach target = %q, want the first non-sidecar container, app", got)
	}
}

// TestNewPodTargetDefaultsToCopyOnCrashLoop confirms the confirmed §41c
// decision: CrashLoopBackOff defaults to modeCopy.
func TestNewPodTargetDefaultsToCopyOnCrashLoop(t *testing.T) {
	t.Parallel()
	m := New(Config{
		Session: nonProdSession(), Namespace: "default", Name: "worker-0",
		Containers: podContainers(), PodPhase: "CrashLoopBackOff",
	})
	if m.mode != modeCopy {
		t.Errorf("mode = %v, want modeCopy for a CrashLoopBackOff pod", m.mode)
	}
	if m.copyName != "worker-0-debug" {
		t.Errorf("copyName = %q, want worker-0-debug", m.copyName)
	}
}

// TestInitialTargetContainerPreselects confirms §41a's fork: execpicker's
// chosen container pre-selects the attach target.
func TestInitialTargetContainerPreselects(t *testing.T) {
	t.Parallel()
	m := New(Config{
		Session: nonProdSession(), Namespace: "default", Name: "api-0",
		Containers: podContainers(), InitialTargetContainer: "sidecar",
	})
	if got := m.attachTargetContainer().Name; got != "sidecar" {
		t.Errorf("attach target = %q, want sidecar (execpicker's own selection)", got)
	}
}

// TestCycleModeBlockedWhenPodWontStayRunning confirms attach is
// hard-disabled (not just dim) while the pod won't stay running — the
// confirmed decision.
func TestCycleModeBlockedWhenPodWontStayRunning(t *testing.T) {
	t.Parallel()
	m := New(Config{
		Session: nonProdSession(), Namespace: "default", Name: "worker-0",
		Containers: podContainers(), Waiting: true,
	})
	if m.mode != modeCopy {
		t.Fatalf("expected modeCopy by default, got %v", m.mode)
	}
	m.cycleMode()
	if m.mode != modeCopy {
		t.Errorf("cycleMode() moved to %v while the pod can't stay running, want it to stay modeCopy", m.mode)
	}
}

// TestCycleModeAllowedForRunningPod confirms mode does cycle normally once
// attach is viable.
func TestCycleModeAllowedForRunningPod(t *testing.T) {
	t.Parallel()
	m := New(Config{Session: nonProdSession(), Namespace: "default", Name: "api-0", Containers: podContainers()})
	m.cycleMode()
	if m.mode != modeCopy {
		t.Errorf("mode = %v, want modeCopy after one cycle from modeAttach", m.mode)
	}
	m.cycleMode()
	if m.mode != modeAttach {
		t.Errorf("mode = %v, want modeAttach after cycling back", m.mode)
	}
}

// TestProfileCycleWraps confirms 'p' steps through DebugProfiles and wraps.
func TestProfileCycleWraps(t *testing.T) {
	t.Parallel()
	m := New(Config{Session: nonProdSession(), Namespace: "default", Name: "api-0", Containers: podContainers()})
	if m.attachProfile != kube.ProfileGeneral {
		t.Fatalf("expected default profile general, got %q", m.attachProfile)
	}
	for _, want := range []kube.DebugProfile{kube.ProfileSysadmin, kube.ProfileNetadmin, kube.ProfileRestricted, kube.ProfileGeneral} {
		m.cycleAttachProfile()
		if m.attachProfile != want {
			t.Errorf("attachProfile = %q, want %q", m.attachProfile, want)
		}
	}
}

// TestBeginLaunchNonProdRunsImmediately confirms TierNone outside PROD:
// enter launches without staging a confirm.
func TestBeginLaunchNonProdRunsImmediately(t *testing.T) {
	t.Parallel()
	m := New(Config{Session: nonProdSession(), Namespace: "default", Name: "api-0", Containers: podContainers()})
	cmd := m.beginLaunch()
	if cmd == nil {
		t.Fatal("expected a non-nil launch Cmd outside PROD")
	}
	if m.launchPending {
		t.Error("launchPending should stay false outside PROD")
	}
}

// TestLaunchCmdDemoModeSkipsRealKubectl confirms launchCmd never builds a
// real kubectl debug subprocess when Config.Demo is set, for all three
// launch shapes (node, attach, copy) — each resolves synchronously to
// kube.ErrDemoUnavailable instead, since there's no real cluster behind
// kube/fake to attach a tty to.
func TestLaunchCmdDemoModeSkipsRealKubectl(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		cfg  Config
	}{
		{"node", Config{Session: nonProdSession(), Demo: true, IsNode: true, Name: "node-a"}},
		{"attach", Config{Session: nonProdSession(), Demo: true, Namespace: "default", Name: "api-0", Containers: podContainers()}},
		{"copy", Config{Session: nonProdSession(), Demo: true, Namespace: "default", Name: "worker-0", Containers: podContainers(), Waiting: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := New(tc.cfg)
			cmd := m.launchCmd()
			if cmd == nil {
				t.Fatal("expected a non-nil Cmd")
			}
			msg, ok := cmd().(launchResultMsg)
			if !ok {
				t.Fatalf("expected launchResultMsg, got %T", msg)
			}
			if !errors.Is(msg.err, kube.ErrDemoUnavailable) {
				t.Fatalf("expected kube.ErrDemoUnavailable, got %v", msg.err)
			}
		})
	}
}

// TestBeginLaunchProdStagesConfirm confirms TierInline in PROD: enter
// stages launchPending and returns no Cmd until 'y' confirms.
func TestBeginLaunchProdStagesConfirm(t *testing.T) {
	t.Parallel()
	m := New(Config{Session: prodSession(), Namespace: "default", Name: "api-0", Containers: podContainers()})
	cmd := m.beginLaunch()
	if cmd != nil {
		t.Fatal("expected a nil Cmd while PROD's confirm is staged")
	}
	if !m.launchPending {
		t.Fatal("expected launchPending true in PROD")
	}
	updated, confirmCmd := m.updateLaunchConfirmKey(tea.KeyPressMsg{Text: "y"})
	next := updated.(*Model)
	if next.launchPending {
		t.Error("launchPending should clear on confirm")
	}
	if confirmCmd == nil {
		t.Error("expected a non-nil launch Cmd after confirming")
	}
}

// TestBeginLaunchProdCancels confirms 'n'/'esc' cancels the staged confirm
// without launching.
func TestBeginLaunchProdCancels(t *testing.T) {
	t.Parallel()
	m := New(Config{Session: prodSession(), IsNode: true, Name: "node-a"})
	m.beginLaunch()
	updated, cmd := m.updateLaunchConfirmKey(tea.KeyPressMsg{Text: "n"})
	next := updated.(*Model)
	if next.launchPending {
		t.Error("launchPending should clear on cancel")
	}
	if cmd != nil {
		t.Error("expected a nil Cmd on cancel")
	}
}

// TestHandleLaunchResultCopyModeStaysOnScreen confirms §41c's contract: a
// clean copy-mode exit does not pop back — it registers the copy with
// Session.DebugCopies and shows the CLEAN UP prompt instead.
func TestHandleLaunchResultCopyModeStaysOnScreen(t *testing.T) {
	t.Parallel()
	sess := nonProdSession()
	sess.DebugCopies = kube.NewDebugCopyRegistry()
	m := New(Config{Session: sess, Namespace: "default", Name: "worker-0", Containers: podContainers(), Waiting: true})

	updated, cmd := m.handleLaunchResult(launchResultMsg{tgt: targetPod, mode: modeCopy, copyName: "worker-0-debug"})
	next := updated.(*Model)
	if cmd != nil {
		t.Error("expected a nil Cmd — copy mode stays on screen, it doesn't pop back")
	}
	if next.cleanup == nil || next.cleanup.name != "worker-0-debug" {
		t.Fatalf("expected cleanup staged for worker-0-debug, got %+v", next.cleanup)
	}
	if !sess.DebugCopies.Contains("default", "worker-0-debug") {
		t.Error("expected the copy pod registered in Session.DebugCopies")
	}
}

// TestHandleLaunchResultAttachModePopsBack confirms a clean attach-mode
// exit pops back, matching execpicker's own contract.
func TestHandleLaunchResultAttachModePopsBack(t *testing.T) {
	t.Parallel()
	m := New(Config{Session: nonProdSession(), Namespace: "default", Name: "api-0", Containers: podContainers()})
	_, cmd := m.handleLaunchResult(launchResultMsg{tgt: targetPod, mode: modeAttach})
	if cmd == nil {
		t.Fatal("expected a non-nil Cmd (tui.BackMsg) on a clean attach exit")
	}
	if msg := cmd(); msg != (tui.BackMsg{}) {
		t.Errorf("expected tui.BackMsg, got %#v", msg)
	}
}

// TestHandleLaunchResultNodeErrorUsesLegacyWording pins "Legacy NodeShell
// retirement": the exact former 's' error string, reused verbatim.
func TestHandleLaunchResultNodeErrorUsesLegacyWording(t *testing.T) {
	t.Parallel()
	m := New(Config{Session: nonProdSession(), IsNode: true, Name: "node-a"})
	updated, cmd := m.handleLaunchResult(launchResultMsg{tgt: targetNode, err: boomError{}})
	next := updated.(*Model)
	if cmd != nil {
		t.Error("expected a nil Cmd on a failed launch — stay on the panel")
	}
	if want := "node shell exited: boom"; next.feedback != want {
		t.Errorf("feedback = %q, want %q", next.feedback, want)
	}
}

type boomError struct{}

func (boomError) Error() string { return "boom" }

// TestCleanUpKeybarShowsWhilePending confirms the CLEAN UP band renders
// once a copy is staged.
func TestCleanUpKeybarShowsWhilePending(t *testing.T) {
	t.Parallel()
	m := New(Config{Session: nonProdSession(), Namespace: "default", Name: "worker-0", Containers: podContainers()})
	m.cleanup = &cleanupPrompt{name: "worker-0-debug"}
	kb := m.Keybar()
	if kb.PillText != "CLEAN UP" {
		t.Errorf("PillText = %q, want CLEAN UP", kb.PillText)
	}
}

// TestBeginLaunchRecordsRecentImageAttach confirms launching an attach
// debug records the launched image into this context's persisted recents
// (state schema v3, PerContext.RecentDebugImages) — the wiring the design
// mockup's "i edit · recents" hint promises.
func TestBeginLaunchRecordsRecentImageAttach(t *testing.T) {
	t.Parallel()
	sess := nonProdSession()
	m := New(Config{Session: sess, Namespace: "default", Name: "api-0", Containers: podContainers()})
	m.attachImage = "nicolaka/netshoot"
	m.beginLaunch()
	got := sess.State.PerContext["dev"].RecentDebugImages
	if len(got) != 1 || got[0] != "nicolaka/netshoot" {
		t.Errorf("RecentDebugImages = %v, want [nicolaka/netshoot]", got)
	}
}

// TestBeginLaunchRecordsRecentImageNode mirrors the attach case for a node
// target's own image field.
func TestBeginLaunchRecordsRecentImageNode(t *testing.T) {
	t.Parallel()
	sess := nonProdSession()
	m := New(Config{Session: sess, IsNode: true, Name: "node-a"})
	m.nodeImage = "busybox:1.37"
	m.beginLaunch()
	got := sess.State.PerContext["dev"].RecentDebugImages
	if len(got) != 1 || got[0] != "busybox:1.37" {
		t.Errorf("RecentDebugImages = %v, want [busybox:1.37]", got)
	}
}

// TestBeginLaunchCopyModeRecordsNoRecentImage confirms copy mode never adds
// to the recents list — it has no image field of its own, per that field's
// own doc comment.
func TestBeginLaunchCopyModeRecordsNoRecentImage(t *testing.T) {
	t.Parallel()
	sess := nonProdSession()
	m := New(Config{Session: sess, Namespace: "default", Name: "worker-0", Containers: podContainers(), Waiting: true})
	m.beginLaunch()
	if got := sess.State.PerContext["dev"].RecentDebugImages; len(got) != 0 {
		t.Errorf("RecentDebugImages = %v, want none from a copy-mode launch", got)
	}
}

// TestCycleRecentImageOnTab confirms 'tab' inside the open image field
// walks forward through the persisted recents list and wraps back to the
// most-recent entry once exhausted.
func TestCycleRecentImageOnTab(t *testing.T) {
	t.Parallel()
	sess := nonProdSession()
	m := New(Config{Session: sess, Namespace: "default", Name: "api-0", Containers: podContainers()})
	m.session.State.PerContext = map[string]state.PerContext{
		"dev": {RecentDebugImages: []string{"myregistry/debug-tools:v2", "internal/toolbox:latest"}},
	}
	m.beginImageOrCopyNameEdit()
	updated, _ := m.updateEditKey(tea.KeyPressMsg{Text: "tab"})
	next := updated.(*Model)
	if got := next.editInput.Value(); got != "myregistry/debug-tools:v2" {
		t.Errorf("after first tab, value = %q, want myregistry/debug-tools:v2", got)
	}
	updated, _ = next.updateEditKey(tea.KeyPressMsg{Text: "tab"})
	next = updated.(*Model)
	if got := next.editInput.Value(); got != "internal/toolbox:latest" {
		t.Errorf("after second tab, value = %q, want internal/toolbox:latest", got)
	}
	updated, _ = next.updateEditKey(tea.KeyPressMsg{Text: "tab"})
	next = updated.(*Model)
	if got := next.editInput.Value(); got != "myregistry/debug-tools:v2" {
		t.Errorf("after third tab (wrap), value = %q, want myregistry/debug-tools:v2", got)
	}
}

// TestHandleLaunchResultNodeCleanExitLooksUpPod confirms a clean node-debug
// exit no longer pops back immediately (the bug report this feature fixes:
// a node-debugger pod left running with no way back to it from kute) —
// instead it kicks off findNodeDebugPodCmd.
func TestHandleLaunchResultNodeCleanExitLooksUpPod(t *testing.T) {
	t.Parallel()
	m := New(Config{Session: nonProdSession(), IsNode: true, Name: "node-a"})
	updated, cmd := m.handleLaunchResult(launchResultMsg{tgt: targetNode})
	next := updated.(*Model)
	if next.cleanup != nil {
		t.Error("cleanup must not be staged before the pod is found")
	}
	if cmd == nil {
		t.Fatal("expected a non-nil Cmd — the node-debug pod lookup")
	}
	msg, ok := cmd().(nodeDebugPodLookupMsg)
	if !ok {
		t.Fatalf("expected a nodeDebugPodLookupMsg, got %#v", cmd())
	}
	if msg.found {
		t.Error("expected not-found with no lister wired")
	}
}

// TestFindNodeDebugPodCmdFindsNewestMatch confirms the lookup picks the
// newest node-debugger-<node>-* pod scheduled on the right node, created no
// earlier than after — and skips an older orphaned one left on the same
// node from a previous session (the exact leftover this feature is meant
// to stop accumulating).
func TestFindNodeDebugPodCmdFindsNewestMatch(t *testing.T) {
	t.Parallel()
	launchedAt := time.Now()
	lister := fakePodLister{pods: []runtime.Object{
		nodeDebuggerPod("kube-system", "node-debugger-node-a-old", "node-a", launchedAt.Add(-time.Hour)),
		nodeDebuggerPod("kube-system", "node-debugger-node-b-fresh", "node-b", launchedAt.Add(time.Second)),
		nodeDebuggerPod("default", "node-debugger-node-a-fresh", "node-a", launchedAt.Add(time.Second)),
		nodeDebuggerPod("default", "unrelated-pod", "node-a", launchedAt.Add(time.Second)),
	}}
	m := New(Config{Session: nonProdSession(), IsNode: true, Name: "node-a", Lister: lister})
	msg, ok := m.findNodeDebugPodCmd(launchedAt, 0)().(nodeDebugPodLookupMsg)
	if !ok {
		t.Fatalf("expected a nodeDebugPodLookupMsg, got %#v", msg)
	}
	if !msg.found {
		t.Fatal("expected the fresh same-node pod to be found")
	}
	if msg.namespace != "default" || msg.name != "node-debugger-node-a-fresh" {
		t.Errorf("found %s/%s, want default/node-debugger-node-a-fresh", msg.namespace, msg.name)
	}
}

// TestHandleNodeDebugPodLookupFoundStagesCleanup confirms a successful
// lookup stages §41d's own CLEAN UP prompt with the pod's discovered
// namespace, mirroring §41c's copy-mode prompt.
func TestHandleNodeDebugPodLookupFoundStagesCleanup(t *testing.T) {
	t.Parallel()
	m := New(Config{Session: nonProdSession(), IsNode: true, Name: "node-a"})
	updated, cmd := m.handleNodeDebugPodLookup(nodeDebugPodLookupMsg{namespace: "kube-system", name: "node-debugger-node-a-xyz", found: true})
	next := updated.(*Model)
	if cmd != nil {
		t.Error("expected a nil Cmd once the prompt is staged")
	}
	if next.cleanup == nil || next.cleanup.namespace != "kube-system" || next.cleanup.name != "node-debugger-node-a-xyz" {
		t.Fatalf("expected cleanup staged for kube-system/node-debugger-node-a-xyz, got %+v", next.cleanup)
	}
}

// TestHandleNodeDebugPodLookupRetriesThenGivesUp confirms a not-found
// result retries up to nodeDebugLookupMaxAttempts before finally popping
// back, rather than retrying forever.
func TestHandleNodeDebugPodLookupRetriesThenGivesUp(t *testing.T) {
	t.Parallel()
	m := New(Config{Session: nonProdSession(), IsNode: true, Name: "node-a"})
	updated, cmd := m.handleNodeDebugPodLookup(nodeDebugPodLookupMsg{attempt: nodeDebugLookupMaxAttempts - 1})
	if updated.(*Model).cleanup != nil {
		t.Error("cleanup must stay unset once the lookup gives up")
	}
	if cmd == nil {
		t.Fatal("expected a non-nil Cmd (tui.BackMsg) once attempts are exhausted")
	}
	if msg := cmd(); msg != (tui.BackMsg{}) {
		t.Errorf("expected tui.BackMsg, got %#v", msg)
	}
}

// TestBeginCleanupDeleteUsesCleanupNamespace confirms the cleanup delete
// targets the pod's own discovered namespace rather than m.namespace, which
// is empty for a Node target — the field beginCleanupDelete used before
// §41d's CLEAN UP prompt existed for nodes.
func TestBeginCleanupDeleteUsesCleanupNamespace(t *testing.T) {
	t.Parallel()
	m := New(Config{Session: nonProdSession(), Mutator: fakeMutator{}, IsNode: true, Name: "node-a"})
	m.cleanup = &cleanupPrompt{namespace: "kube-system", name: "node-debugger-node-a-xyz"}
	// TierInline (non-prod delete) stages a pending action and returns a
	// nil Cmd — the confirm itself waits on 'y', which this test doesn't
	// press — so the pending Scope is the thing to assert on here.
	m.beginCleanupDelete()
	if !m.actions.Active() {
		t.Fatal("expected the cleanup delete confirm to be staged")
	}
	pending := m.actions.Pending()
	if pending == nil {
		t.Fatal("expected a pending action")
	}
	if pending.Scope.Namespace != "kube-system" || pending.Scope.ResourceName != "node-debugger-node-a-xyz" {
		t.Errorf("Scope = %+v, want namespace kube-system, name node-debugger-node-a-xyz", pending.Scope)
	}
}
