package execpicker

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/testutil/goldentest"
	"github.com/kute-dev/kute/internal/tui"
)

// goldenExecPickerModel builds a deterministic 10a screen: a two-container
// pod (a regular "gateway" container plus an IsSidecar "istio-proxy"), both
// Running, with the sidecar (row 1) selected so the "will run" line exercises
// a non-default selection (docs/design README.md §10a: "kubectl exec -it
// <pod> -c <container> -- bash").
func goldenExecPickerModel(width, height int) Model {
	sess := &tui.Session{Theme: tui.Dark()}
	sess.Location.Context = "nva-stage-cluster"
	m := New(Config{
		Session:   sess,
		Namespace: "nva-stage",
		PodName:   "nva-gateway-2b81x",
		Containers: []kube.ContainerInfo{
			{Name: "gateway", Image: "nva-gateway:1.19.0", State: "Running"},
			{Name: "istio-proxy", Image: "sidecar:v1.2", State: "Running", IsSidecar: true},
		},
	})
	m.SetSize(width, height)
	m.selected = 1
	return m
}

func goldenExecPickerFixtures() map[string]string {
	return map[string]string{
		"120x36.golden": goldentest.Plain(goldenExecPickerModel(120, 36).Render()),
		"80x24.golden":  goldentest.Plain(goldenExecPickerModel(80, 24).Render()),
	}
}

func goldenDir() string {
	return filepath.Join("..", "..", "..", "..", "test", "golden", "execpicker")
}

func TestGenerateGoldenFixtures(t *testing.T) {
	if os.Getenv("UPDATE_GOLDEN") != "1" {
		t.Skip("set UPDATE_GOLDEN=1 to regenerate execpicker golden fixtures")
	}
	if err := os.MkdirAll(goldenDir(), 0o755); err != nil {
		t.Fatalf("mkdir golden dir: %v", err)
	}
	for name, got := range goldenExecPickerFixtures() {
		if err := os.WriteFile(filepath.Join(goldenDir(), name), []byte(got), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func TestGoldenFixtures(t *testing.T) {
	for name, got := range goldenExecPickerFixtures() {
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

// truecolorGoldenFixtures renders 10a with a forced truecolor profile in
// both themes, pinning the Theme-token-to-cell color mapping (selected-row
// background, "will run" line color, sidecar label) that the plain goldens
// above can't see — same pattern as poddetail's 5a and setup's 4c. The
// profile swap is global, so this package must not run these in parallel
// with other renders (none of them do).
func truecolorGoldenFixtures(t *testing.T) map[string]string {
	t.Helper()
	dark := goldenExecPickerModel(120, 36)
	light := goldenExecPickerModel(120, 36)
	light.session.Theme = tui.Light()
	return map[string]string{
		"120x36-dark.golden":  goldentest.Truecolor(dark.Render()),
		"120x36-light.golden": goldentest.Truecolor(light.Render()),
	}
}

func TestGenerateTruecolorGoldenFixtures(t *testing.T) {
	if os.Getenv("UPDATE_GOLDEN") != "1" {
		t.Skip("set UPDATE_GOLDEN=1 to regenerate execpicker golden fixtures")
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
