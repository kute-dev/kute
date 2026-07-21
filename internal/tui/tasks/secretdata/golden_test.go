package secretdata

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
)

// goldenSecretDataModel builds a deterministic 27b Data view: docs/design/
// v.0.2.0.dc.html's own §27b mockup scenario — secret/aim-secrets in
// aim-stage with three existing masked keys.
func goldenSecretDataModel(t *testing.T, width, height int) Model {
	t.Helper()
	secret := secretObj("aim-stage", "aim-secrets", map[string][]byte{
		"DATABASE_URL": []byte("postgres://user:pass@db.aim-stage.svc:5432/aim"),
		"API_TOKEN":    []byte("0123456789abcdef0123456789abcdef012345"),
		"SMTP_USER":    []byte("no-reply@aim.dev"),
	})
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{kube.KindSecret: {secret}}}
	sess := newSession()
	sess.Location.Namespace = "aim-stage"
	m := New(Config{
		Session: sess, Lister: lister, Mutator: &fakeMutator{secret: secret},
		Namespace: "aim-stage", Name: "aim-secrets",
	})
	m.SetSize(width, height)
	m = step(t, m, m.Init()())
	// The header's connected badge comes from the conn-state ping loop — a
	// fixed latency keeps the golden deterministic (mirrors helmhistory's
	// own goldenHelmHistoryModel).
	m = step(t, m, kube.ConnStateMsg{Phase: kube.ConnConnected, Latency: 12 * time.Millisecond})
	return m
}

// goldenSecretDataAddModel drives the same scenario into 27b's own mid-add
// screenshot: 'a' + a typed key/value matching the mockup's literal example
// (SMTP_PASSWORD / hunter2-staging▎ · visible while typing · x re-mask).
func goldenSecretDataAddModel(t *testing.T, width, height int) Model {
	t.Helper()
	m := goldenSecretDataModel(t, width, height)
	m = step(t, m, tea.KeyPressMsg{Text: "a"})
	for _, r := range "SMTP_PASSWORD" {
		m = step(t, m, tea.KeyPressMsg{Text: string(r)})
	}
	m = step(t, m, tea.KeyPressMsg{Text: "tab"})
	for _, r := range "hunter2-staging" {
		m = step(t, m, tea.KeyPressMsg{Text: string(r)})
	}
	return m
}

func goldenSecretDataFixtures(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{
		"120x36.golden":     goldenSecretDataModel(t, 120, 36).Render(),
		"80x24.golden":      goldenSecretDataModel(t, 80, 24).Render(),
		"add-120x36.golden": goldenSecretDataAddModel(t, 120, 36).Render(),
		"add-80x24.golden":  goldenSecretDataAddModel(t, 80, 24).Render(),
	}
}

func goldenDir() string {
	return filepath.Join("..", "..", "..", "..", "test", "golden", "secretdata")
}

func TestGenerateGoldenFixtures(t *testing.T) {
	if os.Getenv("UPDATE_GOLDEN") != "1" {
		t.Skip("set UPDATE_GOLDEN=1 to regenerate secretdata golden fixtures")
	}
	if err := os.MkdirAll(goldenDir(), 0o755); err != nil {
		t.Fatalf("mkdir golden dir: %v", err)
	}
	for name, got := range goldenSecretDataFixtures(t) {
		if err := os.WriteFile(filepath.Join(goldenDir(), name), []byte(got), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func TestGoldenFixtures(t *testing.T) {
	for name, got := range goldenSecretDataFixtures(t) {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(goldenDir(), name)
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden %s: %v", path, err)
			}
			if got != string(want) {
				t.Fatalf("golden mismatch for %s\nwant:\n%s\ngot:\n%s", name, string(want), got)
			}
		})
	}
}

// truecolorGoldenFixtures renders 27b's Data view with a forced truecolor
// profile in both themes, pinning the per-cell color mapping (mask glyph,
// accent marker, will-run strip fill) the profile-less goldens above can't
// see. The profile swap is global, so these tests must not run parallel
// with other renders in this package (none of them do).
func truecolorGoldenFixtures(t *testing.T) map[string]string {
	t.Helper()
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(old)

	dark := goldenSecretDataModel(t, 120, 36)
	light := goldenSecretDataModel(t, 120, 36)
	light.session.Theme = tui.Light()
	return map[string]string{
		"120x36-dark.golden":  dark.Render(),
		"120x36-light.golden": light.Render(),
	}
}

func TestGenerateTruecolorGoldenFixtures(t *testing.T) {
	if os.Getenv("UPDATE_GOLDEN") != "1" {
		t.Skip("set UPDATE_GOLDEN=1 to regenerate secretdata golden fixtures")
	}
	if err := os.MkdirAll(goldenDir(), 0o755); err != nil {
		t.Fatalf("mkdir golden dir: %v", err)
	}
	for name, got := range truecolorGoldenFixtures(t) {
		if err := os.WriteFile(filepath.Join(goldenDir(), name), []byte(got), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func TestTruecolorGoldenFixtures(t *testing.T) {
	for name, got := range truecolorGoldenFixtures(t) {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(goldenDir(), name)
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden %s: %v", path, err)
			}
			if got != string(want) {
				t.Fatalf("golden mismatch for %s\nwant:\n%s\ngot:\n%s", name, string(want), got)
			}
		})
	}
}
