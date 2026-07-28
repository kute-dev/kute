package update

import "github.com/kute-dev/kute/internal/semver"

// IsNewer reports whether latest is a newer version than current. Both are
// plain "MAJOR.MINOR.PATCH[-pre]" strings with any leading "v" already
// stripped (Release.Version and kute's own build version are both kept in
// this form, though semver.Compare tolerates the prefix either way).
//
// The ordering itself lives in internal/semver, shared with the Helm chart
// index (§18a) — which needs real pre-release ordering, so this now has it
// too: running "0.2.0-rc.1" reads as older than a released "0.2.0" rather
// than equal to it. In practice GitHub's /releases/latest already excludes
// prereleases, so the two orderings only differ for a locally-built
// pre-release binary, where the new answer is the useful one.
func IsNewer(current, latest string) bool {
	return semver.IsNewer(current, latest)
}
