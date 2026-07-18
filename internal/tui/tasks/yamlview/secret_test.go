package yamlview

import (
	"encoding/base64"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
)

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

var secretYAML = strings.Join([]string{
	"apiVersion: v1",
	"kind: Secret",
	"metadata:",
	"  name: app-secret",
	"  namespace: staging",
	"data:",
	"  password: " + b64("password123"),
	"  username: " + b64("admin"),
	"type: Opaque",
}, "\n")

var tlsSecretYAML = strings.Join([]string{
	"apiVersion: v1",
	"kind: Secret",
	"metadata:",
	"  name: web-tls",
	"  namespace: production",
	"data:",
	"  tls.crt: " + b64("-----BEGIN CERTIFICATE-----\nMIIB...\n-----END CERTIFICATE-----"),
	"type: kubernetes.io/tls",
}, "\n")

func testSecret(name, ns string) *corev1.Secret {
	return &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}}
}

func newSecretModel(text, name string) Model {
	lister := &fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindSecret: {testSecret(name, "staging")},
	}}
	m := New(Config{
		Session: newSession(), Lister: lister,
		YAML:      fakeYAML{text: text, resourceVersion: "9"},
		Kind:      kube.KindSecret,
		Namespace: "staging", Name: name,
	})
	m.SetSize(120, 40)
	return m
}

// secretDataCursor moves the cursor to the rendered line for the given
// data: key, failing the test if no such line exists.
func secretDataCursor(t *testing.T, m *Model, key string) {
	t.Helper()
	for i, rl := range m.rendered() {
		if rl.SecretKey == key {
			m.cursor = i
			return
		}
	}
	t.Fatalf("no rendered line found for secret key %q", key)
}

func TestSecretDataIsMaskedByDefault(t *testing.T) {
	m := newSecretModel(secretYAML, "app-secret")
	m = step(t, m, m.Init()())

	if !m.isSecret {
		t.Fatal("expected isSecret true for a Secret object")
	}
	view := plain(m.Render())
	if !strings.Contains(view, "••••••••") || !strings.Contains(view, "base64") {
		t.Fatalf("expected masked placeholder in view:\n%s", view)
	}
	if strings.Contains(view, b64("password123")) || strings.Contains(view, b64("admin")) {
		t.Fatalf("expected raw base64 never rendered:\n%s", view)
	}
	if strings.Contains(view, "password123") || strings.Contains(view, "admin") {
		t.Fatalf("expected decoded plaintext never rendered before reveal:\n%s", view)
	}
}

func TestXTogglesRevealAtCursorInPlace(t *testing.T) {
	m := newSecretModel(secretYAML, "app-secret")
	m = step(t, m, m.Init()())
	secretDataCursor(t, &m, "password")

	m = step(t, m, tea.KeyPressMsg{Text: "x"})
	if !m.revealed["password"] {
		t.Fatal("expected 'x' to reveal the cursor's key")
	}
	view := plain(m.Render())
	if !strings.Contains(view, "password123") {
		t.Fatalf("expected decoded plaintext visible after reveal:\n%s", view)
	}
	if !strings.Contains(view, "revealed") {
		t.Fatalf("expected a revealed tag:\n%s", view)
	}
	// username stays masked — reveal is per-key, not global.
	if strings.Contains(view, "admin") {
		t.Fatalf("expected the other key to remain masked:\n%s", view)
	}

	m = step(t, m, tea.KeyPressMsg{Text: "x"})
	if m.revealed["password"] {
		t.Fatal("expected a second 'x' to re-mask the key")
	}
	if strings.Contains(plain(m.Render()), "password123") {
		t.Fatal("expected plaintext hidden again after re-masking")
	}
}

