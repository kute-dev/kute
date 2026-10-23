package tui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// keyRecorder is a Screen that records every key it is handed.
type keyRecorder struct{ keys []string }

func (r *keyRecorder) Init() tea.Cmd    { return nil }
func (r *keyRecorder) SetSize(int, int) {}
func (r *keyRecorder) View() tea.View   { return tea.NewView("") }
func (r *keyRecorder) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyPressMsg); ok {
		r.keys = append(r.keys, k.String())
	}
	return r, nil
}
func (r *keyRecorder) Theme() Theme         { return Dark() }
func (r *keyRecorder) Header() HeaderState  { return HeaderState{} }
func (r *keyRecorder) Strips(int) []string  { return nil }
func (r *keyRecorder) Keybar() Keybar       { return Keybar{} }
func (r *keyRecorder) Body(int, int) string { return "" }

func ggModel() (Model, *keyRecorder) {
	rec := &keyRecorder{}
	return NewWithSession(rec, &Session{Theme: Dark(), Styles: NewStyles(Dark())}), rec
}

func ggPress(t *testing.T, m Model, text string) Model {
	t.Helper()
	updated, _ := m.Update(tea.KeyPressMsg{Code: []rune(text)[0], Text: text})
	return updated.(Model)
}

// ggExpire delivers the current gg phase's timeout, as tea.Tick would.
func ggExpire(t *testing.T, m Model) Model {
	t.Helper()
	updated, cmd := m.Update(ggTimeoutMsg{seq: m.ggSeq, phase: m.gg})
	if cmd != nil {
		cmd()
	}
	return updated.(Model)
}

func TestGGJumpsToTop(t *testing.T) {
	m, rec := ggModel()
	m = ggPress(t, m, "g")
	m = ggPress(t, m, "g")
	if !m.PaletteOpen() {
		t.Fatal("palette closed before the commit delay elapsed")
	}
	m = ggExpire(t, m)
	if m.PaletteOpen() {
		t.Fatal("gg left the goto palette open")
	}
	if len(rec.keys) != 1 || rec.keys[0] != "home" {
		t.Fatalf("task got %q, want one home", rec.keys)
	}
}

// TestGGBurstStillTypesAQuery is the "g gitlab" case: a held g followed by
// more typing is a query, never gg.
func TestGGBurstStillTypesAQuery(t *testing.T) {
	m, rec := ggModel()
	for _, k := range []string{"g", "g", "i", "t"} {
		m = ggPress(t, m, k)
	}
	m = ggExpire(t, m)
	if !m.PaletteOpen() {
		t.Fatal("typing a query closed the palette")
	}
	if got := m.palette.Query(); got != "git" {
		t.Fatalf("query = %q, want %q", got, "git")
	}
	if len(rec.keys) != 0 {
		t.Fatalf("task got %q while the palette was typing", rec.keys)
	}
}

// TestGGSlowSecondGTypes: once the arm window has passed, g is just a letter.
func TestGGSlowSecondGTypes(t *testing.T) {
	now := time.Unix(0, 0)
	ggNow = func() time.Time { return now }
	t.Cleanup(func() { ggNow = time.Now })
	m, _ := ggModel()
	m = ggPress(t, m, "g")
	now = now.Add(ggArmWindow + time.Millisecond)
	m = ggPress(t, m, "g")
	if got := m.palette.Query(); got != "g" {
		t.Fatalf("query = %q, want %q", got, "g")
	}
	m = ggExpire(t, m)
	if !m.PaletteOpen() {
		t.Fatal("a slow second g was taken as gg")
	}
}

// TestGGOnlyFromG: ':' opens the same palette but never arms gg, so a query
// can always start with g from there.
func TestGGOnlyFromG(t *testing.T) {
	m, _ := ggModel()
	m = ggPress(t, m, ":")
	m = ggPress(t, m, "g")
	if got := m.palette.Query(); got != "g" {
		t.Fatalf("query = %q, want %q", got, "g")
	}
}

func TestGGStaleTimeoutIgnored(t *testing.T) {
	m, rec := ggModel()
	m = ggPress(t, m, "g")
	m = ggPress(t, m, "g")
	stale := ggTimeoutMsg{seq: m.ggSeq, phase: m.gg}
	m = ggPress(t, m, "i") // releases the held g
	updated, _ := m.Update(stale)
	m = updated.(Model)
	if !m.PaletteOpen() || m.palette.Query() != "gi" || len(rec.keys) != 0 {
		t.Fatalf("stale timeout acted: open=%v query=%q task=%q", m.PaletteOpen(), m.palette.Query(), rec.keys)
	}
}

func TestGotoQuitCommands(t *testing.T) {
	for _, tc := range []struct {
		query     string
		wantForce bool
	}{
		{"q", false}, {"quit", false}, {"wq", false}, {"q!", true}, {"qa!", true},
	} {
		m, _ := ggModel()
		m = ggPress(t, m, ":")
		for _, r := range tc.query {
			m = ggPress(t, m, string(r))
		}
		item, ok := m.palette.Selected()
		if !ok {
			t.Fatalf("%q: nothing selected", tc.query)
		}
		target, _ := item.Data.(gotoTarget)
		if target.action != gotoQuit || target.force != tc.wantForce {
			t.Fatalf("%q: selected %+v, want quit force=%v", tc.query, item, tc.wantForce)
		}
		updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		m = updated.(Model)
		msg := cmd()
		if req, ok := msg.(QuitRequestMsg); !ok || req.Force != tc.wantForce {
			t.Fatalf("%q: enter sent %#v", tc.query, msg)
		}
		updated, cmd = m.Update(msg)
		m = updated.(Model)
		if tc.wantForce {
			if _, ok := cmd().(tea.QuitMsg); !ok {
				t.Fatalf("%q: did not quit", tc.query)
			}
		} else if !m.QuitConfirmOpen() {
			t.Fatalf("%q: did not open the quit confirm", tc.query)
		}
	}
}

// TestGotoQuitNeverFuzzy: a longer query is a plain search, and q is only
// rank 1, not the only row.
func TestGotoQuitNeverFuzzy(t *testing.T) {
	if _, ok := gotoQuitItem("qu"); ok {
		t.Fatal("\"qu\" offered quit")
	}
	if _, ok := gotoQuitItem("quota"); ok {
		t.Fatal("\"quota\" offered quit")
	}
}

type claimingRecorder struct{ keyRecorder }

func (c *claimingRecorder) ClaimsKey(key string) bool { return key == "n" }

// TestKeyClaimerTakesShellKey: a screen claiming n (8a's committed search)
// gets it instead of the namespace palette; unclaimed keys still open theirs.
func TestKeyClaimerTakesShellKey(t *testing.T) {
	rec := &claimingRecorder{}
	m := NewWithSession(rec, &Session{Theme: Dark(), Styles: NewStyles(Dark())})
	m = ggPress(t, m, "n")
	if m.PaletteOpen() || len(rec.keys) != 1 || rec.keys[0] != "n" {
		t.Fatalf("claimed n: palette=%v task=%q", m.PaletteOpen(), rec.keys)
	}
	m = ggPress(t, m, "c")
	if !m.PaletteOpen() {
		t.Fatal("an unclaimed shell key must still open its palette")
	}
}
