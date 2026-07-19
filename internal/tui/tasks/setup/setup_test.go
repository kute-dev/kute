package setup

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
)

func plain(s string) string { return ansi.Strip(s) }

func step(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	updated, _ := m.Update(msg)
	return *updated.(*Model)
}

// TestUnreachableBodyShowsCountdownAndSwitchContext pins 4c: the title
// names the failing context, the raw error renders verbatim, the retry
// countdown reflects Conn, and other kubeconfig contexts are listed.
func TestUnreachableBodyShowsCountdownAndSwitchContext(t *testing.T) {
	m := New(Config{
		Session:       &tui.Session{Theme: tui.Dark()},
		State:         Unreachable,
		ClusterName:   "microk8s-cluster",
		OtherContexts: []string{"prod-eks", "kind-local"},
	})
	m.SetSize(120, 30)
	m = step(t, m, kube.ConnStateMsg{
		Phase:       kube.ConnReconnecting,
		Err:         "dial tcp 10.0.0.5:16443: i/o timeout",
		Attempt:     2,
		NextRetryAt: m.now.Add(8 * time.Second),
	})

	body := plain(m.Render())
	if !strings.Contains(body, "microk8s-cluster is unreachable") {
		t.Errorf("body missing the title:\n%s", body)
	}
	if !strings.Contains(body, "dial tcp 10.0.0.5:16443: i/o timeout") {
		t.Errorf("body missing the verbatim error:\n%s", body)
	}
	if !strings.Contains(body, "retrying in 8s") || !strings.Contains(body, "attempt 2") {
		t.Errorf("body missing the retry countdown:\n%s", body)
	}
	if !strings.Contains(body, "prod-eks") || !strings.Contains(body, "kind-local") {
		t.Errorf("body missing the SWITCH CONTEXT preview:\n%s", body)
	}
	if !strings.Contains(body, "connecting failed") {
		t.Errorf("header badge should read connecting failed:\n%s", body)
	}
	// docs/design README.md §4c: the raw error box is bordered, matching
	// 10b's own LOOKED IN box — previously it rendered as plain tinted text
	// with no border at all.
	if !strings.Contains(body, "╭") || !strings.Contains(body, "╰") {
		t.Errorf("expected the raw error box to render a rounded border:\n%s", body)
	}
}

// TestUnreachableBodyWrapsLongErrorWithoutTruncating pins 4c's word-wrap
// truncation bug: a long connection error that lipgloss wraps onto a second
// line inside the raw-error box must keep that second line's full text, not
// collapse it down to a bare "…" (components.Pad/Truncate previously
// measured the whole wrapped, multi-line string as one run).
func TestUnreachableBodyWrapsLongErrorWithoutTruncating(t *testing.T) {
	longErr := "dial tcp 10.0.0.5:16443: i/o timeout after 30s waiting for the apiserver TLS handshake to complete"
	m := New(Config{
		Session:     &tui.Session{Theme: tui.Dark()},
		State:       Unreachable,
		ClusterName: "microk8s-cluster",
	})
	m.SetSize(120, 30)
	m = step(t, m, kube.ConnStateMsg{
		Phase:       kube.ConnReconnecting,
		Err:         longErr,
		Attempt:     1,
		NextRetryAt: m.now.Add(4 * time.Second),
	})

	body := plain(m.Render())
	if !strings.Contains(body, "apiserver TLS handshake to complete") {
		t.Errorf("expected the wrapped error's tail to survive intact, got:\n%s", body)
	}
}

// TestNoConfigBodyShowsLookedInBox pins 10b: a *kube.ConfigLookupError
// renders as the LOOKED IN box, one row per path checked.
func TestNoConfigBodyShowsLookedInBox(t *testing.T) {
	lookup := &kube.ConfigLookupError{Paths: []kube.PathCheck{
		{Label: "$KUBECONFIG", Reason: "not set"},
		{Label: "~/.kube/config", Path: "/home/x/.kube/config", Reason: "no such file"},
	}}
	m := New(Config{Session: &tui.Session{Theme: tui.Dark()}, State: NoConfig, Err: lookup})
	m.SetSize(120, 30)

	body := plain(m.Render())
	if !strings.Contains(body, "no kubeconfig found") {
		t.Errorf("body missing the headline:\n%s", body)
	}
	if !strings.Contains(body, "$KUBECONFIG") || !strings.Contains(body, "not set") {
		t.Errorf("body missing the $KUBECONFIG row:\n%s", body)
	}
	if !strings.Contains(body, "/home/x/.kube/config") || !strings.Contains(body, "no such file") {
		t.Errorf("body missing the ~/.kube/config row:\n%s", body)
	}
	if !strings.Contains(body, "no cluster") {
		t.Errorf("header badge should read no cluster:\n%s", body)
	}
}

