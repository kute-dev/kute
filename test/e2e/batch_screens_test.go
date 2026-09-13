//go:build e2e

package e2e

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestJobAttemptLedger covers §37b end to end. Before this, the Jobs list's
// `R` rerun was exercised (mutation_batch_test.go) but ↵ — the key that
// replaced the pre-v0.9.0 jump-to-Pods shortcut with the attempt ledger —
// was not pressed anywhere, so the whole screen was untested against a real
// cluster.
//
// The claim under test is the join: resources.BuildJobAttempts matches pods
// to their Job by *controller* ownerRef UID, and turns each into a numbered
// attempt with its own exit code. Nothing about that is provable against a
// fake — the ownerRef UIDs, the per-attempt pods and the exit statuses are
// all things only a real kubelet writes.
//
// fixtures/25-batch.yaml's attempts-fail exhausts backoffLimit 2 and the
// cluster script waits for its terminal Failed condition, so the ledger is
// complete by the time any test looks at it.
func TestJobAttemptLedger(t *testing.T) {
	RequireCluster(t)
	a := Launch(t)
	a.WaitFor("api-", Connect)

	a.gotoKind(t, "jobs", "Jobs")
	a.WaitLoaded(Settle)
	a.selectRow(t, "attempts-fail")
	a.Enter()
	a.WaitLoaded(Settle)

	// The breadcrumb's own segment, which no other screen renders — and the
	// honest fence that ↵ landed here rather than on the Pods list the old
	// shortcut went to.
	a.WaitForAll(Settle, "job/attempts-fail", "Attempts")

	// §37b's status banner and failure card. BackoffLimitExceeded is the
	// API's own terminal reason, so it appears both as the banner's suffix
	// and as the card's title.
	a.WaitForAll(Settle,
		"Failed · BackoffLimitExceeded",
		"attempts used",
		"no retries remain",
	)

	// The ledger itself: the flat attempt table (this Job has no
	// spec.completionMode, so it takes the table branch rather than §37d's
	// index grid). "Error" is the table branch's own word for a failed
	// attempt (resources.attemptResultCell) — §37d's index grid is the one
	// that says "failed", so asserting that word here would have been an
	// assertion the passing screen can never satisfy.
	a.WaitForAll(Settle, "POD", "RESULT", "EXIT", "Error")

	// Three pods ran, so three attempt rows have to be listed, each carrying
	// the exit code the container actually returned. Counting rows rather
	// than waiting for the ordinal "3" is what makes this mean anything — a
	// bare digit matches half the frame, while a join that matched on name
	// instead of controller UID, or that stopped at the first pod, lands
	// short here. Exit 3 is arbitrary in the fixture precisely so it cannot
	// be confused with the 0/1 a container produces by accident.
	a.waitForAttemptRows(t, "attempts-fail-", "3", 3)

	// The spec strip, which is where the backoffLimit the whole ledger is
	// measured against is stated.
	a.WaitFor("backoffLimit", Settle)

	// §37d's index grid must *not* be here: this Job is not Indexed, and the
	// two branches are one screen gated on spec.completionMode. IDX is the
	// index grid's own column header and appears nowhere else.
	a.Never("IDX", 2*time.Second)
}

// TestCronJobDetailScreen covers §36e, which had no e2e path at all — the
// CronJobs list's `S` (schedule editor) and `R` (run now) were both covered
// by mutation_batch_test.go, but ↵ was not.
//
// It runs against detail-cron rather than phase3-cron deliberately: the
// mutation suite rewrites phase3-cron's schedule and runs a manual Job
// against it, so a detail assertion sharing that fixture would read whatever
// the mutation test left behind, in whichever order the two happened to run.
func TestCronJobDetailScreen(t *testing.T) {
	RequireCluster(t)
	a := Launch(t)
	a.WaitFor("api-", Connect)

	a.gotoKind(t, "cronjobs", "CronJobs")
	a.WaitLoaded(Settle)
	a.selectRow(t, "detail-cron")
	a.Enter()
	a.WaitLoaded(Settle)

	// The facts grid, read straight off the object. The schedule is a value
	// no other fixture carries, so seeing it proves the screen is showing
	// *this* CronJob and not simply the list it was opened from.
	a.WaitForAll(Settle,
		"detail-cron",
		"17 4 * * 1",
		"LAST SCHEDULE",
	)

	// §36e's Jobs section. The heading is the load-bearing part: the wording
	// "associated with this cronjob" exists because a manual run is
	// *annotated* rather than owned, so the table cannot honestly claim
	// ownership — and the retention limits come from the object's own
	// history limits (3 and 1 here, chosen to differ from each other and
	// from the defaults so a hard-coded pair could not pass).
	a.WaitForWrapped("associated with this cronjob", Settle)
	a.WaitForWrapped("history limits 3 succeeded / 1 failed", Settle)

	// This CronJob is suspended and has never fired, so the aggregation has
	// to say so rather than inventing a run. That the screen settles at all
	// on a CronJob with zero Jobs is the anti-hang half of the same claim:
	// opening CronJobs starts Job's informer too, and a kind with genuinely
	// zero matching objects syncs without emitting a change event.
	a.NeverLoading(3 * time.Second)

	a.back(t, "CronJobs")
}