func TestCapitalXRequiresConfirmBeforeRevealingAll(t *testing.T) {
	m := newSecretModel(secretYAML, "app-secret")
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Text: "X"})
	if !m.revealAllConfirm {
		t.Fatal("expected 'X' to arm the reveal-all confirm gate")
	}
	if !m.CapturingInput() {
		t.Fatal("expected the confirm gate to capture input")
	}
	kb := m.Keybar()
	if kb.PillText != "CONFIRM" {
		t.Fatalf("PillText = %q, want CONFIRM", kb.PillText)
	}

	// 'n' cancels without revealing anything.
	m = step(t, m, tea.KeyPressMsg{Text: "n"})
	if m.revealAllConfirm {
		t.Fatal("expected 'n' to clear the confirm gate")
	}
	if m.revealed["password"] || m.revealed["username"] {
		t.Fatal("expected 'n' to cancel without revealing")
	}

	m = step(t, m, tea.KeyPressMsg{Text: "X"})
	m = step(t, m, tea.KeyPressMsg{Text: "y"})
	if m.revealAllConfirm {
		t.Fatal("expected 'y' to clear the confirm gate")
	}
	if !m.revealed["password"] || !m.revealed["username"] {
		t.Fatal("expected 'y' to reveal every key")
	}
	view := plain(m.Render())
	if !strings.Contains(view, "password123") || !strings.Contains(view, "admin") {
		t.Fatalf("expected both keys' plaintext visible:\n%s", view)
	}
}

func TestYCopiesDecodedValueOfCursorKey(t *testing.T) {
	m := newSecretModel(secretYAML, "app-secret")
	m = step(t, m, m.Init()())
	secretDataCursor(t, &m, "password")

	_, cmd := m.Update(tea.KeyPressMsg{Text: "y"})
	if cmd == nil {
		t.Fatal("expected 'y' on a secret data line to return a clipboard command")
	}
	if cmd() == nil {
		t.Fatal("expected a non-nil clipboard message")
	}
}

func TestYIsNoopOffASecretDataLine(t *testing.T) {
	m := newSecretModel(secretYAML, "app-secret")
	m = step(t, m, m.Init()())
	m.cursor = 0 // apiVersion: v1 — not a data: entry

	_, cmd := m.Update(tea.KeyPressMsg{Text: "y"})
	if cmd != nil {
		t.Fatal("expected 'y' off a secret data line to be a no-op")
	}
}

func TestSecretStripLineTracksTypeKeysAndRevealedCount(t *testing.T) {
	m := newSecretModel(secretYAML, "app-secret")
	m = step(t, m, m.Init()())

	view := plain(m.Render())
	if !strings.Contains(view, "Secret · Opaque · 2 keys · 0 revealed") {
		t.Fatalf("expected the secret strip summary:\n%s", view)
	}
	if !strings.Contains(view, "decoded in memory only") {
		t.Fatalf("expected the safety note in the strip:\n%s", view)
	}

	secretDataCursor(t, &m, "password")
	m = step(t, m, tea.KeyPressMsg{Text: "x"})
	if !strings.Contains(plain(m.Render()), "1 revealed") {
		t.Fatalf("expected revealed count to track reveals:\n%s", plain(m.Render()))
	}
}

func TestSecretKeybarPillIsSecret(t *testing.T) {
	m := newSecretModel(secretYAML, "app-secret")
	m = step(t, m, m.Init()())
	if got := m.Keybar().PillText; got != "SECRET" {
		t.Fatalf("PillText = %q, want SECRET", got)
	}
}

func TestMultilineRevealedValueExpandsIndentedBlock(t *testing.T) {
	m := newSecretModel(tlsSecretYAML, "web-tls")
	m = step(t, m, m.Init()())
	secretDataCursor(t, &m, "tls.crt")

	m = step(t, m, tea.KeyPressMsg{Text: "x"})
	view := plain(m.Render())
	for _, want := range []string{"-----BEGIN CERTIFICATE-----", "MIIB...", "-----END CERTIFICATE-----"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected multi-line decoded content line %q:\n%s", want, view)
		}
	}
}

func TestNonSecretKindHasNoSecretSemantics(t *testing.T) {
	m, _ := newModel(fixtureYAML)
	m = step(t, m, m.Init()())

	if m.isSecret {
		t.Fatal("expected isSecret false for a Pod")
	}
	if got := m.Keybar().PillText; got != "YAML" {
		t.Fatalf("PillText = %q, want YAML for a non-Secret kind", got)
	}
	m = step(t, m, tea.KeyPressMsg{Text: "X"})
	if m.revealAllConfirm {
		t.Fatal("expected 'X' to be a no-op outside Secret semantics")
	}
}
