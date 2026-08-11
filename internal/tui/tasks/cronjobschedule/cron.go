// Pure cron/timezone analysis for 36d — no cluster I/O, no clock reads
// (every function here takes `now`/a schedule/timezone pair as parameters),
// mirroring resources/cronjobs.go's own "aggregation is pure, projection
// takes an explicit now" split. Kept package-local rather than added to
// resources.CronOccurrences or similar: this package's own client-side
// validation (parseSchedule/validateTimeZone) has to run before there's
// anything to Begin, the same reason the pre-Phase-6 browse/
// cronjobschedule.go imported robfig/cron/v3 directly rather than going
// through resources for it — and the year-long WHAT CHANGES enumeration is
// a concern this screen alone has.
package cronjobschedule

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

// parseSchedule validates schedule the same way Kubernetes' CronJob
// controller does — cron.ParseStandard: the standard five-field form plus
// '@' macros (@daily, @every 1h30m, ...).
func parseSchedule(schedule string) (cron.Schedule, error) {
	trimmed := strings.TrimSpace(schedule)
	if trimmed == "" {
		return nil, fmt.Errorf("schedule is required")
	}
	return cron.ParseStandard(trimmed)
}

// validateTimeZone loads tz via time.LoadLocation, with two Kubernetes-
// specific restrictions the CronJob API itself applies: "Local" is refused
// (unset already means "the controller's own local time" — an explicit
// "Local" string is never what a caller means) and a path attempting to
// escape the tzdata root is refused outright rather than handed to
// LoadLocation, which on some platforms resolves ".." components against
// the real filesystem rather than rejecting them. tz == "" is a valid
// "no zone selected" and returns (nil, nil) — callers distinguish "clear"
// from "invalid" themselves.
func validateTimeZone(tz string) (*time.Location, error) {
	if tz == "" {
		return nil, nil
	}
	if tz == "Local" {
		return nil, fmt.Errorf("%q is not a valid IANA time zone name", tz)
	}
	if strings.Contains(tz, "..") || strings.HasPrefix(tz, "/") {
		return nil, fmt.Errorf("invalid time zone %q", tz)
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, fmt.Errorf("invalid time zone %q: %w", tz, err)
	}
	return loc, nil
}

// scheduleFields is 36d's per-field MIN/HOUR/DOM/MONTH/DOW breakdown —
// always the schedule's own literal tokens, never reworded (task 5's "exact
// tokens"). Macro is set instead of the five fields for an '@'-prefixed
// schedule (@daily, @every 1h30m, ...), which has no field breakdown to
// show. A schedule that isn't exactly five space-separated fields and isn't
// a macro decodes to the zero value — reached only transiently, while the
// caller's own parseSchedule already reports it invalid elsewhere.
type scheduleFields struct {
	Macro                         string
	Minute, Hour, DOM, Month, DOW string
}

func decodeScheduleFields(schedule string) scheduleFields {
	trimmed := strings.TrimSpace(schedule)
	if strings.HasPrefix(trimmed, "@") {
		return scheduleFields{Macro: trimmed}
	}
	fields := strings.Fields(trimmed)
	if len(fields) != 5 {
		return scheduleFields{}
	}
	return scheduleFields{Minute: fields[0], Hour: fields[1], DOM: fields[2], Month: fields[3], DOW: fields[4]}
}

// isSimpleToken reports whether s is a single alphanumeric value with no
// cron operators (*, /, -, ,) — describeField/weekdayList's shared "plain
// enough to name in English" test.
func isSimpleToken(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		alnum := (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		if !alnum {
			return false
		}
	}
	return true
}

func isSimpleList(s string) bool {
	for part := range strings.SplitSeq(s, ",") {
		if !isSimpleToken(part) {
			return false
		}
	}
	return true
}

func isSimpleRange(s string) bool {
	parts := strings.SplitN(s, "-", 2)
	return len(parts) == 2 && isSimpleToken(parts[0]) && isSimpleToken(parts[1])
}

