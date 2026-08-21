package debugpanel

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/config"
	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/state"
	"github.com/kute-dev/kute/internal/tui"
)

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
