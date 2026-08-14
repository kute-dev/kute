package kube

import (
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/validation"
)

// manualJobNameMaxLen mirrors Kubernetes' DNS-1123 *label* length limit
// (63), not the 253-character DNS-1123 subdomain limit metadata.name itself
// would otherwise allow. A Job's own name becomes the "job-name"/
// "batch.kubernetes.io/job-name" *label value* stamped on every pod it
// creates, and label values are capped at 63 — a longer Job name creates
// successfully but then fails pod creation once the controller tries to
// stamp that label, so ManualJobName truncates to stay inside the tighter
// limit up front.
const manualJobNameMaxLen = validation.DNS1123LabelMaxLength

// manualJobNameMaxCollisions bounds ManualJobName's numeric-suffix search —
// a caller-supplied taken func that (pathologically) never returns false
// would otherwise loop forever. 9999 is generous for a same-minute
// collision count no real CronJob run-now workflow will ever reach; if
// every candidate up to it is somehow taken, the last one is returned
// rather than looping further, so the function always terminates.
const manualJobNameMaxCollisions = 9999

// ManualJobName builds a readable, DNS-safe, collision-safe target Job name
// for a manual CronJob run (§36b): "<cronJobName>-manual-HHMM" in UTC, with
// the smallest available numeric "-N" suffix appended when two runs land in
// the same minute. Pure and deterministic for a given (cronJobName, at,
// taken) — the caller (browse's ctrl-r preflight, Phase 4+) resolves the
// exact value once during action staging and reuses it for both the
// will-run preview and the actual TriggerCronJob call (Plan Phase 2 tasks
// 3-4), so preview and Create can never disagree.
//
// taken reports whether a candidate name is already in use — typically a
// closure over the caller's already-loaded Job cache for the CronJob's own
// namespace. A nil taken is treated as "nothing is taken" (every candidate
// accepted on the first try), which is only correct for a caller with no
// names to check against (e.g. a test exercising the truncation/format
// behavior alone).
func ManualJobName(cronJobName string, at time.Time, taken func(name string) bool) string {
	suffix := "-manual-" + at.UTC().Format("1504")
	base := dnsSafeTruncate(cronJobName, manualJobNameMaxLen-len(suffix)) + suffix
	if taken == nil || !taken(base) {
		return base
	}
	var candidate string
	for n := 2; n <= manualJobNameMaxCollisions; n++ {
		numberSuffix := fmt.Sprintf("-%d", n)
		candidate = dnsSafeTruncate(cronJobName, manualJobNameMaxLen-len(suffix)-len(numberSuffix)) + suffix + numberSuffix
		if !taken(candidate) {
			return candidate
		}
	}
	return candidate
}

// nextRerunNameMaxCollisions bounds NextRerunName's numeric-suffix search —
// same reasoning as manualJobNameMaxCollisions: a pathological taken func
// that never returns false must not loop forever.
const nextRerunNameMaxCollisions = 9999

// NextRerunName builds §37c's "create" rerun target name:
// "<jobName>-rerun-<N>" starting at N=1, DNS-safe truncated the same way
// ManualJobName is (a rerun name becomes a pod-template "job-name" label
// value too), with the smallest available N against taken. Unlike
// ManualJobName's HHMM-keyed suffix, a rerun has no natural time-of-day to
// key off — the ordinal itself is the whole point (37c's mockup:
// "migrate-schema-v42-rerun-1") — so this always starts at 1 and increments,
// rather than trying a bare suffix-less name first.
//
// Pure and deterministic for a given (jobName, taken); the caller (browse's
// ctrl-r preflight and jobattempts' own) resolves the value once during
// staging and reuses it for both the will-run preview and the actual
// RetryJob call, so the two can never disagree.
func NextRerunName(jobName string, taken func(name string) bool) string {
	if taken == nil {
		taken = func(string) bool { return false }
	}
	var candidate string
	for n := 1; n <= nextRerunNameMaxCollisions; n++ {
		suffix := fmt.Sprintf("-rerun-%d", n)
		candidate = dnsSafeTruncate(jobName, manualJobNameMaxLen-len(suffix)) + suffix
		if !taken(candidate) {
			return candidate
		}
	}
	return candidate
}

// dnsSafeTruncate trims s to at most maxLen runes-as-bytes (Job/CronJob
// names are already restricted to the DNS-1123 byte-per-char alphabet, so a
// byte-length truncation never splits a multi-byte rune) and strips any
// trailing '-' the cut could have introduced — a DNS-1123 label must both
// start and end with an alphanumeric character. maxLen <= 0 returns "".
func dnsSafeTruncate(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	if len(s) > maxLen {
		s = s[:maxLen]
	}
	return strings.TrimRight(s, "-")
}
