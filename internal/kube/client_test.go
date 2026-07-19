package kube

import (
	"errors"
	"os"
	"path/filepath"
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

// TestNewClientForContextHonorsExplicitOverride pins a real bug found while
// building 4c's SwitchToContext (docs/design README.md §4c: "↵ connect to
// selected"): rawConfig.RawConfig() reflects the kubeconfig file's own
// current-context verbatim — configOverrides only ever reaches
// clientConfig.ClientConfig()'s constructed REST config, never this raw
// view — so requesting a context other than the file's own default must
// still resolve ContextName/ClusterName/Namespace to the one actually
// requested, not silently fall back to describing the file's default one
// (previously: switching from "ctx-a" to "ctx-b" correctly dialed ctx-b's
// server, but the header kept naming "ctx-a").
func TestNewClientForContextHonorsExplicitOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kubeconfig")
	kubeconfig := `apiVersion: v1
kind: Config
current-context: ctx-a
clusters:
- name: cluster-a
  cluster:
    server: https://127.0.0.1:16440
- name: cluster-b
  cluster:
    server: https://127.0.0.1:16441
contexts:
- name: ctx-a
  context:
    cluster: cluster-a
    namespace: ns-a
    user: user-a
- name: ctx-b
  context:
    cluster: cluster-b
    namespace: ns-b
    user: user-b
users:
- name: user-a
  user: {}
- name: user-b
  user: {}
`
	if err := os.WriteFile(path, []byte(kubeconfig), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	t.Setenv("KUBECONFIG", path)

	client, err := newClientForContext("ctx-b")
	if err != nil {
		t.Fatalf("newClientForContext(ctx-b) = %v", err)
	}
	if client.Context.ContextName != "ctx-b" {
		t.Errorf("ContextName = %q, want %q", client.Context.ContextName, "ctx-b")
	}
	if client.Context.ClusterName != "cluster-b" {
		t.Errorf("ClusterName = %q, want %q", client.Context.ClusterName, "cluster-b")
	}
	if client.Context.Namespace != "ns-b" {
		t.Errorf("Namespace = %q, want %q", client.Context.Namespace, "ns-b")
	}

	// The no-override case ("" — NewCluster's own call) must still fall
	// back to the file's own current-context, unchanged.
	client, err = newClientForContext("")
	if err != nil {
		t.Fatalf("newClientForContext(\"\") = %v", err)
	}
	if client.Context.ContextName != "ctx-a" {
		t.Errorf("ContextName = %q, want the file's default %q", client.Context.ContextName, "ctx-a")
	}
}
