package semver

import (
	"cmp"
	"testing"
)

func TestIsNewer(t *testing.T) {
	tests := []struct {
		name            string
		current, latest string
		want            bool
	}{
		// The cases internal/update's own version_test.go carried before
		// this package existed — kute's release check still routes here.
		{"patch bump", "0.2.0", "0.2.1", true},
		{"same", "0.2.0", "0.2.0", false},
		{"older", "0.2.1", "0.2.0", false},
		{"minor bump across patch", "0.1.9", "0.2.0", true},
		{"v prefix both", "v0.2.0", "v0.2.1", true},
		{"v prefix one side", "0.2.0", "v0.2.0", false},
		{"major beats minor", "1.0.0", "0.9.9", false},
		{"empty current", "", "0.1.0", true},
		{"unparseable current", "garbage", "0.1.0", true},

		// Pre-release ordering, which the pre-extraction comparator in
		// internal/update deliberately ignored: a chart repo serves
		// "1.16.0-beta.1" next to "1.15.4", so the ordering has to be real.
		{"release beats its own prerelease", "0.2.0-rc.1", "0.2.0", true},
		{"prerelease loses to release", "0.2.0", "0.2.0-rc.1", false},
		{"later prerelease", "1.0.0-rc.1", "1.0.0-rc.2", true},
		{"prerelease of a higher patch", "1.0.0", "1.0.1-rc.1", true},
		{"build metadata is not a version bump", "1.0.0", "1.0.0+build.5", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsNewer(tt.current, tt.latest); got != tt.want {
				t.Errorf("IsNewer(%q, %q) = %v, want %v", tt.current, tt.latest, got, tt.want)
			}
		})
	}
}

func TestComparePrerelease(t *testing.T) {
	// SemVer 2.0 §11.4's own worked example, in order.
	ordered := []string{
		"1.0.0-alpha",
		"1.0.0-alpha.1",
		"1.0.0-alpha.beta",
		"1.0.0-beta",
		"1.0.0-beta.2",
		"1.0.0-beta.11",
		"1.0.0-rc.1",
		"1.0.0",
	}
	for i := range ordered {
		for j := range ordered {
			want := cmp.Compare(i, j)
			if got := Compare(ordered[i], ordered[j]); got != want {
				t.Errorf("Compare(%q, %q) = %d, want %d", ordered[i], ordered[j], got, want)
			}
		}
	}
}

func TestIsPrerelease(t *testing.T) {
	tests := map[string]bool{
		"1.2.3":            false,
		"v1.2.3":           false,
		"1.2.3+build.5":    false,
		"1.2.3-rc.1":       true,
		"1.2.3-0":          true,
		"1.2.3-rc.1+build": true,
		"":                 false,
		"garbage":          false,
	}
	for v, want := range tests {
		if got := IsPrerelease(v); got != want {
			t.Errorf("IsPrerelease(%q) = %v, want %v", v, got, want)
		}
	}
}
