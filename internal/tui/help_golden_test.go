package tui_test

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/testutil/goldentest"
	"github.com/kute-dev/kute/internal/tui"
)

// helpBackdropScreen is a richer fake tui.Screen than model_test.go's own
// screenTask — a few lines of table-shaped Body content, so the 7b golden
// pins the dim-backdrop composition against something that actually looks
// like a resting screen rather than a single word.
type helpBackdropScreen struct{}

func (helpBackdropScreen) Init() tea.Cmd    { return nil }
func (helpBackdropScreen) SetSize(int, int) {}
func (helpBackdropScreen) View() tea.View {
	return tea.NewView(helpBackdropScreen{}.Body(120, 30))
}
func (helpBackdropScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) { return helpBackdropScreen{}, nil }
func (helpBackdropScreen) Theme() tui.Theme                        { return tui.Dark() }
func (helpBackdropScreen) Header() tui.HeaderState {
	return tui.HeaderState{Crumbs: []tui.Crumb{
		{Text: "microk8s-cluster"}, {Text: "nva-stage"}, {Text: "Pods"},
	}}
}
func (helpBackdropScreen) Strips(int) []string { return []string{"● 32 running · ◐ 2 pending"} }
func (helpBackdropScreen) Keybar() tui.Keybar {
	return tui.Keybar{
		PillText: "PODS",
		Groups: [][]tui.KeyHint{
			{{Key: "↵", Label: "open"}, {Key: "l", Label: "logs"}, {Key: "e", Label: "exec"}},
			{{Key: "ctrl-d", Label: "delete"}, {Key: "y", Label: "yaml"}},
		},
	}
}
func (helpBackdropScreen) Body(int, int) string {
	return "api-7d9f6c8-abcde   Running\nworker-0            CrashLoopBackOff\ncache-0             Pending"
}

func goldenHelpSession(theme tui.Theme) *tui.Session {
	sess := &tui.Session{Theme: theme, Styles: tui.NewStyles(theme)}
	sess.HelpScope = []tui.KeyHint{
		{Key: "g", Label: "goto"}, {Key: "n", Label: "namespace"}, {Key: "c", Label: "context"},
		{Key: "a", Label: "all namespaces"}, {Key: "↵", Label: "toggles last"},
	}
	sess.HelpGlobal = []tui.KeyHint{
		{Key: "↑↓ jk", Label: "move"}, {Key: "esc", Label: "back"},
		{Key: "U", Label: "what's new"}, {Key: "?", Label: "help"}, {Key: "ctrl+q", Label: "quit"},
	}
	return sess
}

func goldenHelpModelWithTheme(theme tui.Theme, width, height int) tui.Model {
	model := tui.NewWithSession(helpBackdropScreen{}, goldenHelpSession(theme))
	updated, _ := model.Update(tea.WindowSizeMsg{Width: width, Height: height})
	updated, _ = updated.(tui.Model).Update(tea.KeyPressMsg{Text: "?"})
	return updated.(tui.Model)
}

func goldenHelpModel(width, height int) tui.Model {
	return goldenHelpModelWithTheme(tui.Dark(), width, height)
}

func goldenHelpDir() string {
	return filepath.Join("..", "..", "test", "golden", "help")
}

func goldenHelpFixtures() map[string]string {
	return map[string]string{
		"120x36.golden": goldentest.Plain(goldenHelpModel(120, 36).View().Content),
		"80x24.golden":  goldentest.Plain(goldenHelpModel(80, 24).View().Content),
	}
}

func TestGenerateGoldenHelpFixtures(t *testing.T) {
	if os.Getenv("UPDATE_GOLDEN") != "1" {
		t.Skip("set UPDATE_GOLDEN=1 to regenerate help golden fixtures")
	}
	if err := os.MkdirAll(goldenHelpDir(), 0o755); err != nil {
		t.Fatalf("mkdir golden dir: %v", err)
	}
	for name, got := range goldenHelpFixtures() {
		if err := os.WriteFile(filepath.Join(goldenHelpDir(), name), []byte(got), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func TestGoldenHelpFixtures(t *testing.T) {
	for name, got := range goldenHelpFixtures() {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(goldenHelpDir(), name)
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

// truecolorGoldenHelpFixtures renders 7b with a forced truecolor profile in
// both themes, pinning the panel's per-cell color mapping (accent title,
// AccentHi column headings, BorderPalette frame) the plain goldens above
// can't see — same pattern as browse/poddetail/setup. The profile swap is
// global, so this package's other renders must not run in parallel with it
// (none in this file do; other _test.go files in package tui_test don't
// render either).
func truecolorGoldenHelpFixtures() map[string]string {
	dark := goldenHelpModel(120, 36)
	light := goldenHelpModelWithTheme(tui.Light(), 120, 36)

	return map[string]string{
		"120x36-dark.golden":  goldentest.Truecolor(dark.View().Content),
		"120x36-light.golden": goldentest.Truecolor(light.View().Content),
	}
}

func TestGenerateTruecolorGoldenHelpFixtures(t *testing.T) {
	if os.Getenv("UPDATE_GOLDEN") != "1" {
		t.Skip("set UPDATE_GOLDEN=1 to regenerate help golden fixtures")
	}
	for name, got := range truecolorGoldenHelpFixtures() {
		if err := os.WriteFile(filepath.Join(goldenHelpDir(), name), []byte(got), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func TestTruecolorGoldenHelpFixtures(t *testing.T) {
	for name, got := range truecolorGoldenHelpFixtures() {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(goldenHelpDir(), name)
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
