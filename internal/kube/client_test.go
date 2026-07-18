package kube

import (
	"errors"
	"testing"
)

// TestBuildConfigLookupErrorPaths pins 10b's "LOOKED IN" box data (mvp-plan.md
// Phase 4): each path kute checked, and why it didn't work.
func TestBuildConfigLookupErrorPaths(t *testing.T) {
	t.Parallel()
	cause := errors.New("stat /nonexistent: no such file or directory")

	t.Run("KUBECONFIG unset", func(t *testing.T) {
		t.Parallel()
		err := buildConfigLookupError("", "/home/x/.kube/config", cause)
		if len(err.Paths) != 2 {
			t.Fatalf("Paths = %d entries, want 2", len(err.Paths))
		}
		if err.Paths[0].Label != "$KUBECONFIG" || err.Paths[0].Reason != "not set" {
			t.Errorf("Paths[0] = %+v, want $KUBECONFIG/not set", err.Paths[0])
		}
		if err.Paths[1].Label != "~/.kube/config" || err.Paths[1].Path != "/home/x/.kube/config" {
			t.Errorf("Paths[1] = %+v, want ~/.kube/config with the resolved path", err.Paths[1])
		}
		if err.Paths[1].Reason != "no such file" {
			t.Errorf("Paths[1].Reason = %q, want %q", err.Paths[1].Reason, "no such file")
		}
	})

	t.Run("KUBECONFIG set to a missing file", func(t *testing.T) {
		t.Parallel()
		err := buildConfigLookupError("/tmp/does-not-exist-kute-test", "/home/x/.kube/config", cause)
		if err.Paths[0].Path != "/tmp/does-not-exist-kute-test" {
			t.Errorf("Paths[0].Path = %q, want the env value", err.Paths[0].Path)
		}
		if err.Paths[0].Reason != "no such file" {
			t.Errorf("Paths[0].Reason = %q, want %q", err.Paths[0].Reason, "no such file")
		}
	})

	t.Run("Error/Unwrap", func(t *testing.T) {
		t.Parallel()
		err := buildConfigLookupError("", "", cause)
		if !errors.Is(err, cause) {
			t.Errorf("errors.Is(err, cause) = false, want true (Unwrap must expose cause)")
		}
		if err.Error() == "" {
			t.Errorf("Error() should not be empty")
		}
	})
}
