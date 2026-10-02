package yamlview

import (
	"encoding/base64"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	sigsyaml "sigs.k8s.io/yaml"

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

// TestRevealedTagSurvivesLongValue pins the 21a fix (docs/design README.md
// §271-274): a decoded value long enough to fill the line must still show
// the "revealed" tag on its own row — the whole line (value + tag) used to
// be truncated as one blob with no budget reserved for the tag, so a long
// real secret (certs, kubeconfigs, tokens) silently lost the marker that
// flags plaintext-on-screen. The assertion scopes to the password row
// specifically (not just "revealed" anywhere in the view) since the strip
// line above always says "N revealed" as part of its own summary count,
// which would mask the tag going missing on the data row itself.
func TestRevealedTagSurvivesLongValue(t *testing.T) {
	long := strings.Repeat("x", 200)
	yaml := strings.Join([]string{
		"apiVersion: v1",
		"kind: Secret",
		"metadata:",
		"  name: app-secret",
		"  namespace: staging",
		"data:",
		"  password: " + b64(long),
		"type: Opaque",
	}, "\n")
	m := newSecretModel(yaml, "app-secret")
	m = step(t, m, m.Init()())
	secretDataCursor(t, &m, "password")

	m = step(t, m, tea.KeyPressMsg{Text: "x"})
	if !m.revealed["password"] {
		t.Fatal("expected 'x' to reveal the cursor's key")
	}
	view := plain(m.Render())
	var passwordLine string
	for line := range strings.SplitSeq(view, "\n") {
		if strings.Contains(line, "password:") {
			passwordLine = line
			break
		}
	}
	if passwordLine == "" {
		t.Fatalf("expected a rendered password: row:\n%s", view)
	}
	if !strings.Contains(passwordLine, "revealed") {
		t.Fatalf("expected the revealed tag on the password row itself:\n%q", passwordLine)
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

// marshalSecret renders sec the way kube.GetYAML does (sigs.k8s.io/yaml), so
// tests see the real key quoting rather than a hand-written approximation.
func marshalSecret(t *testing.T, sec *corev1.Secret) string {
	t.Helper()
	sec.TypeMeta = metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"}
	out, err := sigsyaml.Marshal(sec)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// assertNoSecretMaterial fails if any of leaks appears in the view, in any
// rendered (searchable) or source line, or as a '/' search hit.
func assertNoSecretMaterial(t *testing.T, m Model, leaks ...string) {
	t.Helper()
	view := plain(m.Render())
	for _, leak := range leaks {
		if strings.Contains(view, leak) {
			t.Fatalf("view shows Secret material %q:\n%s", leak, view)
		}
		for _, rl := range m.rendered() {
			if strings.Contains(strings.ToLower(rl.Text), strings.ToLower(leak)) {
				t.Fatalf("rendered line %q holds Secret material %q", rl.Text, leak)
			}
		}
		for _, l := range m.lines {
			if strings.Contains(l, leak) {
				t.Fatalf("source line %q holds Secret material %q", l, leak)
			}
		}
		s := step(t, m, tea.KeyPressMsg{Text: "/"})
		s.cursor = 0
		s = step(t, s, tea.PasteMsg{Content: leak})
		if got := s.rendered()[s.cursor].Text; strings.Contains(got, leak) {
			t.Fatalf("'/' search for %q landed on %q", leak, got)
		}
	}
}

// TestSecretQuotedKeysAreMasked pins 09 H3: sigs.k8s.io/yaml quotes keys
// that would otherwise read as bool/number ("true", "1", "on"), and the old
// line regex didn't match a quoted key, so those values rendered as raw
// base64.
func TestSecretQuotedKeysAreMasked(t *testing.T) {
	values := map[string]string{"true": "val-true-s3cret", "1": "val-one-s3cret", "on": "val-on-s3cret", "plain": "val-plain-s3cret"}
	sec := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "quoted", Namespace: "staging"}, Data: map[string][]byte{}}
	for k, v := range values {
		sec.Data[k] = []byte(v)
	}
	text := marshalSecret(t, sec)
	if !strings.Contains(text, `"true": `) {
		t.Fatalf("fixture premise: expected a quoted key in\n%s", text)
	}
	m := newSecretModel(text, "quoted")
	m = step(t, m, m.Init()())

	var leaks []string
	for _, v := range values {
		leaks = append(leaks, v, b64(v))
	}
	assertNoSecretMaterial(t, m, leaks...)
	if !strings.Contains(plain(m.Render()), "4 keys") {
		t.Fatalf("expected every key counted:\n%s", plain(m.Render()))
	}

	// Revealing a quoted key still shows its real value, on explicit 'x'.
	secretDataCursor(t, &m, "true")
	m = step(t, m, tea.KeyPressMsg{Text: "x"})
	if view := plain(m.Render()); !strings.Contains(view, `"true": val-true-s3cret`) {
		t.Fatalf("expected the revealed quoted key's value:\n%s", view)
	}
}

// TestSecretLastAppliedConfigurationIsRedacted pins 09 H3's second leak: a
// kubectl-applied Secret carries its whole manifest (data, and plaintext
// stringData) in the last-applied-configuration annotation.
func TestSecretLastAppliedConfigurationIsRedacted(t *testing.T) {
	const plaintext = "stringdata-hunter2"
	dataB64 := b64("data-s3cret-value")
	lastApplied := `{"apiVersion":"v1","data":{"token":"` + dataB64 + `"},"kind":"Secret","metadata":{"name":"applied","namespace":"staging"},"stringData":{"password":"` + plaintext + `"},"type":"Opaque"}`
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "applied", Namespace: "staging",
			Annotations: map[string]string{corev1.LastAppliedConfigAnnotation: lastApplied, "team": "platform"},
		},
		Data: map[string][]byte{"token": []byte("data-s3cret-value"), "password": []byte(plaintext)},
		Type: corev1.SecretTypeOpaque,
	}
	m := newSecretModel(marshalSecret(t, sec), "applied")
	m = step(t, m, m.Init()())

	assertNoSecretMaterial(t, m, plaintext, b64(plaintext), dataB64, "data-s3cret-value")
	view := plain(m.Render())
	if !strings.Contains(view, "contains secret data") || !strings.Contains(view, "team: platform") {
		t.Fatalf("expected the annotation redacted and the others kept:\n%s", view)
	}
	if strings.Contains(m.copyText, plaintext) || strings.Contains(m.copyText, "last-applied-configuration") {
		t.Fatalf("Y copy carries the last-applied manifest:\n%s", m.copyText)
	}
	if !strings.Contains(m.copyText, dataB64) {
		t.Fatalf("Y copy should keep data base64 (§21a):\n%s", m.copyText)
	}
}

