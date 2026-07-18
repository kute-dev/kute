package kube

import (
	"strings"
	"testing"
)

func TestEditSpecNamespacedKind(t *testing.T) {
	t.Parallel()
	cmd := EditSpec(KindPod, "default", "api-1")
	got := strings.Join(cmd.Args, " ")
	want := "kubectl edit pod/api-1 -n default"
	if got != want {
		t.Fatalf("EditSpec args = %q, want %q", got, want)
	}
}

func TestEditSpecClusterScopedKindOmitsNamespace(t *testing.T) {
	t.Parallel()
	cmd := EditSpec(KindNode, "", "node-a")
	got := strings.Join(cmd.Args, " ")
	want := "kubectl edit node/node-a"
	if got != want {
		t.Fatalf("EditSpec args = %q, want %q", got, want)
	}
}
