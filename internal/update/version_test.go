package update

import "testing"

func TestIsNewer(t *testing.T) {
	tests := []struct {
		current, latest string
		want            bool
	}{
		{"0.2.0", "0.2.1", true},
		{"0.2.0", "0.2.0", false},
		{"0.2.1", "0.2.0", false},
		{"0.1.9", "0.2.0", true},
		{"v0.2.0", "v0.2.1", true},
		{"0.2.0", "v0.2.0", false},
		{"1.0.0", "0.9.9", false},
		// Pre-release suffixes are ignored for ordering (see IsNewer's doc
		// comment) — same MAJOR.MINOR.PATCH compares equal either direction.
		{"0.2.0", "0.2.0-rc.1", false},
		{"0.2.0-rc.1", "0.2.0", false},
		{"", "0.1.0", true},
		{"garbage", "0.1.0", true},
	}
	for _, tt := range tests {
		if got := IsNewer(tt.current, tt.latest); got != tt.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", tt.current, tt.latest, got, tt.want)
		}
	}
}
