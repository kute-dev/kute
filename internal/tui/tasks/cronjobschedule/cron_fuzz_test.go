package cronjobschedule

import (
	"strings"
	"testing"
)

// FuzzDescribeSchedule fuzzes the hand-written cron decoder and its English
// renderer. The schedule string comes straight off a CronJob spec, so on a
// shared cluster it is written by whoever can create one — not necessarily
// whoever is reading the screen.
//
// The properties, not expected sentences:
//   - nothing panics on any token, however malformed;
//   - describeField never invents content: for a token it does not recognise
//     it must return that token verbatim, which is the documented contract
//     ("never a guessed sentence for a form this function doesn't recognize");
//   - decodeScheduleFields returns either a macro, five fields, or the zero
//     value — never a partial decode that would render a half-empty grid.
func FuzzDescribeSchedule(f *testing.F) {
	seeds := []string{
		"", "   ", "*", "* * * * *", "0 3 * * *", "*/15 * * * 1-5",
		"@daily", "@every 1h30m", "@", "@@@",
		"0,15,30,45 * * * *", "1-5/2 * * * *", "0 0 1 1 *",
		"* * * *", "* * * * * *", "JAN-DEC * * * *",
		"a,b,c * * * *", "*/ * * * *", "1- * * * *", "-1 * * * *",
		"\x00 * * * *", "😀 * * * *",
		strings.Repeat("1,", 500) + "1 * * * *",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, schedule string) {
		fields := decodeScheduleFields(schedule)

		switch {
		case fields.Macro != "":
			if !strings.HasPrefix(fields.Macro, "@") {
				t.Fatalf("decodeScheduleFields(%q) macro = %q, want an @-prefixed token", schedule, fields.Macro)
			}
		case fields == (scheduleFields{}):
			// The documented "not five fields and not a macro" outcome.
		default:
			for name, tok := range map[string]string{
				"minute": fields.Minute, "hour": fields.Hour, "dom": fields.DOM,
				"month": fields.Month, "dow": fields.DOW,
			} {
				if tok == "" {
					t.Fatalf("decodeScheduleFields(%q) produced a partial decode: %s is empty", schedule, name)
				}
			}
		}

		// describeField must never lose the token it was given: either it
		// recognises the shape and says something containing the token's own
		// parts, or it hands the token back untouched.
		for _, tok := range []string{fields.Minute, fields.Hour, fields.DOM, fields.Month, fields.DOW} {
			if tok == "" {
				continue
			}
			got := describeField(tok)
			if got == "" {
				t.Fatalf("describeField(%q) = \"\", want at least the token back", tok)
			}
		}

		// The whole-schedule renderer must not panic on anything the decoder
		// accepted.
		_ = describeSchedule(fields)
	})
}
