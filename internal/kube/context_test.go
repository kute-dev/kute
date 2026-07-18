package kube

import "testing"

func TestUnavailableContext(t *testing.T) {
	t.Parallel()

	ctx := UnavailableContext("cluster unavailable")
	if ctx.ClusterName != "cluster unavailable" {
		t.Fatalf("ClusterName = %q", ctx.ClusterName)
	}
	if ctx.Namespace != "default" {
		t.Fatalf("Namespace = %q, want default", ctx.Namespace)
	}
}
