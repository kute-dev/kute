package execpicker

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/kube/fake"
	"github.com/kute-dev/kute/internal/tui"
)

func plain(s string) string { return ansi.Strip(s) }

func newModel() Model {
	return New(Config{
		Session:   &tui.Session{Theme: tui.Dark()},
		Namespace: "default",
		PodName:   "nva-gateway-2b81x",
		Containers: []kube.ContainerInfo{
			{Name: "gateway", Image: "nva-gateway:1.19.0", State: "Running"},
			{Name: "istio-proxy", Image: "sidecar:v1.2", State: "Running"},
		},
	})
}

func TestMoveSelectionClamps(t *testing.T) {
	t.Parallel()
	m := newModel()
	m.moveSelection(-1)
	if m.selected != 0 {
		t.Fatalf("selected = %d, want clamped to 0", m.selected)
	}
	m.moveSelection(1)
	if m.selected != 1 {
		t.Fatalf("selected = %d, want 1", m.selected)
	}
	m.moveSelection(1)
	if m.selected != 1 {
		t.Fatalf("selected = %d, want clamped to 1 (last container)", m.selected)
	}
}

// TestHeaderShowsForwardChipWhenActive pins 13d: every screen's Header()
// carries the ambient forward chip — execpicker was one of two omitting it.
func TestHeaderShowsForwardChipWhenActive(t *testing.T) {
	mgr := kube.NewForwardManager()
	target := kube.ForwardTarget{Kind: kube.KindPod, Namespace: "default", Name: "worker-0"}
	mgr.Start(fake.NewForwardDialer(), fake.NewPodResolver(fake.New("default", "test")), target, "worker-0", 18080, 80, "")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(mgr.List()) == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if len(mgr.List()) == 0 {
		t.Fatal("forward session never registered")
	}

	m := newModel()
	m.session.Forwards = mgr
	if got := m.Header().ForwardChip.Text; got == "" {
		t.Fatal("expected a non-empty forward chip while a forward is active")
	}
}

func TestEscPopsBack(t *testing.T) {
	t.Parallel()
	m := newModel()
	_, cmd := m.Update(tea.KeyPressMsg(tea.Key{Text: "esc", Code: tea.KeyEscape}))
	if cmd == nil {
		t.Fatal("expected a Cmd from esc")
	}
	if _, ok := cmd().(tui.BackMsg); !ok {
		t.Fatalf("expected tui.BackMsg, got %T", cmd())
	}
}

func TestEnterExecsSelectedContainer(t *testing.T) {
	t.Parallel()
	m := newModel()
	m.selected = 1
	cmd := m.execSelected()
	if cmd == nil {
		t.Fatal("expected a non-nil exec Cmd")
	}
}

// TestEnterDemoModeSkipsRealKubectl confirms the picker's own exec (chosen
// from the multi-container list) never builds a real kubectl subprocess
// when Config.Demo is set — same guard as browse's direct single-container
// fast path, since there's no real cluster behind kube/fake to attach a tty
// to.
func TestEnterDemoModeSkipsRealKubectl(t *testing.T) {
	t.Parallel()
	m := newModel()
	m.demo = true
	m.selected = 1
	cmd := m.execSelected()
	if cmd == nil {
		t.Fatal("expected a non-nil Cmd")
	}
	msg, ok := cmd().(execResultMsg)
	if !ok {
		t.Fatalf("expected execResultMsg, got %T", msg)
	}
	if !errors.Is(msg.err, kube.ErrDemoUnavailable) {
		t.Fatalf("expected kube.ErrDemoUnavailable, got %v", msg.err)
	}
}

func TestExecResultSuccessPopsBack(t *testing.T) {
	t.Parallel()
	m := newModel()
	updated, cmd := m.Update(execResultMsg{err: nil})
	next := updated.(*Model)
	if next.feedback != "" {
		t.Fatalf("feedback = %q, want empty on a clean exit", next.feedback)
	}
	if cmd == nil {
		t.Fatal("expected a Cmd on a clean exit")
	}
	if _, ok := cmd().(tui.BackMsg); !ok {
		t.Fatalf("expected tui.BackMsg on a clean exit, got %T", cmd())
	}
}

