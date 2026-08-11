package kube

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/util/validation"
)

// TestManualJobNameFormatsUTCHHMM pins the readable "<cronJobName>-manual-
// HHMM" format (§36b, Plan Phase 2 task 3, test item 5).
func TestManualJobNameFormatsUTCHHMM(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 3, 4, 2, 30, 0, 0, time.UTC)
	got := ManualJobName("nightly", at, nil)
	want := "nightly-manual-0230"
	if got != want {
		t.Fatalf("ManualJobName = %q, want %q", got, want)
	}
}

// TestManualJobNameConvertsToUTC pins that the HHMM component is always
// UTC, regardless of the input time.Time's own location.
func TestManualJobNameConvertsToUTC(t *testing.T) {
	t.Parallel()
	loc := time.FixedZone("UTC-5", -5*60*60)
	at := time.Date(2026, 3, 4, 21, 30, 0, 0, loc) // 21:30-05:00 == 02:30Z
	got := ManualJobName("nightly", at, nil)
	want := "nightly-manual-0230"
	if got != want {
		t.Fatalf("ManualJobName = %q, want %q", got, want)
	}
}

// TestManualJobNameTruncatesLongCronJobNames pins the DNS-1123-label
// (63-char) truncation: the result must always be a valid label, and must
// never end with '-' even when truncation lands mid-CronJob-name.
func TestManualJobNameTruncatesLongCronJobNames(t *testing.T) {
	t.Parallel()
	longName := strings.Repeat("a", 80)
	at := time.Date(2026, 3, 4, 2, 30, 0, 0, time.UTC)
	got := ManualJobName(longName, at, nil)
	if len(got) > 63 {
		t.Fatalf("ManualJobName length = %d, want <= 63: %q", len(got), got)
	}
	if !strings.HasSuffix(got, "-manual-0230") {
		t.Fatalf("ManualJobName = %q, want a -manual-0230 suffix", got)
	}
	if strings.HasSuffix(strings.TrimSuffix(got, "-manual-0230"), "-") {
		t.Fatalf("ManualJobName = %q, truncated cronjob-name prefix ends with '-'", got)
	}
	if errs := validation.IsDNS1123Label(got); len(errs) != 0 {
		t.Fatalf("ManualJobName = %q is not a valid DNS-1123 label: %v", got, errs)
	}
}

// TestManualJobNamePicksSmallestAvailableSuffix pins the same-minute
// collision contract: the smallest unused numeric suffix wins, and the
// result stays deterministic for the same (name, at, taken) triple.
func TestManualJobNamePicksSmallestAvailableSuffix(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 3, 4, 2, 30, 0, 0, time.UTC)
	existing := map[string]bool{
		"nightly-manual-0230":   true,
		"nightly-manual-0230-2": true,
	}
	taken := func(name string) bool { return existing[name] }

	got := ManualJobName("nightly", at, taken)
	want := "nightly-manual-0230-3"
	if got != want {
		t.Fatalf("ManualJobName = %q, want %q", got, want)
	}

	// Deterministic: calling again with the same taken set returns the same
	// candidate (it doesn't "reserve" a name as a side effect).
	got2 := ManualJobName("nightly", at, taken)
	if got2 != want {
		t.Fatalf("ManualJobName second call = %q, want the same %q", got2, want)
	}
}

// TestManualJobNameSuffixStaysDNSSafeUnderTruncation pins that a long
// CronJob name plus a multi-digit collision suffix still produces a valid
// DNS-1123 label (the suffix eats further into the truncation budget).
func TestManualJobNameSuffixStaysDNSSafeUnderTruncation(t *testing.T) {
	t.Parallel()
	longName := strings.Repeat("b", 80)
	at := time.Date(2026, 3, 4, 2, 30, 0, 0, time.UTC)
	existing := map[string]bool{}
	taken := func(name string) bool { return existing[name] }
	// Force ten same-minute collisions in a row, checking every candidate
	// along the way — including the double-digit suffix once past "-9".
	for i := 0; i < 10; i++ {
		name := ManualJobName(longName, at, taken)
		if errs := validation.IsDNS1123Label(name); len(errs) != 0 {
			t.Fatalf("ManualJobName = %q is not a valid DNS-1123 label: %v", name, errs)
		}
		existing[name] = true
	}
}

// TestManualJobNameExhaustsCollisionsAndReturnsLastCandidate pins
// manualJobNameMaxCollisions' termination guarantee: a pathological taken
// func that never returns false must not loop forever — ManualJobName gives
// up at the bound and returns the last candidate it tried (numeric suffix
// manualJobNameMaxCollisions), rather than hanging the caller.
func TestManualJobNameExhaustsCollisionsAndReturnsLastCandidate(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 3, 4, 2, 30, 0, 0, time.UTC)
	taken := func(string) bool { return true } // nothing is ever available

	got := ManualJobName("nightly", at, taken)
	want := fmt.Sprintf("nightly-manual-0230-%d", manualJobNameMaxCollisions)
	if got != want {
		t.Fatalf("ManualJobName = %q, want the exhausted bound %q", got, want)
	}
}
