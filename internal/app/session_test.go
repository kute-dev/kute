package app

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/kute-dev/kute/internal/state"
	"github.com/kute-dev/kute/internal/tui"
)

// writeTestKubeconfig writes a minimal multi-context kubeconfig (no live
// cluster needed — kube.AvailableContexts only reads the file, it never
// dials out) and points $KUBECONFIG at it for the test's duration.
func writeTestKubeconfig(t *testing.T, currentContext string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config")
	yaml := fmt.Sprintf(`apiVersion: v1
kind: Config
current-context: %s
clusters:
- name: cluster-a
  cluster:
    server: https://a.example.com
contexts:
- name: ctx-a
  context:
    cluster: cluster-a
    namespace: default
- name: ctx-b
  context:
    cluster: cluster-a
    namespace: default
users: []
`, currentContext)
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("writing test kubeconfig: %v", err)
	}
	t.Setenv("KUBECONFIG", path)
}

func TestSelectThemeFlagWins(t *testing.T) {
	t.Parallel()
	if got := selectTheme("dark", "light"); got != tui.Dark() {
		t.Fatalf("flag theme should win over config theme")
	}
}

func TestSelectThemeConfigFallsBackWhenNoFlag(t *testing.T) {
	t.Parallel()
	if got := selectTheme("", "light"); got != tui.Light() {
		t.Fatalf("config theme should apply when no flag is set")
	}
}

func TestSelectThemeUnrecognizedValuesDeferToDetection(t *testing.T) {
	t.Parallel()
	got := selectTheme("auto", "also-not-a-theme")
	if got != tui.Dark() && got != tui.Light() {
		t.Fatalf("expected a valid detected theme, got %+v", got)
	}
}

func TestBuildSessionDemoModeHasNoCluster(t *testing.T) {
	t.Parallel()
	sess, cluster, err := BuildSession(Config{AppName: "kute", Demo: true, Theme: "dark"})
	if cluster != nil {
		t.Fatalf("expected a nil *kube.Cluster in demo mode")
	}
	if err != nil {
		t.Fatalf("expected no error in demo mode, got %v", err)
	}
	if sess.Cluster != nil {
		t.Fatalf("expected Session.Cluster nil in demo mode")
	}
	if sess.Theme != tui.Dark() {
		t.Fatalf("expected the --theme override honored in demo mode")
	}
	if _, ok := sess.Registry.Descriptor("Pod"); !ok {
		t.Fatalf("expected the default registry populated on Session")
	}
	if len(sess.Groups) == 0 {
		t.Fatalf("expected default groups populated on Session")
	}
}

func TestStartupContextPrefersMostRecentAvailableContext(t *testing.T) {
	writeTestKubeconfig(t, "ctx-a")
	got := startupContext(state.State{RecentContexts: []string{"ctx-b", "ctx-a"}})
	if got != "ctx-b" {
		t.Fatalf("startupContext() = %q, want ctx-b", got)
	}
}

func TestStartupContextFallsBackWhenRecentContextIsGone(t *testing.T) {
	writeTestKubeconfig(t, "ctx-a")
	got := startupContext(state.State{RecentContexts: []string{"ctx-deleted"}})
	if got != "" {
		t.Fatalf("startupContext() = %q, want empty (defer to kubeconfig current-context)", got)
	}
}

func TestStartupContextEmptyWithNoRecents(t *testing.T) {
	writeTestKubeconfig(t, "ctx-a")
	got := startupContext(state.State{})
	if got != "" {
		t.Fatalf("startupContext() = %q, want empty", got)
	}
}
