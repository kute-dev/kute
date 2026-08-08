package certchain

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kute-dev/kute/internal/kube/fake"
	"github.com/kute-dev/kute/internal/testutil/goldentest"
	"github.com/kute-dev/kute/internal/tui"
)

// goldenFixedNow pins the one clock this screen renders from — every age
// column and the failure card's "N ago" line — the same reasoning
// fluxdetail's own golden test gives: the demo fixtures build their
// timestamps relative to the real time.Now(), which is right for the app and
// useless for a fixture.
var goldenFixedNow = time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

// goldenCertChainModel builds §35a against the demo cluster's own
// cert-manager fixtures — the same seeded objects `--demo` shows.
// "web-tls" is the failing chain (failure card + all four rows); "api-tls"
// the healthy one (zero chrome).
//
// The demo fixtures build their creation timestamps relative to the real
// time.Now() (fake.NewDemo's own age() closure), which is right for the app
// and useless for a fixture pinned to goldenFixedNow — so every age-bearing
// field the view reads (each chain node's Created, the failure's Since) is
// overwritten to a fixed offset after load, the same fix fluxdetail's own
// golden test applies to its failure card's "N ago"/retry countdown.
func goldenCertChainModel(t *testing.T, namespace, name string, width, height int) *Model {
	t.Helper()
	c := fake.NewDemo()
	sess := &tui.Session{
		Theme: tui.Dark(),
		Location: tui.Location{
			Context: "microk8s-cluster", Namespace: namespace,
		},
	}
	m := New(Config{Session: sess, Lister: c, Namespace: namespace, Name: name})
	m.SetSize(width, height)
	updated, _ := m.Update(m.load()())
	got := updated.(*Model)
	got.now = goldenFixedNow
	for i := range got.chain {
		age := 8 * time.Minute
		if got.chain[i].Depth == 0 {
			age = 41 * 24 * time.Hour // the Certificate itself — §35a's own mockup age
		}
		got.chain[i].Created = goldenFixedNow.Add(-age)
	}
	if got.fail != nil {
		got.fail.Since = goldenFixedNow.Add(-8 * time.Minute)
	}
	return got
}

func goldenDir() string {
	return filepath.Join("..", "..", "..", "..", "test", "golden", "certchain")
}

func goldenFixtures(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{
		"failed-120x36.golden":  goldentest.Plain(goldenCertChainModel(t, "default", "web-tls", 120, 36).Render()),
		"failed-80x24.golden":   goldentest.Plain(goldenCertChainModel(t, "default", "web-tls", 80, 24).Render()),
		"healthy-120x36.golden": goldentest.Plain(goldenCertChainModel(t, "default", "api-tls", 120, 36).Render()),
	}
}

func TestGenerateGoldenFixtures(t *testing.T) {
	if os.Getenv("UPDATE_GOLDEN") != "1" {
		t.Skip("set UPDATE_GOLDEN=1 to regenerate certchain golden fixtures")
	}
	if err := os.MkdirAll(goldenDir(), 0o755); err != nil {
		t.Fatalf("mkdir golden dir: %v", err)
	}
	for name, got := range goldenFixtures(t) {
		if err := os.WriteFile(filepath.Join(goldenDir(), name), []byte(got), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func TestGoldenFixtures(t *testing.T) {
	for name, got := range goldenFixtures(t) {
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

// truecolorGoldenFixtures pins the failure banner's colour mapping (the
// filled ErrBannerBg block + left accent bar, poddetail's own recipe) in
// both themes — the plain fixtures above render colourless under test.
//
// The profile swap is global, so this must not run parallel with other
// renders in this package (none of them do).
func truecolorGoldenFixtures(t *testing.T) map[string]string {
	t.Helper()
	dark := goldenCertChainModel(t, "default", "web-tls", 120, 36)
	light := goldenCertChainModel(t, "default", "web-tls", 120, 36)
	light.session.Theme = tui.Light()
	return map[string]string{
		"failed-120x36-dark.golden":  goldentest.Truecolor(dark.Render()),
		"failed-120x36-light.golden": goldentest.Truecolor(light.Render()),
	}
}

func TestGenerateTruecolorGoldenFixtures(t *testing.T) {
	if os.Getenv("UPDATE_GOLDEN") != "1" {
		t.Skip("set UPDATE_GOLDEN=1 to regenerate certchain golden fixtures")
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
