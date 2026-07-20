package update

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/update"
)

// goldenAvailableModel is 28b's main state: current 0.2.0, latest 0.2.1
// available, 4 changelog entries — fewer than any of the fixture sizes'
// vertical budget, so every entry renders and no "… N more" trailer
// appears (see goldenManyEntriesModel for the truncating case).
func goldenAvailableModel(theme tui.Theme, width, height int) Model {
	sess := &tui.Session{Theme: theme, Styles: tui.NewStyles(theme), Version: "0.2.0"}
	sess.Update = &tui.UpdateInfo{
		Latest: update.Release{
			Version:     "0.2.1",
			PublishedAt: time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC),
			HTMLURL:     "https://github.com/kute-dev/kute/releases/tag/v0.2.1",
		},
		Changelog: []update.ChangelogEntry{
			{Type: "fix", Text: "rollout watch could miss the final ready event on slow clusters"},
			{Type: "fix", Text: "secret value zeroing on esc leaked one frame to the alt-screen buffer"},
			{Type: "new", Text: "resources editor accepts binary suffixes (Gi/Mi) case-insensitively"},
			{Type: "perf", Text: "namespace palette CPU-share column no longer refetches per keystroke"},
		},
		Install: update.InstallInfo{Manager: "homebrew", Command: "brew install kute-dev/tap/kute"},
	}
	m := New(Config{Session: sess, OpenBrowser: func(string) error { return nil }})
	m.nowAt = time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	m.SetSize(width, height)
	return m
}

// goldenManyEntriesModel pins the changelog list filling all available
// vertical space and, once a release genuinely has more entries than fit,
// truncating to a "… N more" trailer rather than a fixed row cap — a
// realistically long release (12 entries) at a modest 80x24 forces that
// truncation, while the caller renders it at 120x36 too to confirm the
// extra room simply shows more real entries instead of triggering the
// trailer sooner.
func goldenManyEntriesModel(theme tui.Theme, width, height int) Model {
	sess := &tui.Session{Theme: theme, Styles: tui.NewStyles(theme), Version: "0.2.0"}
	sess.Update = &tui.UpdateInfo{
		Latest: update.Release{
			Version:     "0.2.1",
			PublishedAt: time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC),
			HTMLURL:     "https://github.com/kute-dev/kute/releases/tag/v0.2.1",
		},
		Changelog: []update.ChangelogEntry{
			{Type: "fix", Text: "rollout watch could miss the final ready event on slow clusters"},
			{Type: "fix", Text: "secret value zeroing on esc leaked one frame to the alt-screen buffer"},
			{Type: "fix", Text: "node-shell debug pod leaked when the exec session errored mid-attach"},
			{Type: "fix", Text: "helm history rollback picked the wrong revision after a fast-forward"},
			{Type: "new", Text: "resources editor accepts binary suffixes (Gi/Mi) case-insensitively"},
			{Type: "new", Text: "who-can query supports namespaced ClusterRoleBindings"},
			{Type: "new", Text: "forwards registry survives a context switch mid-session"},
			{Type: "perf", Text: "namespace palette CPU-share column no longer refetches per keystroke"},
			{Type: "perf", Text: "event dedupe groups now compute incrementally instead of full rescans"},
			{Type: "perf", Text: "YAML view syntax highlighting caches per-line instead of per-render"},
			{Type: "fix", Text: "PROD type-the-name modal accepted a trailing newline as a match"},
			{Type: "new", Text: "CRD printer columns now drive the generic list view's own sort keys"},
		},
		Install: update.InstallInfo{Manager: "homebrew", Command: "brew install kute-dev/tap/kute"},
	}
	m := New(Config{Session: sess, OpenBrowser: func(string) error { return nil }})
	m.nowAt = time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	m.SetSize(width, height)
	return m
}

func goldenEmptyModel(theme tui.Theme, width, height int) Model {
	sess := &tui.Session{Theme: theme, Styles: tui.NewStyles(theme), Version: "0.2.0"}
	sess.Update = &tui.UpdateInfo{Latest: update.Release{Version: "0.2.0"}}
	sess.State.UpdateCheck.LastChecked = time.Date(2026, 7, 19, 21, 0, 0, 0, time.UTC)
	m := New(Config{Session: sess, OpenBrowser: func(string) error { return nil }})
	m.nowAt = time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	m.SetSize(width, height)
	return m
}

func goldenLoadingModel(theme tui.Theme, width, height int) Model {
	sess := &tui.Session{Theme: theme, Styles: tui.NewStyles(theme), Version: "0.2.0"}
	m := New(Config{Session: sess, OpenBrowser: func(string) error { return nil }})
	m.SetSize(width, height)
	return m
}

func goldenFixtures() map[string]string {
	return map[string]string{
		"120x36-available.golden":   goldenAvailableModel(tui.Dark(), 120, 36).Render(),
		"120x36-empty.golden":       goldenEmptyModel(tui.Dark(), 120, 36).Render(),
		"120x36-loading.golden":     goldenLoadingModel(tui.Dark(), 120, 36).Render(),
		"80x24-available.golden":    goldenAvailableModel(tui.Dark(), 80, 24).Render(),
		"80x24-manyentries.golden":  goldenManyEntriesModel(tui.Dark(), 80, 24).Render(),
		"120x36-manyentries.golden": goldenManyEntriesModel(tui.Dark(), 120, 36).Render(),
		// A short body actually forces the "… N more" trailer (all 12
		// entries fit comfortably at 80x24/120x36 — that's the point of
		// those two above) — pins the truncating branch itself.
		"80x14-manyentries.golden": goldenManyEntriesModel(tui.Dark(), 80, 14).Render(),
	}
}

func TestGenerateGoldenFixtures(t *testing.T) {
	if os.Getenv("UPDATE_GOLDEN") != "1" {
		t.Skip("set UPDATE_GOLDEN=1 to regenerate update golden fixtures")
	}
	for name, got := range goldenFixtures() {
		path := filepath.Join("..", "..", "..", "..", "test", "golden", "update", name)
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func TestGoldenFixtures(t *testing.T) {
	for name, got := range goldenFixtures() {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join("..", "..", "..", "..", "test", "golden", "update", name)
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

// truecolorGoldenFixtures forces a truecolor profile in both themes,
// pinning the Theme-token-to-cell color mapping the plain goldens above
// can't see — same pattern as browse/podlogs (see their own golden_test.go
// doc comments). The profile swap is global, so this package must not run
// truecolor-rendering tests in parallel with each other.
func truecolorGoldenFixtures(t *testing.T) map[string]string {
	t.Helper()
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(old)

	return map[string]string{
		"120x36-dark.golden":  goldenAvailableModel(tui.Dark(), 120, 36).Render(),
		"120x36-light.golden": goldenAvailableModel(tui.Light(), 120, 36).Render(),
	}
}

func TestGenerateTruecolorGoldenFixtures(t *testing.T) {
	if os.Getenv("UPDATE_GOLDEN") != "1" {
		t.Skip("set UPDATE_GOLDEN=1 to regenerate update golden fixtures")
	}
	for name, got := range truecolorGoldenFixtures(t) {
		path := filepath.Join("..", "..", "..", "..", "test", "golden", "update", name)
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func TestTruecolorGoldenFixtures(t *testing.T) {
	for name, got := range truecolorGoldenFixtures(t) {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join("..", "..", "..", "..", "test", "golden", "update", name)
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