// waitForAttemptRows waits until exactly want rows of §37b's attempt table
// name a pod with the given prefix, each reporting "Error" — the flat
// table's own RESULT word for a failed attempt — with the given exit code.
//
// A row-shaped predicate rather than a substring: the ledger's own claim is
// per-attempt, so "the exit code is somewhere on screen" is not the same
// statement as "every attempt row carries it". Polled, because the join
// completes on a lazily-started Pod cache several event-loop turns after the
// screen is pushed.
func (a *App) waitForAttemptRows(t *testing.T, podPrefix, exitCode string, want int) {
	t.Helper()
	count := func(frame string) int {
		n := 0
		for _, line := range strings.Split(frame, "\n") {
			if !strings.Contains(line, "Error") {
				continue
			}
			for _, field := range strings.Fields(line) {
				if strings.HasPrefix(field, podPrefix) && len(field) > len(podPrefix) {
					if lineHasExactField(line, exitCode) {
						n++
					}
					break
				}
			}
		}
		return n
	}
	frame, ok := a.poll(func(f string) bool { return count(f) == want }, Settle)
	if !ok {
		t.Fatalf("wanted %d %q attempt rows with exit %s, saw %d; frame:\n%s",
			want, podPrefix, exitCode, count(frame), frame)
	}
}

// TestIndexedJobAttemptLedger covers §37d, the other half of the one screen
// §37b gates on spec.completionMode.
//
// TestJobAttemptLedger already asserts the index grid is *absent* on an
// ordinary Job; nothing asserted it appears on an Indexed one, so the branch
// that renders it had no e2e path at all. The grid is built from a join no
// fake performs: each pod carries its completion index in a label the
// kubelet-side controller writes, and resources.ProjectJobIndexGrid groups
// the Job's own pods by it.
//
// fixtures/25-batch.yaml's attempts-indexed completes all three indices and
// the cluster script waits for its Complete condition, so "3 of 3 finished"
// is settled before any test looks — "1 of 3, still going" and "3 of 3" are
// different screens, and polling cannot tell them apart.
func TestIndexedJobAttemptLedger(t *testing.T) {
	RequireCluster(t)
	a := Launch(t)
	a.WaitFor("api-", Connect)

	a.gotoKind(t, "jobs", "Jobs")
	a.WaitLoaded(Settle)
	a.selectRow(t, "attempts-indexed")
	a.Enter()
	a.WaitLoaded(Settle)

	a.WaitForAll(Settle, "job/attempts-indexed", "Attempts")

	// The Job succeeded, so the banner is the success reading and the
	// failure card must not be drawn over it.
	a.WaitFor("Succeeded", Settle)

	// §37d's grid: its own IDX column — the header TestJobAttemptLedger
	// asserts can never appear on a non-Indexed Job — and one row per
	// completion index, each reporting the grid branch's own word for a
	// finished index ("complete", where the flat table says "Error"/"OK").
	a.WaitForAll(Settle, "IDX", "RESULT", "ATTEMPTS")
	a.waitForIndexRows(t, 3, "complete")

	// The spec strip states what the grid is measured against; completions
	// is the Indexed-only half of it.
	a.WaitForAll(Settle, "backoffLimit", "completions")
}

// waitForIndexRows waits until the index grid lists indices 0..want-1, each
// carrying result.
//
// Row-shaped and index-addressed, for the same reason waitForAttemptRows is:
// §37d's claim is per-index, and a screen that rendered one index three
// times, or three indices with the wrong results, satisfies every substring
// check a flat assertion could make.
func (a *App) waitForIndexRows(t *testing.T, want int, result string) {
	t.Helper()
	seen := func(frame string) int {
		n := 0
		for idx := range want {
			for _, line := range strings.Split(frame, "\n") {
				if lineHasExactField(line, strconv.Itoa(idx)) && strings.Contains(line, result) {
					n++
					break
				}
			}
		}
		return n
	}
	frame, ok := a.poll(func(f string) bool { return seen(f) == want }, Settle)
	if !ok {
		t.Fatalf("wanted indices 0..%d each %q in the grid, saw %d; frame:\n%s", want-1, result, seen(frame), frame)
	}
}
