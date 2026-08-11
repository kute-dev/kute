package cronjobschedule

import (
	"testing"
	"time"
)

func TestParseScheduleValidAndInvalid(t *testing.T) {
	t.Parallel()
	valid := []string{"*/5 * * * *", "0 2 * * *", "@daily", "@every 1h30m", "0 9-17 * * 1-5"}
	for _, s := range valid {
		if _, err := parseSchedule(s); err != nil {
			t.Errorf("parseSchedule(%q) = %v, want nil", s, err)
		}
	}
	invalid := []string{"", "not a cron expression", "60 * * * *", "* * * *"}
	for _, s := range invalid {
		if _, err := parseSchedule(s); err == nil {
			t.Errorf("parseSchedule(%q) = nil, want an error", s)
		}
	}
}

func TestValidateTimeZone(t *testing.T) {
	t.Parallel()
	if loc, err := validateTimeZone(""); err != nil || loc != nil {
		t.Errorf(`validateTimeZone("") = (%v, %v), want (nil, nil)`, loc, err)
	}
	if _, err := validateTimeZone("America/New_York"); err != nil {
		t.Errorf("validateTimeZone(America/New_York) = %v, want nil", err)
	}
	for _, bad := range []string{"Local", "Not/AZone", "../etc/passwd", "/etc/passwd"} {
		if _, err := validateTimeZone(bad); err == nil {
			t.Errorf("validateTimeZone(%q) = nil, want an error", bad)
		}
	}
}

func TestDecodeScheduleFields(t *testing.T) {
	t.Parallel()
	f := decodeScheduleFields("*/15 9-17 * * 1-5")
	want := scheduleFields{Minute: "*/15", Hour: "9-17", DOM: "*", Month: "*", DOW: "1-5"}
	if f != want {
		t.Errorf("decodeScheduleFields = %+v, want %+v", f, want)
	}
	macro := decodeScheduleFields("@daily")
	if macro.Macro != "@daily" {
		t.Errorf("decodeScheduleFields(@daily).Macro = %q, want @daily", macro.Macro)
	}
	zero := decodeScheduleFields("only two fields")
	if zero != (scheduleFields{}) {
		t.Errorf("decodeScheduleFields(malformed) = %+v, want the zero value", zero)
	}
}

func TestDescribeFieldWildcardStepListRange(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"*":     "every",
		"*/15":  "every 15",
		"3,4,5": "at 3, 4, 5",
		"1-5":   "1 through 5",
		"7":     "7",
	}
	for token, want := range cases {
		if got := describeField(token); got != want {
			t.Errorf("describeField(%q) = %q, want %q", token, got, want)
		}
	}
}

func TestWeekdayListNumericRangeListAndAbbrev(t *testing.T) {
	t.Parallel()
	if got, ok := weekdayList("1-5"); !ok || got != "Monday through Friday" {
		t.Errorf("weekdayList(1-5) = (%q, %v), want (Monday through Friday, true)", got, ok)
	}
	if got, ok := weekdayList("MON,WED,FRI"); !ok || got != "Monday, Wednesday, Friday" {
		t.Errorf("weekdayList(MON,WED,FRI) = (%q, %v)", got, ok)
	}
	if got, ok := weekdayList("0"); !ok || got != "Sunday" {
		t.Errorf("weekdayList(0) = (%q, %v), want Sunday", got, ok)
	}
	if got, ok := weekdayList("7"); !ok || got != "Sunday" {
		t.Errorf("weekdayList(7) = (%q, %v), want Sunday (7 aliases 0)", got, ok)
	}
	if _, ok := weekdayList("*/2"); ok {
		t.Errorf("weekdayList(*/2) = ok, want false for a step expression")
	}
}

func TestDescribeScheduleFriendlyAndFallback(t *testing.T) {
	t.Parallel()
	daily := describeSchedule(decodeScheduleFields("0 2 * * *"))
	if daily != "daily at 02:00 (schedule's own time zone)" {
		t.Errorf("describeSchedule(daily) = %q", daily)
	}
	weekday := describeSchedule(decodeScheduleFields("30 9 * * 1-5"))
	if weekday != "at 09:30 on Monday through Friday" {
		t.Errorf("describeSchedule(weekday) = %q", weekday)
	}
	fallback := describeSchedule(decodeScheduleFields("*/5 * 1,15 * *"))
	if fallback == "" || fallback == daily {
		t.Errorf("describeSchedule(complex) = %q, want a non-empty per-field fallback", fallback)
	}
}

