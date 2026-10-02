package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kute-dev/kute/internal/config"
	"github.com/kute-dev/kute/internal/testutil/testenv"
	"github.com/kute-dev/kute/internal/tui"
)

// TestLogConfigLoadRecordsBrokenConfig: a config.yaml that failed to parse
// reaches the diagnostics stream with the parser's reason, so a crash
// report or --log-file says why every context turned PROD.
func TestLogConfigLoadRecordsBrokenConfig(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := os.MkdirAll(filepath.Dir(config.Path()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.Path(), []byte("prodContexts: [unterminated"), 0o644); err != nil {
		t.Fatal(err)
	}
	sink := newTestSink(t, newLiveState(nil, false))

	logConfigLoad(sink, &tui.Session{Config: config.Load()})

	recent := strings.Join(sink.Recent(), "\n")
	if !strings.Contains(recent, config.Path()) || !strings.Contains(recent, "treating every context as PROD") {
		t.Fatalf("expected the config parse error in the diag stream, got:\n%s", recent)
	}
}

func TestLogConfigLoadQuietForCleanConfig(t *testing.T) {
	sink := newTestSink(t, newLiveState(nil, false))
	logConfigLoad(sink, &tui.Session{})
	if got := sink.Recent(); len(got) != 0 {
		t.Fatalf("expected nothing logged for a clean config, got %q", got)
	}
}
