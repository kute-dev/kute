package podlogs

import "testing"

func TestParseSeverity(t *testing.T) {
	t.Parallel()

	cases := []struct {
		line string
		want string
	}{
		{`{"level":"error","msg":"boom"}`, SeverityErr},
		{`{"level":"warn","msg":"careful"}`, SeverityWarn},
		{`{"level":"info","msg":"ok"}`, SeverityInfo},
		{"time=10:00 level=WARN msg=disk", SeverityWarn},
		{"time=10:00 level=fatal msg=oops", SeverityErr},
		{"2026-01-01 ERROR something broke", SeverityErr},
		{"2026-01-01 WRN queue backing up", SeverityWarn},
		{"2026-01-01 INF starting server", SeverityInfo},
		{"just a plain message", ""},
	}
	for _, c := range cases {
		if got := parseSeverity(c.line); got != c.want {
			t.Errorf("parseSeverity(%q) = %q, want %q", c.line, got, c.want)
		}
	}
}

func TestSplitTimestamp(t *testing.T) {
	t.Parallel()

	ts, msg := splitTimestamp("2026-01-15T10:24:02.123456789Z starting up")
	if ts != "10:24:02" || msg != "starting up" {
		t.Fatalf("ts=%q msg=%q", ts, msg)
	}

	ts, msg = splitTimestamp("no timestamp here")
	if ts != "" || msg != "no timestamp here" {
		t.Fatalf("ts=%q msg=%q, want passthrough", ts, msg)
	}
}