func TestExecResultFailureSetsFeedback(t *testing.T) {
	t.Parallel()
	m := newModel()
	updated, cmd := m.Update(execResultMsg{err: errors.New("exit status 127")})
	next := updated.(*Model)
	if cmd != nil {
		t.Fatal("expected no Cmd (stay on the picker) after a non-zero exit")
	}
	if !strings.Contains(next.feedback, "exit status 127") {
		t.Fatalf("feedback = %q, want it to mention the error", next.feedback)
	}
	kb := next.Keybar()
	if !strings.Contains(kb.RightNote, "exit status 127") {
		t.Fatalf("Keybar RightNote = %q, want the feedback surfaced", kb.RightNote)
	}
}

// TestViewLabelsSidecarContainers pins 10a (docs/design README.md:141:
// "sidecars labeled sidecar"): a container flagged IsSidecar gets a visible
// "sidecar" label next to its image; a regular container must not.
func TestViewLabelsSidecarContainers(t *testing.T) {
	t.Parallel()
	m := New(Config{
		Session:   &tui.Session{Theme: tui.Dark()},
		Namespace: "default",
		PodName:   "nva-gateway-2b81x",
		Containers: []kube.ContainerInfo{
			{Name: "gateway", Image: "nva-gateway:1.19.0", State: "Running"},
			{Name: "envoy", Image: "envoy:1.28", State: "Running", IsSidecar: true},
		},
	})
	m.SetSize(120, 36)
	out := plain(m.Render())

	lines := strings.Split(out, "\n")
	var gatewayLine, envoyLine string
	for _, l := range lines {
		switch {
		case strings.Contains(l, "gateway") && !strings.Contains(l, "envoy"):
			gatewayLine = l
		case strings.Contains(l, "envoy"):
			envoyLine = l
		}
	}
	if strings.Contains(gatewayLine, "sidecar") {
		t.Errorf("regular container's line unexpectedly labeled sidecar:\n%s", gatewayLine)
	}
	if !strings.Contains(envoyLine, "sidecar") {
		t.Errorf("expected the sidecar container's line to be labeled 'sidecar', got:\n%s", envoyLine)
	}
}

// TestPanelHeaderClampsLongPodName pins a real regression: a StatefulSet pod
// name long enough to push "exec › <pod>" past panelWidth used to widen the
// whole card (Body's panelStyle carries no explicit Width, so lipgloss sizes
// it to its widest line), leaving every other fixed-width line — container
// rows, will-run — short, with blank space trailing under "N containers".
// The header must stay clamped to the same width as the rest of the panel.
func TestPanelHeaderClampsLongPodName(t *testing.T) {
	t.Parallel()
	m := New(Config{
		Session:   &tui.Session{Theme: tui.Dark()},
		Namespace: "observability",
		PodName:   "alertmanager-kube-prometheus-stack-alertmanager-0",
		Containers: []kube.ContainerInfo{
			{Name: "alertmanager", Image: "quay.io/prometheus/alertmanager:v0.33.1", State: "Running"},
			{Name: "config-reloader", Image: "quay.io/prometheus-operator/prometheus-config-reloader:v0.92.1", State: "Running"},
		},
	})
	theme := m.Theme()
	header := ansi.Strip(m.panelHeader(theme))
	row := ansi.Strip(m.containerLines(theme, 0, m.containers[0])[0])
	if hw, rw := lipgloss.Width(header), lipgloss.Width(row); hw != rw {
		t.Errorf("panel header width = %d, container row width = %d, want equal (header must clamp to panelWidth)", hw, rw)
	}
	if lipgloss.Width(header) > panelWidth {
		t.Errorf("panel header width = %d, want <= panelWidth (%d)", lipgloss.Width(header), panelWidth)
	}
	if !strings.Contains(header, "2 containers") {
		t.Errorf("panel header = %q, want it to still show the container count", header)
	}
}

func TestViewRendersContainers(t *testing.T) {
	t.Parallel()
	m := newModel()
	m.SetSize(120, 36)
	out := plain(m.Render())
	if !strings.Contains(out, "gateway") || !strings.Contains(out, "istio-proxy") {
		t.Fatalf("expected both container names in the rendered view, got:\n%s", out)
	}
	if !strings.Contains(out, "kubectl exec") {
		t.Fatalf("expected the 'will run' kubectl command in the rendered view, got:\n%s", out)
	}
}

