package kube

import (
	"os/exec"
	"strings"
)

// execArgs builds the kubectl exec argv shared by ExecSpec and
// ExecCommandString, so the 10a picker's "will run" documentation line can
// never drift from the command that actually executes.
func execArgs(namespace, pod, container, shell string) []string {
	args := []string{"exec", "-it", pod, "-n", namespace}
	if container != "" {
		args = append(args, "-c", container)
	}
	args = append(args, "--")
	if shell != "" {
		args = append(args, shell)
	} else {
		args = append(args, "sh", "-c", "command -v bash >/dev/null && exec bash || exec sh")
	}
	return args
}

// ExecSpec builds the kubectl exec command for the 10a exec screen. Bubble
// Tea suspends and hands the tty to this process (tea.ExecProcess); exit
// returns control to the pod that launched it. Shell detection is static
// for MVP: try bash, fall back to sh — no live in-container probe.
func ExecSpec(namespace, pod, container, shell string) *exec.Cmd {
	return exec.Command("kubectl", execArgs(namespace, pod, container, shell)...)
}

// ExecCommandString renders the exact kubectl invocation ExecSpec builds,
// for 10a's "will run" line (docs/design README.md §10a: "no magic,
// copyable documentation") — quoting any argument containing whitespace
// (the shell-fallback probe's `-c` payload).
func ExecCommandString(namespace, pod, container, shell string) string {
	args := append([]string{"kubectl"}, execArgs(namespace, pod, container, shell)...)
	for i, a := range args {
		if strings.ContainsAny(a, " \t") {
			args[i] = "'" + a + "'"
		}
	}
	return strings.Join(args, " ")
}
