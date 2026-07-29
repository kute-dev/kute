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

func TestNodeShellUnavailable(t *testing.T) {
	tests := []struct {
		name    string
		node    string
		labels  map[string]string
		wantSub string
	}{
		{
			name:   "plain node",
			node:   "node-a",
			labels: map[string]string{"kubernetes.io/os": "linux"},
		},
		{
			name:   "GKE Standard node",
			node:   "gke-prod-default-pool-1a2b3c4d-xyz",
			labels: map[string]string{gkeNodePoolLabel: "default-pool"},
		},
		{
			name:   "EKS EC2 node",
			node:   "ip-10-0-1-23.eu-west-1.compute.internal",
			labels: map[string]string{fargateComputeTypeLabel: "ec2"},
		},
		{
			name:    "EKS Fargate node",
			node:    "fargate-ip-10-0-1-23.eu-west-1.compute.internal",
			labels:  map[string]string{fargateComputeTypeLabel: "fargate"},
			wantSub: "EKS Fargate",
		},
		{
			name:    "GKE Autopilot node",
			node:    "gk3-autopilot-cluster-default-pool-bcd71fbe-6qh9",
			labels:  map[string]string{gkeNodePoolLabel: "default-pool"},
			wantSub: "GKE Autopilot",
		},
		{
			// The name prefix alone must never decide: it only means
			// anything on a node GKE's own label already claims.
			name:   "gk3-prefixed name outside GKE",
			node:   "gk3-something-homegrown",
			labels: map[string]string{},
		},
	}
	for _, tt := range tests {
		got := NodeShellUnavailable(tt.node, tt.labels)
		switch {
		case tt.wantSub == "" && got != "":
			t.Errorf("%s: NodeShellUnavailable = %q, want it available", tt.name, got)
		case tt.wantSub != "" && !strings.Contains(got, tt.wantSub):
			t.Errorf("%s: NodeShellUnavailable = %q, want it to name %q", tt.name, got, tt.wantSub)
		}
	}
}
