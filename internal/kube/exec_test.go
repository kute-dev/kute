package kube

import (
	"strings"
	"testing"
)

func TestExecSpecWithContainerAndShell(t *testing.T) {
	t.Parallel()
	cmd := ExecSpec("default", "api-1", "worker", "bash")
	got := strings.Join(cmd.Args, " ")
	want := "kubectl exec -it api-1 -n default -c worker -- bash"
	if got != want {
		t.Fatalf("ExecSpec args = %q, want %q", got, want)
	}
}

func TestExecSpecWithoutContainer(t *testing.T) {
	t.Parallel()
	cmd := ExecSpec("default", "api-1", "", "sh")
	got := strings.Join(cmd.Args, " ")
	want := "kubectl exec -it api-1 -n default -- sh"
	if got != want {
		t.Fatalf("ExecSpec args = %q, want %q", got, want)
	}
}

func TestExecSpecWithoutShellFallsBackToDetection(t *testing.T) {
	t.Parallel()
	cmd := ExecSpec("default", "api-1", "worker", "")
	got := strings.Join(cmd.Args, " ")
	if !strings.Contains(got, "command -v bash") {
		t.Fatalf("expected the bash-then-sh fallback probe, got %q", got)
	}
	if !strings.HasPrefix(got, "kubectl exec -it api-1 -n default -c worker -- sh -c") {
		t.Fatalf("unexpected command prefix: %q", got)
	}
}

func TestExecCommandStringMatchesExecSpec(t *testing.T) {
	t.Parallel()
	got := ExecCommandString("default", "api-1", "worker", "bash")
	want := "kubectl exec -it api-1 -n default -c worker -- bash"
	if got != want {
		t.Fatalf("ExecCommandString = %q, want %q", got, want)
	}
}

func TestExecCommandStringQuotesFallbackProbe(t *testing.T) {
	t.Parallel()
	got := ExecCommandString("default", "api-1", "worker", "")
	want := "kubectl exec -it api-1 -n default -c worker -- sh -c 'command -v bash >/dev/null && exec bash || exec sh'"
	if got != want {
		t.Fatalf("ExecCommandString = %q, want %q", got, want)
	}
}