// TestSecretStringDataIsMasked covers a manifest that still carries
// stringData (a saved Helm release manifest goes through this screen too):
// plaintext values are masked, reveal on 'x', and Y copies them base64.
func TestSecretStringDataIsMasked(t *testing.T) {
	const plaintext = "manifest-plaintext-pw"
	text := strings.Join([]string{
		"apiVersion: v1",
		"kind: Secret",
		"metadata:",
		"  name: chart-secret",
		"stringData:",
		"  password: " + plaintext,
		`  "yes": also-` + plaintext,
		"type: Opaque",
	}, "\n")
	m := newSecretModel(text, "chart-secret")
	m = step(t, m, m.Init()())

	assertNoSecretMaterial(t, m, plaintext)
	if !strings.Contains(plain(m.Render()), "stringData · 21 B") {
		t.Fatalf("expected the stringData placeholder:\n%s", plain(m.Render()))
	}
	if strings.Contains(m.copyText, plaintext) || !strings.Contains(m.copyText, b64(plaintext)) {
		t.Fatalf("Y copy should carry stringData as base64 data:\n%s", m.copyText)
	}

	secretDataCursor(t, &m, "stringData/password")
	m = step(t, m, tea.KeyPressMsg{Text: "x"})
	if !strings.Contains(plain(m.Render()), "password: "+plaintext) {
		t.Fatalf("expected the revealed stringData value:\n%s", plain(m.Render()))
	}
}

// TestUnparseableSecretFailsClosed: if the YAML can't be parsed for
// masking, nothing of it is shown or copied.
func TestUnparseableSecretFailsClosed(t *testing.T) {
	m := newSecretModel("data:\n  k: c2VjcmV0\n  - broken: [", "broken")
	m = step(t, m, m.Init()())
	assertNoSecretMaterial(t, m, "c2VjcmV0")
	if _, cmd := m.Update(tea.KeyPressMsg{Text: "Y"}); cmd != nil {
		t.Fatal("Y on an unparseable Secret must copy nothing")
	}
}
