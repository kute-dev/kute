package execpicker

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/kute-dev/kute/internal/kube"
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

func key(s string) tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Text: s, Code: rune(s[0])})
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