func TestOccurrencesAcrossDSTSpringForward(t *testing.T) {
	t.Parallel()
	// America/New_York springs forward at 2026-03-08 02:00 -> 03:00 local:
	// the wall-clock instant 02:30 never happens that day. A daily 02:30
	// schedule's own Next() therefore skips 03-08 outright rather than
	// falling back to some nearby time — this pins that DST-aware behavior
	// (a naive fixed-UTC-offset add would instead land on a real, wrong
	// instant every day including the transition).
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	from := time.Date(2026, 3, 7, 12, 0, 0, 0, loc)
	to := time.Date(2026, 3, 10, 0, 0, 0, 0, loc)
	times, truncated, err := occurrences("30 2 * * *", "America/New_York", from, to, 10)
	if err != nil {
		t.Fatalf("occurrences: %v", err)
	}
	if truncated {
		t.Fatalf("expected no truncation for a 3-day window")
	}
	if len(times) != 1 {
		t.Fatalf("expected exactly 1 occurrence (03-08's 02:30 doesn't exist), got %d: %v", len(times), times)
	}
	if got := times[0].In(loc); got.Day() != 9 || got.Hour() != 2 || got.Minute() != 30 {
		t.Errorf("expected 2026-03-09 02:30 local, got %v", got)
	}
	// A schedule the transition doesn't touch (09:00, well clear of the
	// 02:00-03:00 gap) fires normally on both days — the contrast that
	// proves the single result above is the DST gap, not an off-by-one in
	// the window itself.
	unaffected, _, err := occurrences("0 9 * * *", "America/New_York", from, to, 10)
	if err != nil {
		t.Fatalf("occurrences (unaffected): %v", err)
	}
	if len(unaffected) != 2 {
		t.Fatalf("expected 2 occurrences for a schedule the DST gap doesn't touch, got %d: %v", len(unaffected), unaffected)
	}
}

func TestOccurrencesTruncatesAtCap(t *testing.T) {
	t.Parallel()
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)
	times, truncated, err := occurrences("* * * * *", "UTC", from, to, 5)
	if err != nil {
		t.Fatalf("occurrences: %v", err)
	}
	if !truncated || len(times) != 5 {
		t.Fatalf("expected truncation at 5 occurrences, got truncated=%v len=%d", truncated, len(times))
	}
}

func TestCompareFiringSetsUnavailableWithoutBothTimezones(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := compareFiringSets("0 2 * * *", "", "0 3 * * *", "UTC", now)
	if !c.Unavailable {
		t.Fatalf("expected Unavailable when the old side has no explicit time zone")
	}
	c2 := compareFiringSets("0 2 * * *", "UTC", "0 3 * * *", "", now)
	if !c2.Unavailable {
		t.Fatalf("expected Unavailable when the new side has no explicit time zone")
	}
}

func TestCompareFiringSetsRemovedWeekendsAddedRunsAndNextChanges(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) // a Thursday
	// Old: every day at 09:00. New: weekdays only at 09:00 — every Sat/Sun
	// occurrence in the window is removed, nothing is added, and since
	// today (Thursday) still fires under both, the very next occurrence is
	// unchanged.
	c := compareFiringSets("0 9 * * *", "UTC", "0 9 * * 1-5", "UTC", now)
	if c.Unavailable {
		t.Fatalf("expected an available comparison, got Unavailable: %s", c.UnavailableWhy)
	}
	if c.Removed == 0 {
		t.Errorf("expected removed occurrences for the weekend-dropping edit, got 0")
	}
	if c.Added != 0 {
		t.Errorf("expected zero added occurrences (new is a strict subset of old), got %d", c.Added)
	}
	if c.AnnualDelta >= 0 {
		t.Errorf("expected a negative annual delta (fewer runs/year), got %d", c.AnnualDelta)
	}
	if c.NextChanges {
		t.Errorf("expected the next occurrence (today, a weekday) to stay unchanged")
	}
}

func TestCompareFiringSetsNextChangesOnTimezoneOnlyEdit(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := compareFiringSets("0 9 * * *", "UTC", "0 9 * * *", "America/New_York", now)
	if c.Unavailable {
		t.Fatalf("expected an available comparison, got Unavailable: %s", c.UnavailableWhy)
	}
	if !c.NextChanges {
		t.Errorf("expected a timezone-only edit to move every absolute occurrence, including the next one")
	}
}

func TestCompareFiringSetsInvalidScheduleIsUnavailable(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := compareFiringSets("not a schedule", "UTC", "0 9 * * *", "UTC", now)
	if !c.Unavailable {
		t.Fatalf("expected an invalid old schedule to make the comparison Unavailable")
	}
}

func TestOccurrencesRequiresTimeZone(t *testing.T) {
	t.Parallel()
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	_, _, err := occurrences("0 9 * * *", "", from, from.Add(time.Hour), 10)
	if err == nil {
		t.Fatalf("expected occurrences to require a time zone")
	}
}
