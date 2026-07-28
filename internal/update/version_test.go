package update

import "testing"

// TestIsNewer keeps a thin check that the delegation to internal/semver is
// wired the right way round (it's easy to swap current/latest and have every
// equal-version case still pass) — the ordering rules themselves are tested
// in internal/semver.
func TestIsNewer(t *testing.T) {
	tests := []struct {
		current, latest string
		want            bool
	}{
		{"0.2.0", "0.2.1", true},
		{"0.2.0", "0.2.0", false},
		{"0.2.1", "0.2.0", false},
		{"v0.2.0", "v0.2.1", true},
		{"", "0.1.0", true},
		// A pre-release build is behind the release of the same version.
		{"0.2.0-rc.1", "0.2.0", true},
		{"0.2.0", "0.2.0-rc.1", false},
	}
	for _, tt := range tests {
		if got := IsNewer(tt.current, tt.latest); got != tt.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", tt.current, tt.latest, got, tt.want)
		}
	}
}
