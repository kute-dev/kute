package tui_test

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/config"
	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/state"
	"github.com/kute-dev/kute/internal/tui"
)

// palette_golden_test.go pins the one shared palette shell's rendered
// states across its three scopes (docs/design README.md): 12a (goto, empty
// query, alias letters + ranked kinds), 12b (goto, alias letter typed —
// re-ranks, doesn't fire), 6a (namespace palette with live PODS/HEALTH/CPU
// columns), and 7a (context palette with the PROD tag + probing state).
// Reuses goto_test.go/namespace_test.go/context_test.go's own session
// builders (gotoTestSession, gotoFakeLister, fakeMetricsReader,
// writeContextTestKubeconfig) rather than redeclaring them, and drives the
// same tui.NewWithSession + real key press path those files already prove
// out — so each golden model is the actual composed Model.View(), dimmed
// backdrop and all, not a hand-assembled palette.Model.

func goldenGotoEmptyModel(t *testing.T, width, height int) tui.Model {
	t.Helper()
	lister := gotoFakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod:        {gotoTestPod("default", "api-1"), gotoTestPod("default", "api-2")},
		kube.KindDeployment: {},
	}}
	sess := gotoTestSession(lister)
	task := &screenTask{name: "browse", pill: "PODS"}
	model := tui.NewWithSession(task, sess)
	updated, _ := model.Update(tea.WindowSizeMsg{Width: width, Height: height})
	updated, _ = updated.(tui.Model).Update(tea.KeyPressMsg{Text: "g"})
	return updated.(tui.Model)
}

func goldenGotoTypedModel(t *testing.T, width, height int) tui.Model {
	t.Helper()
	lister := gotoFakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindDeployment: {},
	}}
	sess := gotoTestSession(lister)
	task := &screenTask{name: "browse", pill: "PODS"}
	model := tui.NewWithSession(task, sess)
	updated, _ := model.Update(tea.WindowSizeMsg{Width: width, Height: height})
	updated, _ = updated.(tui.Model).Update(tea.KeyPressMsg{Text: "g"})
	updated, _ = updated.(tui.Model).Update(tea.KeyPressMsg{Text: "d"})
	return updated.(tui.Model)
}

func goldenNamespaceModel(t *testing.T, width, height int) tui.Model {
	t.Helper()
	lister := gotoFakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindNamespace: {namespaceObj("default"), namespaceObj("nva-stage"), namespaceObj("nva-prod")},
		kube.KindPod: {
			gotoTestPod("default", "api-1"),
			gotoTestPod("nva-stage", "worker-0"),
			gotoTestPod("nva-stage", "worker-1"),
			gotoTestPod("nva-prod", "api-2"),
		},
	}}
	sess := gotoTestSession(lister)
	sess.Metrics = fakeMetricsReader{byNamespace: map[string]map[string]kube.PodMetrics{
		"default":   {"api-1": {CPUMilli: 100}},
		"nva-stage": {"worker-0": {CPUMilli: 300}, "worker-1": {CPUMilli: 200}},
		"nva-prod":  {"api-2": {CPUMilli: 400}},
	}}
	sess.State = state.State{PerContext: map[string]state.PerContext{
		"microk8s-cluster": {RecentNamespaces: []string{"default", "nva-prod"}},
	}}

	task := &screenTask{name: "browse", pill: "PODS"}
	model := tui.NewWithSession(task, sess)
	updated, _ := model.Update(tea.WindowSizeMsg{Width: width, Height: height})
	updated, cmd := updated.(tui.Model).Update(tea.KeyPressMsg{Text: "n"})
	if cmd != nil {
		updated, _ = updated.(tui.Model).Update(cmd())
	}
	return updated.(tui.Model)
}