// stubShellDetector returns a fixed answer per container, so tests (and the
// golden fixtures) exercise the shells column without spawning kubectl:
// "gateway" has bash and sh, "istio-proxy" only sh, "distroless" none, and
// anything named "denied" fails the probe.
type stubShellDetector struct{}

func (stubShellDetector) DetectShells(_ context.Context, _, _, container string) ([]string, error) {
	switch container {
	case "denied":
		return nil, errors.New("pods/exec is forbidden")
	case "distroless":
		return nil, nil
	case "istio-proxy":
		return []string{"sh"}, nil
	default:
		return []string{"bash", "sh"}, nil
	}
}

func TestShellsTextStates(t *testing.T) {
	t.Parallel()

	m := newModel()
	m.shells = stubShellDetector{}
	// Nothing detected yet — the probe is still in flight.
	if got := m.shellsText("gateway"); got != "checking…" {
		t.Errorf("pending shells cell = %q, want \"checking…\"", got)
	}

	m.detected = map[string]shellResult{
		"gateway":     {shells: []string{"bash", "sh"}},
		"istio-proxy": {shells: []string{"sh"}},
		"distroless":  {},
		"denied":      {err: errors.New("pods/exec is forbidden")},
	}
	tests := []struct {
		container, want string
	}{
		{"gateway", "bash, sh"},
		{"istio-proxy", "sh"},
		// A real answer: no shell at all. Distinct from unknown, because it
		// tells the user enter won't get them a prompt.
		{"distroless", "no shell"},
		// The probe couldn't run — never rendered as "no shell", never as a
		// fabricated list.
		{"denied", "–"},
	}
	for _, tt := range tests {
		if got := m.shellsText(tt.container); got != tt.want {
			t.Errorf("shellsText(%q) = %q, want %q", tt.container, got, tt.want)
		}
	}
}

func TestShellsTextUnknownWithoutDetector(t *testing.T) {
	t.Parallel()
	m := newModel() // Config.Shells nil
	if got := m.shellsText("gateway"); got != "–" {
		t.Errorf("shells cell without a detector = %q, want \"–\"", got)
	}
}

func TestInitProbesEveryContainer(t *testing.T) {
	t.Parallel()

	m := newModel()
	m.shells = stubShellDetector{}
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init returned no probe command")
	}
	// Drive the batch through Update and assert both containers land.
	model := tea.Model(&m)
	for _, msg := range collectBatch(t, cmd) {
		model, _ = model.Update(msg)
	}
	got := model.(*Model).detected
	if len(got) != 2 {
		t.Fatalf("detected %d containers, want 2: %v", len(got), got)
	}
	if want := []string{"bash", "sh"}; !slices.Equal(got["gateway"].shells, want) {
		t.Errorf("gateway shells = %v, want %v", got["gateway"].shells, want)
	}
	if want := []string{"sh"}; !slices.Equal(got["istio-proxy"].shells, want) {
		t.Errorf("istio-proxy shells = %v, want %v", got["istio-proxy"].shells, want)
	}
}

func TestInitWithoutDetectorProbesNothing(t *testing.T) {
	t.Parallel()
	m := newModel() // Config.Shells nil
	if cmd := m.Init(); cmd != nil {
		t.Error("Init spawned a probe with no detector configured")
	}
}

// collectBatch flattens a tea.Batch into its messages by running every
// returned Cmd. tea.BatchMsg is a []tea.Cmd, so one level of nesting is all
// Init produces.
func collectBatch(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()
	var msgs []tea.Msg
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			if c == nil {
				continue
			}
			msgs = append(msgs, c())
		}
	default:
		msgs = append(msgs, msg)
	}
	return msgs
}

