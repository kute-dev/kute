package kube

import (
	"os/exec"
	"strings"
)

// DebugProfile is one of kubectl debug's --profile values (docs/design
// v.0.11.0.dc.html §41b/§41d: "shown, never hidden"). general is the
// default for a pod debug session; sysadmin is the default kute has always
// used for a node debug session (formerly hardcoded — see NodeDebugSpec).
type DebugProfile string

const (
	ProfileGeneral    DebugProfile = "general"
	ProfileSysadmin   DebugProfile = "sysadmin"
	ProfileNetadmin   DebugProfile = "netadmin"
	ProfileRestricted DebugProfile = "restricted"
)

// DebugProfiles is the cycle order the debug panel's 'p' key steps through.
var DebugProfiles = []DebugProfile{ProfileGeneral, ProfileSysadmin, ProfileNetadmin, ProfileRestricted}

// Next cycles p to the following DebugProfiles entry, wrapping around. An
// unrecognized value (should not happen — the panel only ever sets p from
// this list) starts back at the first entry.
func (p DebugProfile) Next() DebugProfile {
	for i, candidate := range DebugProfiles {
		if candidate == p {
			return DebugProfiles[(i+1)%len(DebugProfiles)]
		}
	}
	return DebugProfiles[0]
}

// DefaultDebugImage is the pod debug session's default image (§41b/§41c) —
// distinct from DefaultNodeShellImage (nodeshell.go), which only needs a
// chroot binary; netshoot ships the network/troubleshooting tools a pod
// debug session is for.
const DefaultDebugImage = "nicolaka/netshoot"

// DefaultDebugCopyEntrypoint is §41c's default replacement command — this is
// what actually stops a crash loop, not just a convenience default.
const DefaultDebugCopyEntrypoint = "sh"

// DefaultDebugCopyName is the copy panel's initial "copy name" field value.
func DefaultDebugCopyName(pod string) string { return pod + "-debug" }

// DebugAttachResource/DebugCopyResource name the API resource §41a's RBAC
// pre-check asks about — the create verb kubectl debug's chosen mode
// actually exercises against the server: attach mode creates an ephemeral
// container (the pods/ephemeralcontainers subresource), copy mode creates a
// whole new pod.
const (
	DebugAttachResource = "pods/ephemeralcontainers"
	DebugCopyResource   = "pods"
)

// PodWontStayRunning reports whether a pod is in the state §41c's copy-mode
// gate exists for — CrashLoopBackOff, or any container currently Waiting
// (e.g. ImagePullBackOff) — shared by the debug panel's own mode default
// (debugpanel.Model.podWontStayRunning) and the routing callers that need
// to know the mode before the panel exists (browse/poddetail's §41a RBAC
// pre-check), so the two predicates can't drift apart.
func PodWontStayRunning(podPhase string, waiting bool) bool {
	return waiting || podPhase == "CrashLoopBackOff"
}

// commandString renders args as a copy-pasteable kubectl invocation,
// quoting any argument containing whitespace — shared by every
// *CommandString function in this file, mirroring ExecCommandString's own
// quoting (exec.go) so the three debug builders can't drift apart in how
// they document themselves.
func commandString(args []string) string {
	out := append([]string{"kubectl"}, args...)
	for i, a := range out {
		if strings.ContainsAny(a, " \t") {
			out[i] = "'" + a + "'"
		}
	}
	return strings.Join(out, " ")
}

// podDebugAttachArgs builds the kubectl debug argv for §41b: an ephemeral
// container attached to a running pod, sharing target's process namespace.
// Empty image/profile default rather than emit a malformed command — the
// panel always supplies both, but a defensive default keeps this builder
// safe to call directly (e.g. from a test) the same way nodeDebugArgs
// defaults image.
func podDebugAttachArgs(namespace, pod, image, target string, profile DebugProfile) []string {
	if image == "" {
		image = DefaultDebugImage
	}
	if profile == "" {
		profile = ProfileGeneral
	}
	return []string{
		"debug", "-it", pod, "-n", namespace,
		"--image", image,
		"--target", target,
		"--profile", string(profile),
	}
}

