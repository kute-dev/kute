package kube

import (
	"os/exec"
	"strings"
)

// editArgs builds the kubectl edit argv for EditSpec — kind lowercased is a
// valid kubectl singular resource name for every registered ResourceKind
// (the same resolution NodeShellSpec relies on implicitly via "node/"+node).
func editArgs(kind ResourceKind, namespace, name string) []string {
	args := []string{"edit", strings.ToLower(string(kind)) + "/" + name}
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