// describeField renders one field's conservative English reading (task 5):
// a plain sentence for the simple forms real CronJob schedules
// overwhelmingly use (wildcard, step, list, range), and the literal token
// itself for anything more elaborate (a list of ranges, a step off a
// range, …) — never a guessed sentence for a form this function doesn't
// specifically recognize.
func describeField(token string) string {
	switch {
	case token == "*":
		return "every"
	case strings.HasPrefix(token, "*/") && isSimpleToken(token[2:]):
		return "every " + token[2:]
	case strings.Contains(token, ",") && isSimpleList(token):
		return "at " + strings.Join(strings.Split(token, ","), ", ")
	case isSimpleRange(token):
		parts := strings.SplitN(token, "-", 2)
		return parts[0] + " through " + parts[1]
	case isSimpleToken(token):
		return token
	default:
		return token
	}
}

var weekdayNames = [7]string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}

var weekdayAbbrev = map[string]string{
	"SUN": "Sunday", "MON": "Monday", "TUE": "Tuesday", "WED": "Wednesday",
	"THU": "Thursday", "FRI": "Friday", "SAT": "Saturday",
}

func resolveWeekday(s string) (string, bool) {
	if n, err := strconv.Atoi(s); err == nil {
		if n == 7 {
			n = 0
		}
		if n < 0 || n > 6 {
			return "", false
		}
		return weekdayNames[n], true
	}
	if name, ok := weekdayAbbrev[strings.ToUpper(s)]; ok {
		return name, true
	}
	return "", false
}

// weekdayList renders a DOW field's conservative reading using weekday
// names when every value in it resolves cleanly (numeric 0-7 or the
// standard 3-letter abbreviations) — ok is false for anything it can't
// name with full confidence, and the caller falls back to describeField's
// plain-token reading rather than guess.
func weekdayList(token string) (string, bool) {
	switch {
	case isSimpleRange(token):
		parts := strings.SplitN(token, "-", 2)
		a, ok1 := resolveWeekday(parts[0])
		b, ok2 := resolveWeekday(parts[1])
		if !ok1 || !ok2 {
			return "", false
		}
		return a + " through " + b, true
	case strings.Contains(token, ","):
		names := make([]string, 0, strings.Count(token, ",")+1)
		for part := range strings.SplitSeq(token, ",") {
			n, ok := resolveWeekday(part)
			if !ok {
				return "", false
			}
			names = append(names, n)
		}
		return strings.Join(names, ", "), true
	case isSimpleToken(token):
		return resolveWeekday(token)
	default:
		return "", false
	}
}

func pad2(s string) string {
	if len(s) == 1 {
		return "0" + s
	}
	return s
}

// describeSchedule composes fields' conservative English description (task
// 5). Common shapes (a plain daily/weekly HH:MM) get a friendlier reading;
// everything else falls back to a per-field join — still exact, since every
// clause comes from describeField/weekdayList's own token-level readings,
// never a pattern guess. Empty for a macro schedule (the caller renders
// Macro directly) or an undecodable one (parseSchedule already reports that
// case as invalid elsewhere).
func describeSchedule(f scheduleFields) string {
	if f.Macro != "" || f.Minute == "" {
		return ""
	}
	if isSimpleToken(f.Minute) && isSimpleToken(f.Hour) && f.DOM == "*" && f.Month == "*" {
		at := pad2(f.Hour) + ":" + pad2(f.Minute)
		switch f.DOW {
		case "*":
			return "daily at " + at + " (schedule's own time zone)"
		default:
			if days, ok := weekdayList(f.DOW); ok {
				return "at " + at + " on " + days
			}
		}
	}
	parts := []string{
		"minute " + describeField(f.Minute),
		"hour " + describeField(f.Hour),
		"day of month " + describeField(f.DOM),
		"month " + describeField(f.Month),
		"day of week " + describeField(f.DOW),
	}
	return strings.Join(parts, "; ")
}

