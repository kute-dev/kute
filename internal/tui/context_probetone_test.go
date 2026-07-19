package tui

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui/components/palette"
)

const probeToneTestKubeconfig = `
apiVersion: v1
kind: Config
current-context: dev
contexts:
- name: dev
  context: {cluster: dev, namespace: default}
- name: prod
  context: {cluster: prod, namespace: prod}
clusters:
- name: dev
  cluster: {server: https://dev.example.invalid}
- name: prod
  cluster: {server: https://prod.example.invalid}
users: []
`

// TestProbeToneMatchesReachability pins 7a's STATUS column color (docs/design
// README.md §7a: "● 12ms green, ◌ probing… yellow, ✕ unreachable red") —
// contextItems previously left every row's RightTone at its zero value
// (ToneDefault), so probeStatus's text rendered in the same faint gray
// regardless of state; this is the fix's regression pin.
func TestProbeToneMatchesReachability(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		res  kube.ProbeResult
		want palette.Tone
	}{
		{"probing (zero value)", kube.ProbeResult{}, palette.ToneWarn},
		{"unreachable", kube.ProbeResult{Err: errors.New("dial timeout")}, palette.ToneBad},
		{"reachable", kube.ProbeResult{Name: "dev", Latency: 12 * time.Millisecond}, palette.ToneOK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := probeTone(c.res); got != c.want {
				t.Fatalf("probeTone(%+v) = %v, want %v", c.res, got, c.want)
			}
		})
	}
}

// TestContextItemsSetsRightTone covers the actual wiring (not just the pure
// probeTone helper): each row's STATUS column cell (palette.Item.Cols[1])
// must come from its own probe result, not the zero-value ToneDefault every
// row silently carried before this fix.
func TestContextItemsSetsRightTone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(probeToneTestKubeconfig), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", path)

	sess := &Session{Location: Location{Context: "dev"}}
	probes := map[string]kube.ProbeResult{
		"dev":  {Name: "dev", Latency: 5 * time.Millisecond},
		"prod": {Err: errors.New("unreachable")},
	}
	items := contextItems(sess, probes)

	byName := make(map[string]palette.Item, len(items))
	for _, it := range items {
		byName[it.Label] = it
	}
	if got := byName["dev"].Cols[1].Tone; got != palette.ToneOK {
		t.Fatalf("dev: STATUS tone = %v, want ToneOK", got)
	}
	if got := byName["prod"].Cols[1].Tone; got != palette.ToneBad {
		t.Fatalf("prod: STATUS tone = %v, want ToneBad", got)
	}
}

// TestKubeconfigPathAbbreviatesHome pins 7a's right hint text (docs/design
// README.md §7a: right hint `~/.kube/config · 5 contexts`) — the real
// kube.KubeconfigPath() returns an unabbreviated absolute path, which for
// any $KUBECONFIG override outside literally ~/.kube (this repo's own
// mise.toml points it at a repo-relative path) can be long enough that
// padBetweenStyled's hint-fit budget silently drops the whole right hint.
func TestKubeconfigPathAbbreviatesHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("KUBECONFIG", filepath.Join(home, "dev", "proj", ".kube", "config"))

	got := kubeconfigPath()
	want := filepath.Join("~", "dev", "proj", ".kube", "config")
	if got != want {
		t.Fatalf("kubeconfigPath() = %q, want %q", got, want)
	}
}
