package tui

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/state"
	"github.com/kute-dev/kute/internal/testutil/testenv"
)

const reprobeTestKubeconfig = `
apiVersion: v1
kind: Config
current-context: dev
contexts:
- name: dev
  context: {cluster: dev, namespace: default}
- name: prod-eks
  context: {cluster: prod, namespace: prod}
- name: pod-lab
  context: {cluster: dev, namespace: lab}
clusters:
- name: dev
  cluster: {server: https://dev.example.invalid}
- name: prod
  cluster: {server: https://prod.example.invalid}
users: []
`

// reprobeTestScreen is the minimal Screen the root shell needs before it
// routes g/n/c to a palette.
type reprobeTestScreen struct{ probeGenTestTask }

func (reprobeTestScreen) Theme() Theme                          { return Dark() }
func (reprobeTestScreen) Header() HeaderState                   { return HeaderState{} }
func (reprobeTestScreen) Strips(int) []string                   { return nil }
func (reprobeTestScreen) Keybar() Keybar                        { return Keybar{} }
func (reprobeTestScreen) Body(int, int) string                  { return "" }
func (s reprobeTestScreen) Update(tea.Msg) (tea.Model, tea.Cmd) { return s, nil }

// openReprobeTestPalette opens the 7a context palette over a fixture with a
// "pod-lab" context, which a query that lost its 'r' ("pod") would match.
// Not parallel-safe: t.Setenv.
func openReprobeTestPalette(t *testing.T) Model {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(reprobeTestKubeconfig), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", path)
	testenv.SetHome(t, t.TempDir())

	m := NewWithSession(reprobeTestScreen{}, &Session{
		Theme:    Dark(),
		Location: Location{Context: "dev"},
		State:    state.State{PerContext: map[string]state.PerContext{}},
	})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	updated, _ = updated.(Model).Update(tea.KeyPressMsg{Text: "c", Code: 'c'})
	m = updated.(Model)
	if m.palette == nil {
		t.Fatalf("expected c to open the context palette")
	}
	return m
}

// TestContextPaletteTypesRIntoQuery is the regression for re-probe being a
// bare 'r': typing "prod" filtered on "pod" (matching pod-lab) and restarted
// the probe of every context instead.
func TestContextPaletteTypesRIntoQuery(t *testing.T) {
	m := openReprobeTestPalette(t)
	gen := m.probeGen

	for _, r := range "prod" {
		updated, _ := m.Update(tea.KeyPressMsg{Text: string(r), Code: r})
		m = updated.(Model)
	}
	if got := m.palette.Query(); got != "prod" {
		t.Fatalf("query = %q, want prod typed literally", got)
	}
	if m.probeGen != gen {
		t.Fatalf("typing r re-probed contexts (probeGen %d -> %d)", gen, m.probeGen)
	}
	for _, it := range m.palette.Items {
		if it.Label == "pod-lab" {
			t.Fatalf("query filtered as %q: pod-lab still listed", "pod")
		}
	}
}

// TestContextPaletteCtrlRReprobes pins re-probe's chord, and that it
// leaves the query alone.
func TestContextPaletteCtrlRReprobes(t *testing.T) {
	m := openReprobeTestPalette(t)
	updated, _ := m.Update(tea.KeyPressMsg{Text: "p", Code: 'p'})
	m = updated.(Model)
	gen := m.probeGen

	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	m = updated.(Model)
	if m.palette == nil {
		t.Fatalf("ctrl+r closed the palette")
	}
	if m.probeGen != gen+1 || cmd == nil {
		t.Fatalf("ctrl+r: probeGen %d -> %d, cmd nil=%v; want a fresh probe run", gen, m.probeGen, cmd == nil)
	}
	if got := m.palette.Query(); got != "p" {
		t.Fatalf("query = %q after ctrl+r, want p untouched", got)
	}
}
