package update

import (
	"strconv"
	"strings"
)

// IsNewer reports whether latest is a newer version than current. Both are
// plain "MAJOR.MINOR.PATCH[-pre]" strings with any leading "v" already
// stripped (Release.Version and kute's own build version are both kept in
// this form). Compares the three numeric components in order; a malformed
// or missing component compares as 0, so an unparseable string never
// panics — it just reads as "no newer than anything sensible". Pre-release
// suffixes are ignored for ordering (kute's own release cadence is plain
// semver; GitHub's /releases/latest already excludes prereleases, so this
// never needs to rank "0.2.1-rc.1" against "0.2.1").
func IsNewer(current, latest string) bool {
	return compareVersions(latest, current) > 0
}

// compareVersions returns -1/0/1 comparing a and b's MAJOR.MINOR.PATCH
// components numerically.
func compareVersions(a, b string) int {
	av := versionParts(a)
	bv := versionParts(b)
	for i := range 3 {
		switch {
		case av[i] < bv[i]:
			return -1
		case av[i] > bv[i]:
			return 1
		}
	}
	return 0
}

func versionParts(v string) [3]int {
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	var out [3]int
	for i, part := range strings.SplitN(v, ".", 3) {
		if i >= 3 {
			break
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			continue
		}
		out[i] = n
	}
	return out
}
