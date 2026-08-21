package kube

import (
	"strings"
	"testing"
)

// NodeDebugSpec's own argv coverage (formerly NodeShellSpec's) lives in
// debug_test.go, alongside the other two debug builders it was generalized
// to sit beside.

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
