package kube

import (
	"slices"
	"strings"
	"testing"
)

func TestNodeShellSpecDefaultsImage(t *testing.T) {
	cmd := NodeShellSpec("node-a", "")
	want := []string{
		"kubectl", "debug", "node/node-a", "-it",
		"--image", DefaultNodeShellImage,
		"--profile", "sysadmin",
		"--", "chroot", "/host",
		"sh", "-c", "command -v bash >/dev/null && exec bash || exec sh",
	}
	if got := cmd.Args; !slices.Equal(got, want) {
		t.Fatalf("NodeShellSpec args = %q, want %q", got, want)
	}
}

func TestNodeShellSpecCustomImage(t *testing.T) {
	cmd := NodeShellSpec("node-a", "registry.internal/tools/debug:v2")
	joined := strings.Join(cmd.Args, " ")
	if !strings.Contains(joined, "--image registry.internal/tools/debug:v2") {
		t.Fatalf("NodeShellSpec args missing custom image: %q", cmd.Args)
	}
	if strings.Contains(joined, DefaultNodeShellImage) {
		t.Fatalf("NodeShellSpec must not fall back to the default image when one is given: %q", cmd.Args)
	}
}
