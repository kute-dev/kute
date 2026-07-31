package diag

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixedNow() func() time.Time {
	t := time.Date(2026, 7, 31, 15, 30, 12, 0, time.UTC)
	return func() time.Time { return t }
}

func TestOpenWritesLogFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "kute.log")
	sink, err := Open(Options{LogFile: path, Now: fixedNow()})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	sink.Logf("hello %s", "world")
	if _, err := sink.Write([]byte("E0731 reflector: watch failed\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	got := string(data)
	for _, want := range []string{"kute hello world", "reflector: watch failed", "0731 15:30:12.000"} {
		if !strings.Contains(got, want) {
			t.Errorf("log file missing %q; got:\n%s", want, got)
		}
	}
}

// A run with no --log-file must still be diagnosable: the same lines land in
// the ring buffer, which is what a crash report reads.
func TestWriteWithoutLogFileFillsRing(t *testing.T) {
	sink, err := Open(Options{Now: fixedNow()})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if sink.Path() != "" {
		t.Errorf("Path() = %q, want empty without --log-file", sink.Path())
	}
	sink.Logf("connection reconnecting")

	recent := sink.Recent()
	if len(recent) != 1 || !strings.Contains(recent[0], "connection reconnecting") {
		t.Fatalf("Recent() = %#v, want one line naming the connection", recent)
	}
}

// klog writes a line per call today, but nothing in its contract says so —
// a chunk that splits mid-line must not produce two ring entries.
func TestWriteReassemblesPartialLines(t *testing.T) {
	sink, _ := Open(Options{})
	_, _ = sink.Write([]byte("W0731 throttl"))
	if got := sink.Recent(); len(got) != 0 {
		t.Fatalf("Recent() = %#v, want nothing until the line is terminated", got)
	}
	_, _ = sink.Write([]byte("ing request\nnext line\n"))

	want := []string{"W0731 throttling request", "next line"}
	got := sink.Recent()
	if len(got) != len(want) {
		t.Fatalf("Recent() = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Recent()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestRingIsBounded(t *testing.T) {
	sink, _ := Open(Options{})
	for i := range ringLines * 2 {
		sink.Logf("line %d", i)
	}
	recent := sink.Recent()
	if len(recent) != ringLines {
		t.Fatalf("len(Recent()) = %d, want %d", len(recent), ringLines)
	}
	if !strings.HasSuffix(recent[len(recent)-1], "line 599") {
		t.Errorf("last buffered line = %q, want the most recent one", recent[len(recent)-1])
	}
	if strings.HasSuffix(recent[0], "line 0") {
		t.Error("oldest line survived; the ring is not dropping anything")
	}
}

// An explicit --log-file that can't be opened is a startup error, not a
// silent downgrade: a user who asked for a log and got none has no way to
// tell.
func TestOpenFailsOnUnwritableLogFile(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(Options{LogFile: filepath.Join(blocker, "kute.log")}); err == nil {
		t.Fatal("Open() = nil error, want a failure for an unwritable path")
	}
}

func TestNilSinkIsUsable(t *testing.T) {
	var sink *Sink
	sink.Logf("no destination at all")
	if got := sink.Report(Crash{Cause: "update", Value: "boom"}); got != "" {
		t.Errorf("Report() on a nil Sink = %q, want empty", got)
	}
	if got := sink.Footer(""); got != "" {
		t.Errorf("Footer() on a nil Sink = %q, want empty", got)
	}
	if err := sink.Close(); err != nil {
		t.Errorf("Close() on a nil Sink = %v, want nil", err)
	}
}

func TestBuildString(t *testing.T) {
	for _, tc := range []struct {
		name  string
		build Build
		want  string
	}{
		{"full", Build{Version: "0.3.0", Commit: "abc1234", Date: "2026-07-28"}, "0.3.0 (abc1234, 2026-07-28)"},
		{"commit only", Build{Version: "0.3.0", Commit: "abc1234"}, "0.3.0 (abc1234)"},
		{"version only", Build{Version: "0.3.0"}, "0.3.0"},
		{"go run", Build{}, "dev"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.build.String(); got != tc.want {
				t.Errorf("String() = %q, want %q", got, tc.want)
			}
		})
	}
}