// TestRetryKeyDispatch pins the three "r" behaviors: Unreachable's plain
// retry calls RetryNow (no rebuild); everything else calls Reconnect.
func TestRetryKeyDispatch(t *testing.T) {
	t.Run("Unreachable calls RetryNow, not Reconnect", func(t *testing.T) {
		var retried, reconnected bool
		m := New(Config{
			State:     Unreachable,
			RetryNow:  func() { retried = true },
			Reconnect: func(string) tea.Cmd { reconnected = true; return nil },
		})
		m = step(t, m, tea.KeyPressMsg{Text: "r"})
		if !retried || reconnected {
			t.Fatalf("retried=%v reconnected=%v, want retried only", retried, reconnected)
		}
	})

	t.Run("NoConfig calls Reconnect", func(t *testing.T) {
		var gotPath string
		called := false
		m := New(Config{
			State:     NoConfig,
			Reconnect: func(p string) tea.Cmd { called, gotPath = true, p; return nil },
		})
		m = step(t, m, tea.KeyPressMsg{Text: "r"})
		if !called || gotPath != "" {
			t.Fatalf("called=%v gotPath=%q, want called with empty path", called, gotPath)
		}
	})
}

// TestEditKubeconfigPathFlow pins the 'k'/'e' inline path editor: typing
// builds up pathInput, Enter submits it to Reconnect, Esc cancels.
func TestEditKubeconfigPathFlow(t *testing.T) {
	var gotPath string
	m := New(Config{
		State:          NoConfig,
		KubeconfigPath: "/old/path",
		Reconnect:      func(p string) tea.Cmd { gotPath = p; return nil },
	})
	m = step(t, m, tea.KeyPressMsg{Text: "k"})
	if !m.editing || m.pathInput != "/old/path" {
		t.Fatalf("editing=%v pathInput=%q after 'k', want editing with the current path prefilled", m.editing, m.pathInput)
	}
	if !m.CapturingInput() {
		t.Fatalf("CapturingInput() = false while editing")
	}

	m = step(t, m, tea.KeyPressMsg{Text: "", Code: tea.KeyBackspace})
	if m.pathInput != "/old/pat" {
		t.Fatalf("pathInput after backspace = %q, want %q", m.pathInput, "/old/pat")
	}
	m = step(t, m, tea.KeyPressMsg{Text: "h2"})
	if m.pathInput != "/old/path2" {
		t.Fatalf("pathInput after typing = %q, want %q", m.pathInput, "/old/path2")
	}

	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.editing {
		t.Fatalf("still editing after enter")
	}
	if gotPath != "/old/path2" {
		t.Fatalf("Reconnect path = %q, want %q", gotPath, "/old/path2")
	}
}

func TestEditEscCancels(t *testing.T) {
	m := New(Config{State: NoConfig, KubeconfigPath: "/x"})
	m = step(t, m, tea.KeyPressMsg{Text: "k"})
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.editing {
		t.Fatalf("still editing after esc")
	}
}

// TestRetryFailedMsgShowsError pins Reconnect's failure path: setup stays
// on screen (no task swap — that's the root shell's job on success only)
// and surfaces the new error.
func TestRetryFailedMsgShowsError(t *testing.T) {
	m := New(Config{State: NoConfig})
	m.retrying = true
	m = step(t, m, RetryFailedMsg{Err: errors.New("still no kubeconfig")})
	if m.retrying {
		t.Fatalf("retrying should clear on RetryFailedMsg")
	}
	if !strings.Contains(plain(m.Render()), "still no kubeconfig") {
		t.Fatalf("body should show the retry error:\n%s", plain(m.Render()))
	}
}
