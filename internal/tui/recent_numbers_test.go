package tui

import (
	"testing"

	"github.com/kute-dev/kute/internal/tui/components/palette"
)

// TestOtherRecentsExcludesCurrent pins the shared "recents minus current"
// list mostRecentOther (the alt-tab target) and numberedRecents both build
// on.
func TestOtherRecentsExcludesCurrent(t *testing.T) {
	t.Parallel()
	got := otherRecents([]string{"default", "prod", "stage", "prod"}, "default")
	want := []string{"prod", "stage", "prod"}
	if len(got) != len(want) {
		t.Fatalf("otherRecents() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("otherRecents()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestNumberedRecentsExcludesCurrentAndPrevious pins the numbered pick's
// pool (docs/design README.md §6a/§7a): current and the immediately-previous
// entry (mostRecentOther's alt-tab target) are both dropped — they already
// have their own on-row tag ("current"/"previous"), so a digit for either
// would be redundant. Digit "1" is whatever comes after "previous".
func TestNumberedRecentsExcludesCurrentAndPrevious(t *testing.T) {
	t.Parallel()
	recents := []string{"current", "previous", "r1", "r2"}
	got := numberedRecents(recents, "current")
	want := []string{"r1", "r2"}
	if len(got) != len(want) {
		t.Fatalf("numberedRecents() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("numberedRecents()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestRecentNumbersExcludesCurrentAndPreviousCapsAtNine pins 6a/7a's
// numbered RECENT-row gutter: neither current nor previous ever gets a
// digit, the first entry *after* previous is always digit 1, and only 1-9
// are addressable from the keyboard.
func TestRecentNumbersExcludesCurrentAndPreviousCapsAtNine(t *testing.T) {
	t.Parallel()
	recents := []string{"current", "previous", "r1", "r2", "r3", "r4", "r5", "r6", "r7", "r8", "r9", "r10"}
	got := recentNumbers(recents, "current")

	if _, ok := got["current"]; ok {
		t.Fatalf("expected current to have no assigned digit, got %v", got)
	}
	if _, ok := got["previous"]; ok {
		t.Fatalf("expected previous (mostRecentOther's target) to have no assigned digit, got %v", got)
	}
	for i := 1; i <= 9; i++ {
		name := recents[i+1] // +1 to skip past "previous" at index 1
		if got[name] != i {
			t.Fatalf("recentNumbers()[%q] = %d, want %d", name, got[name], i)
		}
	}
	if _, ok := got["r10"]; ok {
		t.Fatalf("expected the 11th recents entry to have no digit (only 1-9 are addressable), got %v", got)
	}
}

// TestDigitRecentTargetAgreesWithRecentNumbers pins that digitRecentTarget
// (the query-digit lookup) and recentNumbers (the row gutter) always resolve
// the same digit to the same target, given the same numberedRecents list —
// a user pressing '2' must land on the exact row showing a gutter "2".
func TestDigitRecentTargetAgreesWithRecentNumbers(t *testing.T) {
	t.Parallel()
	recents := []string{"default", "prod", "stage", "qa"}
	others := numberedRecents(recents, "default")
	nums := recentNumbers(recents, "default")

	for name, n := range nums {
		got, ok := digitRecentTarget(itoa(n), others)
		if !ok || got != name {
			t.Fatalf("digitRecentTarget(%d) = (%q, %v), want (%q, true)", n, got, ok, name)
		}
	}
}

func itoa(n int) string {
	return string(rune('0' + n))
}

// TestPromoteRecentItemsOrdersCurrentPreviousThenDigits pins the "recent
// namespaces/contexts float to the top" behavior: current leads, then the
// row tagged "previous", then the numbered 1-9 recents in digit order, and
// everything else keeps its original relative order after that.
func TestPromoteRecentItemsOrdersCurrentPreviousThenDigits(t *testing.T) {
	t.Parallel()
	items := []palette.Item{
		{Label: "zzz-plain-a"},
		{Label: "digit-3", RecentNum: 3},
		{Label: "previous", Tag: "previous"},
		{Label: "aaa-plain-b"},
		{Label: "digit-1", RecentNum: 1},
		{Label: "current", Tag: "current"},
		{Label: "digit-2", RecentNum: 2},
	}
	promoteRecentItems(items)

	want := []string{"current", "previous", "digit-1", "digit-2", "digit-3", "zzz-plain-a", "aaa-plain-b"}
	if len(items) != len(want) {
		t.Fatalf("promoteRecentItems() = %d items, want %d", len(items), len(want))
	}
	for i, label := range want {
		if items[i].Label != label {
			t.Fatalf("promoteRecentItems()[%d].Label = %q, want %q (full order: %v)", i, items[i].Label, label, itemLabels(items))
		}
	}
}

func itemLabels(items []palette.Item) []string {
	labels := make([]string, len(items))
	for i, it := range items {
		labels[i] = it.Label
	}
	return labels
}
