package podlogs

import (
	"strings"
	"testing"
)

// FuzzParseLine fuzzes the two functions that run over raw container stdout —
// the most genuinely untrusted byte stream kute touches. Anything a workload
// can print reaches these, including partial UTF-8, control bytes and lines
// engineered to look like a timestamp or a level field.
//
// The properties, rather than expected outputs:
//   - neither function panics, on any input;
//   - parseSeverity only ever returns one of the three known severities or "";
//   - splitTimestamp never invents or loses message bytes — the message it
//     returns is always a suffix of the line, and when it reports no timestamp
//     the line comes back exactly as it went in.
//
// That last one is the real invariant: a log viewer that silently drops part
// of a line is worse than one that fails to colour it.
func FuzzParseLine(f *testing.F) {
	seeds := []string{
		"",
		"plain message with no level",
		`{"level":"error","msg":"boom"}`,
		`{"level":"WaRn","msg":"mixed case"}`,
		"level=info component=api starting",
		"2024-01-02T03:04:05.123456789Z the message",
		"2024-01-02T03:04:05+01:00 offset timestamp",
		"2024-01-02T03:04:05Z",          // timestamp with no message
		"9999-99-99T99:99:99Z bad date", // matches the shape, not a real time
		"ERROR at the start",
		"contains ERRORS but not the bare token",
		"\x00\x1b[31mANSI and NUL\x1b[0m",
		"\xff\xfe invalid utf-8",
		strings.Repeat("A", 4096),
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, line string) {
		sev := parseSeverity(line)
		switch sev {
		case "", SeverityErr, SeverityWarn, SeverityInfo:
		default:
			t.Fatalf("parseSeverity(%q) = %q, not a known severity", line, sev)
		}

		ts, msg := splitTimestamp(line)
		if ts == "" {
			if msg != line {
				t.Fatalf("splitTimestamp(%q) reported no timestamp but changed the line to %q", line, msg)
			}
			return
		}
		if len(ts) != len("15:04:05") {
			t.Fatalf("splitTimestamp(%q) timestamp = %q, want HH:MM:SS", line, ts)
		}
		if !strings.HasSuffix(line, msg) {
			t.Fatalf("splitTimestamp(%q) message %q is not a suffix of the line — bytes were lost or invented", line, msg)
		}
	})
}
