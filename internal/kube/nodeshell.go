package kube

import "os/exec"

// DefaultNodeShellImage is the debug-container image used when the user
// config's nodeShellImage is unset. The image only needs a `chroot` binary —
// the shell itself resolves against the host root, not the image.
const DefaultNodeShellImage = "busybox:1.37"

// nodeShellArgs builds the kubectl debug argv for NodeShellSpec.
// --profile=sysadmin runs the debug container privileged with host
// namespaces (a node shell without root access on the node would be
// pointless), and `chroot /host` pivots into the node's root mount, with the
// same static bash-then-sh fallback ExecSpec uses — resolved against the
// host filesystem.
func nodeShellArgs(node, image string) []string {
	if image == "" {
		image = DefaultNodeShellImage
	}
	return []string{
		"debug", "node/" + node, "-it",
		"--image", image,
		"--profile", "sysadmin",
		"--", "chroot", "/host",
		"sh", "-c", "command -v bash >/dev/null && exec bash || exec sh",
	}
}

// NodeShellSpec builds the kubectl debug command for the node-shell verb
// ('s' on a Nodes row / in tasks/nodedetail). Bubble Tea suspends and hands
// the tty to this process (tea.ExecProcess), the same handoff as ExecSpec.
// kubectl leaves the node-debugger pod behind in a Completed state after
// exit — its own documented behavior; it prints the pod name on entry, so
// cleanup stays visible to (and with) the user for MVP.
func NodeShellSpec(node, image string) *exec.Cmd {
	return exec.Command("kubectl", nodeShellArgs(node, image)...)
}