// comparisonWindow/comparisonCap bound 36d's WHAT CHANGES enumeration
// (task 7): a fixed upcoming one year, capped at comparisonCap occurrences
// per side so a sub-minute schedule (`* * * * *`) can't spin the analysis
// command forever — hitting the cap sets scheduleComparison.Truncated,
// which the view labels "at least"/"truncated" rather than an exact count.
const (
	comparisonWindow = 365 * 24 * time.Hour
	comparisonCap    = 5000
)

// scheduleComparison is compareFiringSets' result — WHAT CHANGES's added/
// removed counts, the annual delta, whether the very next occurrence moves,
// and whether either side's enumeration was capped.
type scheduleComparison struct {
	Unavailable    bool
	UnavailableWhy string
	Added          int
	Removed        int
	AnnualDelta    int
	NextChanges    bool
	Truncated      bool
}

// occurrences enumerates schedule's firings in timezone, strictly after
// from and up to to, capped at capN. timezone must be non-empty — an
// unset/"controller local" side is the caller's Unavailable branch, never
// reached here.
func occurrences(schedule, timezone string, from, to time.Time, capN int) ([]time.Time, bool, error) {
	sched, err := parseSchedule(schedule)
	if err != nil {
		return nil, false, err
	}
	loc, err := validateTimeZone(timezone)
	if err != nil {
		return nil, false, err
	}
	if loc == nil {
		return nil, false, fmt.Errorf("time zone is required for this comparison")
	}
	at := from.In(loc)
	toIn := to.In(loc)
	var out []time.Time
	truncated := false
	for {
		at = sched.Next(at)
		if at.IsZero() || at.After(toIn) {
			break
		}
		if len(out) >= capN {
			truncated = true
			break
		}
		out = append(out, at.UTC())
	}
	return out, truncated, nil
}

// compareFiringSets computes 36d's WHAT CHANGES: the old and new schedule/
// timezone's firing sets over the fixed upcoming comparisonWindow, from
// now. Exact only when both sides carry an explicit timezone (directly
// comparable once normalized to UTC); otherwise Unavailable — comparing a
// "controller local" firing set against anything else would be exactly the
// guess §3.9 already refuses to make for NEXT.
func compareFiringSets(oldSchedule, oldTZ, newSchedule, newTZ string, now time.Time) scheduleComparison {
	if oldTZ == "" || newTZ == "" {
		return scheduleComparison{
			Unavailable:    true,
			UnavailableWhy: "comparison unavailable — this schedule runs in the controller's own local time zone, which kute cannot discover",
		}
	}
	to := now.Add(comparisonWindow)
	oldTimes, oldTrunc, err1 := occurrences(oldSchedule, oldTZ, now, to, comparisonCap)
	newTimes, newTrunc, err2 := occurrences(newSchedule, newTZ, now, to, comparisonCap)
	if err1 != nil || err2 != nil {
		return scheduleComparison{Unavailable: true, UnavailableWhy: "comparison unavailable — invalid schedule or time zone"}
	}
	oldSet := make(map[int64]bool, len(oldTimes))
	for _, t := range oldTimes {
		oldSet[t.Unix()] = true
	}
	newSet := make(map[int64]bool, len(newTimes))
	for _, t := range newTimes {
		newSet[t.Unix()] = true
	}
	added, removed := 0, 0
	for k := range newSet {
		if !oldSet[k] {
			added++
		}
	}
	for k := range oldSet {
		if !newSet[k] {
			removed++
		}
	}
	nextChanges := len(oldTimes) == 0 || len(newTimes) == 0 || !oldTimes[0].Equal(newTimes[0])
	return scheduleComparison{
		Added:       added,
		Removed:     removed,
		AnnualDelta: len(newTimes) - len(oldTimes),
		NextChanges: nextChanges,
		Truncated:   oldTrunc || newTrunc,
	}
}
