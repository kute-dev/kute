package kube

import (
	"slices"
	"strings"
	"testing"
)

func TestDebugProfileNextCycles(t *testing.T) {
	t.Parallel()
	got := ProfileGeneral
	for _, want := range []DebugProfile{ProfileSysadmin, ProfileNetadmin, ProfileRestricted, ProfileGeneral} {
		got = got.Next()
		if got != want {
			t.Fatalf("Next() = %q, want %q", got, want)
		}
	}
}

func TestPodDebugAttachSpec(t *testing.T) {
	t.Parallel()
	cmd := PodDebugAttachSpec("nva-stage", "nva-gateway-2b81x", "nicolaka/netshoot", "gateway", ProfileGeneral)
	got := strings.Join(cmd.Args, " ")
	want := "kubectl debug -it nva-gateway-2b81x -n nva-stage --image nicolaka/netshoot --target gateway --profile general"
	if got != want {
		t.Fatalf("PodDebugAttachSpec args = %q, want %q", got, want)
	}
}

func TestPodDebugAttachSpecDefaultsImageAndProfile(t *testing.T) {
	t.Parallel()
	cmd := PodDebugAttachSpec("default", "api-1", "", "worker", "")
	joined := strings.Join(cmd.Args, " ")
	if !strings.Contains(joined, "--image "+DefaultDebugImage) {
		t.Errorf("expected default image %q in %q", DefaultDebugImage, joined)
	}
	if !strings.Contains(joined, "--profile "+string(ProfileGeneral)) {
		t.Errorf("expected default profile %q in %q", ProfileGeneral, joined)
	}
}

func TestPodDebugAttachCommandStringMatchesSpec(t *testing.T) {
	t.Parallel()
	got := PodDebugAttachCommandString("nva-stage", "nva-gateway-2b81x", "nicolaka/netshoot", "gateway", ProfileGeneral)
	want := "kubectl debug -it nva-gateway-2b81x -n nva-stage --image nicolaka/netshoot --target gateway --profile general"
	if got != want {
		t.Fatalf("PodDebugAttachCommandString = %q, want %q", got, want)
	}
}

func TestPodDebugCopySpecSharedProcesses(t *testing.T) {
	t.Parallel()
	cmd := PodDebugCopySpec("nva-stage", "nva-worker-9k2ss", "nva-worker-9k2ss-debug", "worker", "sh", true, ProfileNetadmin)
	got := strings.Join(cmd.Args, " ")
	want := "kubectl debug -it nva-worker-9k2ss -n nva-stage --copy-to nva-worker-9k2ss-debug --container worker --profile netadmin --share-processes -- sh"
	if got != want {
		t.Fatalf("PodDebugCopySpec args = %q, want %q", got, want)
	}
}

func TestPodDebugCopySpecWithoutSharedProcesses(t *testing.T) {
	t.Parallel()
	cmd := PodDebugCopySpec("nva-stage", "nva-worker-9k2ss", "nva-worker-9k2ss-debug", "worker", "sh", false, ProfileGeneral)
	got := strings.Join(cmd.Args, " ")
	if strings.Contains(got, "--share-processes") {
		t.Fatalf("PodDebugCopySpec must omit --share-processes when false: %q", got)
	}
}

func TestPodDebugCopySpecDefaultsEntrypoint(t *testing.T) {
	t.Parallel()
	cmd := PodDebugCopySpec("default", "api-1", "api-1-debug", "worker", "", false, "")
	if !slices.Contains(cmd.Args, DefaultDebugCopyEntrypoint) {
		t.Fatalf("expected default entrypoint %q in %v", DefaultDebugCopyEntrypoint, cmd.Args)
	}
	if got := strings.Join(cmd.Args, " "); !strings.Contains(got, "--profile general") {
		t.Fatalf("empty profile did not default to general: %q", got)
	}
}

func TestPodDebugCopyCommandStringMatchesSpec(t *testing.T) {
	t.Parallel()
	got := PodDebugCopyCommandString("nva-stage", "nva-worker-9k2ss", "nva-worker-9k2ss-debug", "worker", "sh", true, ProfileSysadmin)
	want := "kubectl debug -it nva-worker-9k2ss -n nva-stage --copy-to nva-worker-9k2ss-debug --container worker --profile sysadmin --share-processes -- sh"
	if got != want {
		t.Fatalf("PodDebugCopyCommandString = %q, want %q", got, want)
	}
}

func TestDefaultDebugCopyName(t *testing.T) {
	t.Parallel()
	if got, want := DefaultDebugCopyName("nva-worker-9k2ss"), "nva-worker-9k2ss-debug"; got != want {
		t.Fatalf("DefaultDebugCopyName = %q, want %q", got, want)
	}
}

// TestNodeDebugSpecMatchesLegacyNodeShellDefaults pins NodeDebugSpec to the
// exact argv the retired 's' NodeShell verb used to run (see git history's
// nodeShellArgs) — the debug panel's un-edited defaults must launch the
// byte-identical command, chroot tail included, that 's' launched with zero
// confirmation.
func TestNodeDebugSpecMatchesLegacyNodeShellDefaults(t *testing.T) {
	t.Parallel()
	cmd := NodeDebugSpec("node-a", "", "")
	want := []string{
		"kubectl", "debug", "node/node-a", "-it",
		"--image", DefaultNodeShellImage,
		"--profile", "sysadmin",
		"--", "chroot", "/host",
		"sh", "-c", "command -v bash >/dev/null && exec bash || exec sh",
	}
	if got := cmd.Args; !slices.Equal(got, want) {
		t.Fatalf("NodeDebugSpec args = %q, want %q", got, want)
	}
}

func TestNodeDebugSpecCustomImageAndProfile(t *testing.T) {
	t.Parallel()
	cmd := NodeDebugSpec("node-a", "registry.internal/tools/debug:v2", ProfileNetadmin)
	joined := strings.Join(cmd.Args, " ")
	if !strings.Contains(joined, "--image registry.internal/tools/debug:v2") {
		t.Fatalf("NodeDebugSpec args missing custom image: %q", cmd.Args)
	}
	if strings.Contains(joined, DefaultNodeShellImage) {
		t.Fatalf("NodeDebugSpec must not fall back to the default image when one is given: %q", cmd.Args)
	}
	if !strings.Contains(joined, "--profile netadmin") {
		t.Fatalf("NodeDebugSpec args missing custom profile: %q", cmd.Args)
	}
}

func TestNodeDebugCommandStringMatchesSpec(t *testing.T) {
	t.Parallel()
	got := NodeDebugCommandString("node-a", "busybox:1.37", ProfileSysadmin)
	want := "kubectl debug node/node-a -it --image busybox:1.37 --profile sysadmin -- chroot /host sh -c 'command -v bash >/dev/null && exec bash || exec sh'"
	if got != want {
		t.Fatalf("NodeDebugCommandString = %q, want %q", got, want)
	}
}

func TestPodWontStayRunning(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		podPhase string
		waiting  bool
		want     bool
	}{
		{"running", "Running", false, false},
		{"running with waiting container", "Running", true, true},
		{"pending", "Pending", false, true},
		{"succeeded", "Succeeded", false, true},
		{"failed", "Failed", false, true},
		{"unknown", "Unknown", false, true},
		{"phase unavailable", "", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := PodWontStayRunning(tc.podPhase, tc.waiting); got != tc.want {
				t.Fatalf("PodWontStayRunning(%q, %v) = %v, want %v", tc.podPhase, tc.waiting, got, tc.want)
			}
		})
	}
}