// TestWillRunCommandNamesDetectedShell asserts on the command text rather
// than the rendered line: mockup 10a's panel is a fixed 56 cells, so
// willRunLine ellipsizes a realistic pod's command well before its trailing
// shell — the shells column, not this line, is where the user reads which
// shell they'll get.
func TestWillRunCommandNamesDetectedShell(t *testing.T) {
	t.Parallel()

	m := newModel()
	m.shells = stubShellDetector{}
	m.detected = map[string]shellResult{"gateway": {shells: []string{"bash", "sh"}}}
	got := kube.ExecCommandString(m.namespace, m.podName, "gateway", m.preferredShell(0))
	if !strings.HasSuffix(got, "-- bash") {
		t.Errorf("will-run command = %q, want it to end in the detected shell", got)
	}

	// With no detection result the command keeps kube.ExecSpec's own
	// in-container bash-then-sh fallback, rather than claiming a shell.
	m.detected = map[string]shellResult{}
	got = kube.ExecCommandString(m.namespace, m.podName, "gateway", m.preferredShell(0))
	if !strings.Contains(got, "command -v bash") {
		t.Errorf("undetected will-run command = %q, want the in-container fallback probe", got)
	}
}

// TestTruncateImageRefDropsRedundantDigest pins the image line's trim rule:
// a digest is dropped when a tag already names the version, and whatever's
// left ellipsizes from the front so the tag stays visible.
func TestTruncateImageRefDropsRedundantDigest(t *testing.T) {
	t.Parallel()
	const img = "quay.io/prometheus-operator/prometheus-config-reloader:v0.91.0@sha256:7d9e4eea5f1139e602508871f422b011"
	got := truncateImageRef(img, 40)
	if strings.Contains(got, "sha256") {
		t.Errorf("truncateImageRef(%q) = %q, want the redundant digest dropped", img, got)
	}
	if !strings.HasSuffix(got, "v0.91.0") {
		t.Errorf("truncateImageRef(%q) = %q, want the tag to survive at the end", img, got)
	}
	if !strings.HasPrefix(got, "…") {
		t.Errorf("truncateImageRef(%q) = %q, want it ellipsized from the front", got, got)
	}

	// No tag: the digest is the only version signal, so it's left alone
	// (still ellipsized from the front if it doesn't fit, same as any other
	// too-long reference — the point is it's never dropped outright).
	const digestOnly = "gcr.io/distroless/static@sha256:7d9e4eea5f1139e602508871f422b011"
	if got := truncateImageRef(digestOnly, 100); got != digestOnly {
		t.Errorf("truncateImageRef(%q, 100) = %q, want the untrimmed digest kept when there's no tag", digestOnly, got)
	}
}

func TestPreferredShellPassedToExec(t *testing.T) {
	t.Parallel()

	m := newModel()
	m.detected = map[string]shellResult{"gateway": {shells: []string{"bash", "sh"}}}
	if got := m.preferredShell(0); got != "bash" {
		t.Errorf("preferredShell = %q, want \"bash\" (first in preference order)", got)
	}
	// A failed probe must not resolve to a shell — that would put a command
	// on screen that may not exist in the container.
	m.detected = map[string]shellResult{"gateway": {err: errors.New("forbidden")}}
	if got := m.preferredShell(0); got != "" {
		t.Errorf("preferredShell after a failed probe = %q, want empty", got)
	}
}

// TestEnterRefusedWhileOffline pins the 4a gate on the picker itself: Exec is
// Mutating, so a connection that dropped after the picker was pushed must
// refuse enter — and say so in the panel, since this screen has no keybar
// note the user is already reading (docs/design README.md §52).
func TestEnterRefusedWhileOffline(t *testing.T) {
	t.Parallel()

	m := newModel()
	m.SetSize(120, 36)
	updated, _ := m.Update(kube.ConnStateMsg{Phase: kube.ConnReconnecting, Err: "i/o timeout"})
	m = *updated.(*Model)

	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = *updated.(*Model)
	if cmd != nil {
		t.Error("enter handed the tty to kubectl while offline")
	}
	if !strings.Contains(plain(m.Render()), "disabled while offline") {
		t.Errorf("expected the refusal in the panel, got:\n%s", plain(m.Render()))
	}

	updated, _ = m.Update(kube.ConnStateMsg{Phase: kube.ConnConnected})
	m = *updated.(*Model)
	if _, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd == nil {
		t.Error("enter stayed refused after reconnecting")
	}
}