// PodDebugAttachSpec builds the kubectl debug command for §41b's "attach
// ephemeral" launch. Bubble Tea suspends and hands the tty to this process
// (tea.ExecProcess), the same handoff as ExecSpec/NodeDebugSpec. kubectl
// creates the ephemeral container as part of -it — there is no separate
// cluster write kute makes first.
func PodDebugAttachSpec(namespace, pod, image, target string, profile DebugProfile) *exec.Cmd {
	return exec.Command("kubectl", podDebugAttachArgs(namespace, pod, image, target, profile)...)
}

// PodDebugAttachCommandString renders the exact kubectl invocation
// PodDebugAttachSpec builds, for §41b's "will run" line.
func PodDebugAttachCommandString(namespace, pod, image, target string, profile DebugProfile) string {
	return commandString(podDebugAttachArgs(namespace, pod, image, target, profile))
}

// podDebugCopyArgs builds the kubectl debug argv for §41c: a copy of pod
// with entrypoint replacing its command — what actually breaks a crash
// loop, so a pod that won't stay running can be inspected at all.
func podDebugCopyArgs(namespace, pod, copyName, container, entrypoint string, shareProcesses bool) []string {
	if entrypoint == "" {
		entrypoint = DefaultDebugCopyEntrypoint
	}
	args := []string{
		"debug", "-it", pod, "-n", namespace,
		"--copy-to", copyName,
		"--container", container,
	}
	if shareProcesses {
		args = append(args, "--share-processes")
	}
	return append(args, "--", entrypoint)
}

// PodDebugCopySpec builds the kubectl debug command for §41c's "copy pod"
// launch. Same tea.ExecProcess handoff as PodDebugAttachSpec/ExecSpec; the
// original pod is untouched, still crash-looping, once this exits.
func PodDebugCopySpec(namespace, pod, copyName, container, entrypoint string, shareProcesses bool) *exec.Cmd {
	return exec.Command("kubectl", podDebugCopyArgs(namespace, pod, copyName, container, entrypoint, shareProcesses)...)
}

// PodDebugCopyCommandString renders the exact kubectl invocation
// PodDebugCopySpec builds, for §41c's "will run" line.
func PodDebugCopyCommandString(namespace, pod, copyName, container, entrypoint string, shareProcesses bool) string {
	return commandString(podDebugCopyArgs(namespace, pod, copyName, container, entrypoint, shareProcesses))
}

// nodeDebugArgs builds the kubectl debug argv for §41d — the node-debug
// panel that replaced the former 's' NodeShell verb. This is nodeShellArgs'
// exact former argv (see git history), generalized so profile is a
// parameter instead of hardcoded "sysadmin": the same privileged,
// host-namespace debug container, chrooted into the node's root mount via
// the same static bash-then-sh fallback ExecSpec uses. The chroot/shell
// tail is fixed, never a panel field — it is what makes the shell usable,
// not a detail to expose.
func nodeDebugArgs(node, image string, profile DebugProfile) []string {
	if image == "" {
		image = DefaultNodeShellImage
	}
	if profile == "" {
		profile = ProfileSysadmin
	}
	return []string{
		"debug", "node/" + node, "-it",
		"--image", image,
		"--profile", string(profile),
		"--", "chroot", "/host",
		"sh", "-c", "command -v bash >/dev/null && exec bash || exec sh",
	}
}

// NodeDebugSpec builds the kubectl debug command for the node-debug panel
// ('x' on a Node row / in tasks/nodedetail — §41d). Bubble Tea suspends and
// hands the tty to this process (tea.ExecProcess), the same handoff as
// ExecSpec. kubectl leaves the node-debugger pod behind in a Completed
// state after exit — its own documented behavior; it prints the pod name on
// entry, so cleanup stays visible to (and with) the user for MVP.
func NodeDebugSpec(node, image string, profile DebugProfile) *exec.Cmd {
	return exec.Command("kubectl", nodeDebugArgs(node, image, profile)...)
}

// NodeDebugCommandString renders the exact kubectl invocation NodeDebugSpec
// builds, for the debug panel's "will run" line — the former 's' key never
// had one; unlike the retired one-shot launch, this is now shown before the
// launch ever runs.
func NodeDebugCommandString(node, image string, profile DebugProfile) string {
	return commandString(nodeDebugArgs(node, image, profile))
}