// goldenContextKubeconfigPath writes the fixture kubeconfig to a fixed path
// rather than context_test.go's own t.TempDir()-based helper — t.TempDir()
// embeds the running test's name, and the 7a palette's right hint renders
// that path verbatim (kubeconfigPath() in context.go), so a name-derived
// path would make the golden differ between TestGenerateGoldenPaletteFixtures
// and TestGoldenPaletteFixtures even though nothing else about the render
// changed.
func goldenContextKubeconfigPath(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(os.TempDir(), "kute-golden-context-kubeconfig")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	path := filepath.Join(dir, "config")
	if err := os.WriteFile(path, []byte(threeContextTestKubeconfig), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func goldenContextModel(t *testing.T, width, height int) tui.Model {
	t.Helper()
	t.Setenv("KUBECONFIG", goldenContextKubeconfigPath(t))

	sess := &tui.Session{
		Theme:    tui.Dark(),
		Styles:   tui.NewStyles(tui.Dark()),
		Location: tui.Location{Context: "dev"},
		Config:   config.Config{ProdContexts: []string{"prod-eks"}},
		State: state.State{
			PerContext:     map[string]state.PerContext{},
			RecentContexts: []string{"dev", "prod-eks", "stage-eks"},
		},
	}
	task := &screenTask{name: "browse", pill: "PODS"}
	model := tui.NewWithSession(task, sess)
	updated, _ := model.Update(tea.WindowSizeMsg{Width: width, Height: height})
	updated, _ = updated.(tui.Model).Update(tea.KeyPressMsg{Text: "c"})
	return updated.(tui.Model)
}

func goldenPaletteDir() string {
	return filepath.Join("..", "..", "test", "golden", "palette")
}

func goldenPaletteFixtures(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{
		"goto-empty-120x36.golden": goldenGotoEmptyModel(t, 120, 36).View().Content,
		"goto-empty-80x24.golden":  goldenGotoEmptyModel(t, 80, 24).View().Content,
		"goto-typed-120x36.golden": goldenGotoTypedModel(t, 120, 36).View().Content,
		"goto-typed-80x24.golden":  goldenGotoTypedModel(t, 80, 24).View().Content,
		"namespace-120x36.golden":  goldenNamespaceModel(t, 120, 36).View().Content,
		"namespace-80x24.golden":   goldenNamespaceModel(t, 80, 24).View().Content,
		"context-120x36.golden":    goldenContextModel(t, 120, 36).View().Content,
		"context-80x24.golden":     goldenContextModel(t, 80, 24).View().Content,
	}
}

func TestGenerateGoldenPaletteFixtures(t *testing.T) {
	if os.Getenv("UPDATE_GOLDEN") != "1" {
		t.Skip("set UPDATE_GOLDEN=1 to regenerate palette golden fixtures")
	}
	if err := os.MkdirAll(goldenPaletteDir(), 0o755); err != nil {
		t.Fatalf("mkdir golden dir: %v", err)
	}
	for name, got := range goldenPaletteFixtures(t) {
		if err := os.WriteFile(filepath.Join(goldenPaletteDir(), name), []byte(got), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func TestGoldenPaletteFixtures(t *testing.T) {
	for name, got := range goldenPaletteFixtures(t) {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(goldenPaletteDir(), name)
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

// truecolorGoldenPaletteFixtures pins the palette panel's per-cell color
// mapping (accent title, alias-letter highlight, recent-row tags, PROD
// border) in both themes — goto-empty only (the state exercising the most
// color tokens at once: alias letters, live counts, recent row), matching
// setup/golden_test.go's precedent of truecolor'ing one representative
// state rather than every one.
func truecolorGoldenPaletteFixtures(t *testing.T) map[string]string {
	t.Helper()
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(old)

	dark := goldenGotoEmptyModel(t, 120, 36)

	lister := gotoFakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod:        {gotoTestPod("default", "api-1"), gotoTestPod("default", "api-2")},
		kube.KindDeployment: {},
	}}
	sess := gotoTestSession(lister)
	sess.Theme = tui.Light()
	sess.Styles = tui.NewStyles(tui.Light())
	task := &screenTask{name: "browse", pill: "PODS"}
	model := tui.NewWithSession(task, sess)
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	updated, _ = updated.(tui.Model).Update(tea.KeyPressMsg{Text: "g"})
	light := updated.(tui.Model)

	return map[string]string{
		"goto-empty-120x36-dark.golden":  dark.View().Content,
		"goto-empty-120x36-light.golden": light.View().Content,
	}
}

func TestGenerateTruecolorGoldenPaletteFixtures(t *testing.T) {
	if os.Getenv("UPDATE_GOLDEN") != "1" {
		t.Skip("set UPDATE_GOLDEN=1 to regenerate palette golden fixtures")
	}
	for name, got := range truecolorGoldenPaletteFixtures(t) {
		if err := os.WriteFile(filepath.Join(goldenPaletteDir(), name), []byte(got), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func TestTruecolorGoldenPaletteFixtures(t *testing.T) {
	for name, got := range truecolorGoldenPaletteFixtures(t) {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(goldenPaletteDir(), name)
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
