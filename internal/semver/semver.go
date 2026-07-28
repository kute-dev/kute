// Package semver is kute's version-string comparator: the ordering both
// kute's own release check (internal/update, §28a/28b) and Helm chart
// freshness (internal/helmrepo, §18a) need. It is a leaf package — no
// dependency on anything else in the tree — so both callers can share one
// implementation of "is this newer than that" instead of keeping their own.
//
// Parsing is deliberately tolerant rather than strict: a malformed or
// missing component reads as 0 and an unparseable string never panics, it
// just orders as "no newer than anything sensible". Chart repositories and
// git tags both contain versions that don't quite validate, and a browse
// path must not care.
package semver

import (
	"strconv"
	"strings"
)

// IsNewer reports whether latest is a newer version than current.
func IsNewer(current, latest string) bool {
	return Compare(latest, current) > 0
}

// Compare returns -1, 0 or 1 as a orders before, equal to, or after b.
//
// MAJOR.MINOR.PATCH compare numerically, then SemVer 2.0's pre-release
// rules apply: a version carrying a pre-release suffix sorts *below* the
// same MAJOR.MINOR.PATCH without one ("1.2.3-rc.1" < "1.2.3"), and two
// pre-releases compare identifier by identifier. Build metadata ("+sha")
// is ignored entirely, as the spec requires.
func Compare(a, b string) int {
	acore, apre := parse(a)
	bcore, bpre := parse(b)
	for i := range 3 {
		switch {
		case acore[i] < bcore[i]:
			return -1
		case acore[i] > bcore[i]:
			return 1
		}
	}
	return comparePrerelease(apre, bpre)
}

// IsPrerelease reports whether v carries a pre-release suffix — the
// distinction `helm search repo` draws when it offers a chart's newest
// version, and kute's chart index draws for the same reason.
func IsPrerelease(v string) bool {
	_, pre := parse(v)
	return pre != ""
}

// parse splits v into its three numeric components and its pre-release
// suffix ("" when there is none). Any leading "v" and any build metadata
// are dropped first.
func parse(v string) ([3]int, string) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	pre := ""
	if i := strings.IndexByte(v, '-'); i >= 0 {
		v, pre = v[:i], v[i+1:]
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
	return out, pre
}

// comparePrerelease applies SemVer 2.0 §11.3/§11.4 to two pre-release
// suffixes: absent outranks present; otherwise dot-separated identifiers
// compare left to right, numerically when both are numeric, and a numeric
// identifier always ranks below an alphanumeric one. A longer identifier
// list wins when every shared identifier is equal.
func comparePrerelease(a, b string) int {
	switch {
	case a == "" && b == "":
		return 0
	case a == "":
		return 1
	case b == "":
		return -1
	}
	aparts := strings.Split(a, ".")
	bparts := strings.Split(b, ".")
	for i := range min(len(aparts), len(bparts)) {
		if c := compareIdentifier(aparts[i], bparts[i]); c != 0 {
			return c
		}
	}
	switch {
	case len(aparts) < len(bparts):
		return -1
	case len(aparts) > len(bparts):
		return 1
	}
	return 0
}

func compareIdentifier(a, b string) int {
	an, aErr := strconv.Atoi(a)
	bn, bErr := strconv.Atoi(b)
	switch {
	case aErr == nil && bErr == nil:
		return cmpInt(an, bn)
	case aErr == nil:
		return -1 // numeric identifiers always rank below alphanumeric ones
	case bErr == nil:
		return 1
	}
	return strings.Compare(a, b)
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
