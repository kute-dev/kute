package kube

import (
	"os/exec"
	"strings"
)

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

// Node labels the two managed platforms that can't host a node shell set on
// their own nodes.
const (
	// fargateComputeTypeLabel is set to "fargate" on the Node object EKS
	// synthesises per Fargate pod. Documented and definitive — it is what
	// `kubectl get nodes -l eks.amazonaws.com/compute-type=fargate` selects.
	fargateComputeTypeLabel = "eks.amazonaws.com/compute-type"
	// gkeNodePoolLabel is on every GKE node, Autopilot or Standard. It is
	// only used to confirm a node is GKE's at all before the name check
	// below is allowed to mean anything.
	gkeNodePoolLabel = "cloud.google.com/gke-nodepool"
	// autopilotNodePrefix is how GKE names Autopilot-managed nodes —
	// "gk3-<cluster>-…" against Standard's "gke-<cluster>-…". Google
	// generates both prefixes; neither is user-settable, so a Standard node
	// cannot be mistaken for an Autopilot one.
	autopilotNodePrefix = "gk3-"
)

// NodeShellUnavailable explains why a node shell cannot work on this node, or
// returns "" when it can. A node shell is `kubectl debug --profile=sysadmin`:
// a privileged container in the node's host namespaces, chrooted into the
// node's root filesystem. Two managed platforms make that impossible, and
// they fail in ways worth telling apart from "the command didn't work":
//
//   - EKS Fargate. Each pod gets its own synthetic Node object with no
//     machine behind it to attach to.
//   - GKE Autopilot. The nodes are real, but Autopilot's admission webhooks
//     reject privileged host-namespace containers outright.
//
// Neither is detectable from a cluster-level API — Autopilot in particular is
// a property of the GKE cluster resource, not of anything Kubernetes serves —
// so this reads the node's own labels, falling back to GKE's Autopilot node
// naming for the case where no label distinguishes it. Deliberately
// conservative: a false negative just means the user sees kubectl's own
// rejection instead of ours, while a false positive would hide a working
// verb, so the name check only applies to nodes already known to be GKE's.
//
// This is a message, not a gate: docs/managed-clusters.md §3 asks for "a
// clear error naming the reason, not a hidden key", so 's' stays on the
// keybar and explains itself when pressed.
func NodeShellUnavailable(name string, labels map[string]string) string {
	if labels[fargateComputeTypeLabel] == "fargate" {
		return "node shell is unavailable on EKS Fargate: the pod has no node to attach to"
	}
	if _, gke := labels[gkeNodePoolLabel]; gke && strings.HasPrefix(name, autopilotNodePrefix) {
		return "node shell is unavailable on GKE Autopilot: it rejects the privileged host-namespace container kubectl debug needs"
	}
	return ""
}
