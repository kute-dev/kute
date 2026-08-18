package main

import "testing"

// TestConflictingScopeFlags pins the usage-error rule
// (docs/plans/namespace-scoped-final-plan.md): --namespace-scoped already
// selects the namespace, so pairing it with either spelling of -n/--namespace
// is ambiguous rather than a precedence question, and every other
// combination is a normal launch.
func TestConflictingScopeFlags(t *testing.T) {
	tests := []struct {
		name    string
		set     []string
		wantErr bool
	}{
		{"neither set", nil, false},
		{"namespace alone", []string{"namespace"}, false},
		{"n alone", []string{"n"}, false},
		{"namespace-scoped alone", []string{"namespace-scoped"}, false},
		{"other unrelated flags alone", []string{"context", "demo"}, false},
		{"namespace and namespace-scoped", []string{"namespace", "namespace-scoped"}, true},
		{"n and namespace-scoped", []string{"n", "namespace-scoped"}, true},
		{"namespace, n, and namespace-scoped", []string{"namespace", "n", "namespace-scoped"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := conflictingScopeFlags(tt.set)
			if tt.wantErr && err == nil {
				t.Fatalf("conflictingScopeFlags(%v) = nil, want an error", tt.set)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("conflictingScopeFlags(%v) = %v, want nil", tt.set, err)
			}
		})
	}
}
