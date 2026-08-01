package kube

import (
	"os/exec"
)

// editArgs builds the kubectl edit argv for EditSpec. ResourceArg is what
// makes the resource name resolvable for every registered ResourceKind —
// the lowercased Kind for almost all of them (the same resolution
// NodeShellSpec relies on implicitly via "node/"+node), and the
// fully-qualified <plural>.<group> for a kind whose registry key isn't its
// API Kind, where the bare name wouldn't resolve at all.
func editArgs(kind ResourceKind, namespace, name string) []string {
	args := []string{"edit", kind.ResourceArg() + "/" + name}
	if namespace != "" {
		args = append(args, "-n", namespace)
	}
	return args
}

// EditSpec builds the kubectl edit command for the Edit verb ('E' on any
// row in browse, poddetail, nodedetail). Bubble Tea suspends and hands the
// tty to this process (tea.ExecProcess), the same handoff as ExecSpec/
// NodeShellSpec. kubectl opens $EDITOR, owns schema validation and the
// resourceVersion conflict retry, and aborts cleanly on an unchanged save —
// no client-side diffing needed here.
func EditSpec(kind ResourceKind, namespace, name string) *exec.Cmd {
	return exec.Command("kubectl", editArgs(kind, namespace, name)...)
}
