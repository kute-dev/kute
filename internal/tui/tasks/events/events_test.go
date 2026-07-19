package events

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
)

func plain(s string) string { return ansi.Strip(s) }

// fakeEvents is a minimal EventsReader test double.
type fakeEvents struct {
	namespaceEvents []kube.Event
	objectEvents    []kube.Event
	err             error
}

func (f fakeEvents) NamespaceEvents(context.Context, string) ([]kube.Event, error) {
	return f.namespaceEvents, f.err
}

func (f fakeEvents) ObjectEvents(context.Context, string, kube.ResourceKind, string) ([]kube.Event, error) {
	return f.objectEvents, f.err
}

func newSession() *tui.Session {
	return &tui.Session{
		Location: tui.Location{Context: "microk8s-cluster", Namespace: "default"},
		Theme:    tui.Dark(),
	}
}

// step applies one message and returns the updated Model, draining a
// tea.BatchMsg fan-out like browse's own test helper does. A
// tea.Sequence-returning Cmd (openSelectedObject's ↵ navigation) yields an
// unexported bubbletea type this package can't type-switch on; passing it
// straight to Update is harmless (no case matches, so it's a no-op) — the
// real *tea.Program is what actually drains a sequence in production.
func step(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			if c != nil {
				m = step(t, m, c())
			}
		}
		return m
	}
	updated, cmd := m.Update(msg)
	next := *updated.(*Model)
	if cmd != nil {
		return step(t, next, cmd())
	}
	return next
}

func TestNamespaceScopedLoadDedupesAndFoldsNormal(t *testing.T) {
	events := []kube.Event{
		{Type: "Warning", Reason: "BackOff", Object: "Pod/worker-0", Message: "back-off restarting", Count: 1, LastSeen: time.Now()},
		{Type: "Warning", Reason: "BackOff", Object: "Pod/worker-0", Message: "back-off restarting", Count: 1, LastSeen: time.Now()},
		{Type: "Normal", Reason: "Pulled", Object: "Pod/worker-0", Message: "pulled image", Count: 1, LastSeen: time.Now()},
		{Type: "Normal", Reason: "Created", Object: "Pod/worker-0", Message: "created container", Count: 1, LastSeen: time.Now()},
	}
	m := New(Config{Session: newSession(), Events: fakeEvents{namespaceEvents: events}, Namespace: "default"})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	if m.state != tui.TaskStateReady {
		t.Fatalf("state = %s, want ready", m.state)
	}
	// Two warning events dedupe into one group; two normal events fold into
	// one summary row — three display rows total.
	if len(m.rows) != 2 {
		t.Fatalf("rows = %d, want 2 (1 warning group + 1 folded normal row)", len(m.rows))
	}
	if m.rows[0].kind != rowGroup || m.rows[0].group.Reason != "BackOff" {
		t.Fatalf("rows[0] = %+v, want the deduped BackOff warning", m.rows[0])
	}
	if m.rows[1].kind != rowFolded || len(m.rows[1].folded) != 2 {
		t.Fatalf("rows[1] = %+v, want a folded row with 2 normal groups", m.rows[1])
	}

	view := plain(m.Render())
	if !strings.Contains(view, "BackOff") || !strings.Contains(view, "×2") {
		t.Fatalf("expected deduped BackOff ×2 in view:\n%s", view)
	}
	if !strings.Contains(view, "normal · 2 events") {
		t.Fatalf("expected folded normal summary line in view:\n%s", view)
	}
}

func TestTabExpandsAndCollapsesFoldedNormalRow(t *testing.T) {
	events := []kube.Event{
		{Type: "Normal", Reason: "Pulled", Object: "Pod/worker-0", Message: "pulled image", Count: 1, LastSeen: time.Now()},
		{Type: "Normal", Reason: "Created", Object: "Pod/worker-0", Message: "created container", Count: 1, LastSeen: time.Now()},
	}
	m := New(Config{Session: newSession(), Events: fakeEvents{namespaceEvents: events}, Namespace: "default"})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	if len(m.rows) != 1 || m.rows[0].kind != rowFolded {
		t.Fatalf("expected one folded row before expand, got %+v", m.rows)
	}
	m = step(t, m, tea.KeyPressMsg{Text: "tab"})
	if len(m.rows) != 2 {
		t.Fatalf("expected 2 expanded rows after tab, got %d", len(m.rows))
	}
	m = step(t, m, tea.KeyPressMsg{Text: "tab"})
	if len(m.rows) != 1 || m.rows[0].kind != rowFolded {
		t.Fatalf("expected folded again after second tab, got %+v", m.rows)
	}
}

func TestWKeyTogglesWarningsOnly(t *testing.T) {
	events := []kube.Event{
		{Type: "Warning", Reason: "BackOff", Object: "Pod/worker-0", Message: "m", Count: 1, LastSeen: time.Now()},
		{Type: "Normal", Reason: "Pulled", Object: "Pod/worker-0", Message: "m", Count: 1, LastSeen: time.Now()},
	}
	m := New(Config{Session: newSession(), Events: fakeEvents{namespaceEvents: events}, Namespace: "default"})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	if len(m.rows) != 2 {
		t.Fatalf("expected warning + folded-normal rows, got %d", len(m.rows))
	}
	m = step(t, m, tea.KeyPressMsg{Text: "w"})
	if len(m.rows) != 1 || m.rows[0].kind != rowGroup {
		t.Fatalf("expected only the warning row once warnings-only is on, got %+v", m.rows)
	}
}

func TestTimeWindowFiltersOldEvents(t *testing.T) {
	events := []kube.Event{
		{Type: "Warning", Reason: "Recent", Object: "Pod/a", Message: "m", Count: 1, LastSeen: time.Now()},
		{Type: "Warning", Reason: "Ancient", Object: "Pod/b", Message: "m", Count: 1, LastSeen: time.Now().Add(-48 * time.Hour)},
	}
	m := New(Config{Session: newSession(), Events: fakeEvents{namespaceEvents: events}, Namespace: "default"})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()()) // default window is 1h

	if len(m.rows) != 1 || m.rows[0].group.Reason != "Recent" {
		t.Fatalf("expected only the recent event within the default 1h window, got %+v", m.rows)
	}

	// Cycle t: 15m -> 1h -> 6h -> 24h -> all. Starting at 1h, 3 more presses
	// reaches "all".
	for range 3 {
		m = step(t, m, tea.KeyPressMsg{Text: "t"})
	}
	if len(m.rows) != 2 {
		t.Fatalf("expected both events once the window is 'all', got %d", len(m.rows))
	}
}

func TestFilterQueryNarrowsRows(t *testing.T) {
	events := []kube.Event{
		{Type: "Warning", Reason: "BackOff", Object: "Pod/worker-0", Message: "restarting", Count: 1, LastSeen: time.Now()},
		{Type: "Warning", Reason: "FailedScheduling", Object: "Pod/cache-0", Message: "insufficient cpu", Count: 1, LastSeen: time.Now()},
	}
	m := New(Config{Session: newSession(), Events: fakeEvents{namespaceEvents: events}, Namespace: "default"})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Text: "/"})
	if !m.filterActive {
		t.Fatal("expected / to activate the filter")
	}
	for _, r := range "worker" {
		m = step(t, m, tea.KeyPressMsg{Text: string(r)})
	}
	if len(m.rows) != 1 || m.rows[0].group.Reason != "BackOff" {
		t.Fatalf("expected filter to narrow to the worker-0 event, got %+v", m.rows)
	}
	// docs/design system-wide interactions: "items never silently
	// disappear" — the strip must say a row was hidden by the filter, not
	// just show a bare matched count.
	view := plain(m.Render())
	if !strings.Contains(view, "hidden by filter") {
		t.Fatalf("expected the 'hidden by filter' notice:\n%s", view)
	}
}

func TestFilterAltJKMovesSelectionWithoutTyping(t *testing.T) {
	events := []kube.Event{
		{Type: "Warning", Reason: "BackOff", Object: "Pod/worker-0", Message: "restarting", Count: 1, LastSeen: time.Now()},
		{Type: "Warning", Reason: "FailedScheduling", Object: "Pod/cache-0", Message: "insufficient cpu", Count: 1, LastSeen: time.Now()},
	}
	m := New(Config{Session: newSession(), Events: fakeEvents{namespaceEvents: events}, Namespace: "default"})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Text: "/"})
	if m.selected != 0 {
		t.Fatalf("selected = %d, want 0 before moving", m.selected)
	}

	m = step(t, m, tea.KeyPressMsg{Code: 'j', Mod: tea.ModAlt})
	if m.selected != 1 {
		t.Fatalf("selected = %d, want 1 after alt+j", m.selected)
	}
	if m.filterQuery != "" {
		t.Fatalf("filterQuery = %q, want empty (alt+j must move, not type)", m.filterQuery)
	}

	m = step(t, m, tea.KeyPressMsg{Code: 'k', Mod: tea.ModAlt})
	if m.selected != 0 {
		t.Fatalf("selected = %d, want 0 after alt+k", m.selected)
	}
	if m.filterQuery != "" {
		t.Fatalf("filterQuery = %q, want empty (alt+k must move, not type)", m.filterQuery)
	}
}

func TestObjectScopedLoadUsesObjectEvents(t *testing.T) {
	objectEvents := []kube.Event{
		{Type: "Warning", Reason: "BackOff", Object: "Pod/worker-0", Message: "m", Count: 1, LastSeen: time.Now()},
	}
	m := New(Config{
		Session: newSession(), Events: fakeEvents{namespaceEvents: nil, objectEvents: objectEvents},
		Namespace: "default", ObjectKind: kube.KindPod, ObjectName: "worker-0",
	})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	if m.state != tui.TaskStateReady || len(m.rows) != 1 {
		t.Fatalf("expected object-scoped events to load, state=%s rows=%d", m.state, len(m.rows))
	}
	view := plain(m.Render())
	if !strings.Contains(view, "Pod/worker-0") {
		t.Fatalf("expected the object-scoped breadcrumb, got:\n%s", view)
	}
}

func TestEmptyStateWhenNoEvents(t *testing.T) {
	m := New(Config{Session: newSession(), Events: fakeEvents{}, Namespace: "default"})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	if m.state != tui.TaskStateEmpty {
		t.Fatalf("state = %s, want empty", m.state)
	}
	view := plain(m.Render())
	if !strings.Contains(view, "no events in default") {
		t.Fatalf("expected the empty-state message, got:\n%s", view)
	}
}

func TestEnterOnGroupRowReturnsNavigationCmd(t *testing.T) {
	events := []kube.Event{
		{Type: "Warning", Reason: "BackOff", Object: "Pod/worker-0", Message: "m", Count: 1, LastSeen: time.Now()},
	}
	m := New(Config{Session: newSession(), Events: fakeEvents{namespaceEvents: events}, Namespace: "default"})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	cmd, ok := m.openSelectedObject()
	if !ok || cmd == nil {
		t.Fatal("expected ↵ on a real event group to produce a navigation command")
	}
}

func TestEnterOnFoldedRowIsNoop(t *testing.T) {
	events := []kube.Event{
		{Type: "Normal", Reason: "Pulled", Object: "Pod/worker-0", Message: "m", Count: 1, LastSeen: time.Now()},
		{Type: "Normal", Reason: "Created", Object: "Pod/worker-0", Message: "m", Count: 1, LastSeen: time.Now()},
	}
	m := New(Config{Session: newSession(), Events: fakeEvents{namespaceEvents: events}, Namespace: "default"})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	if _, ok := m.openSelectedObject(); ok {
		t.Fatal("expected ↵ on the folded summary row to be a no-op")
	}
}

func TestEscSendsBackMsg(t *testing.T) {
	m := New(Config{Session: newSession(), Events: fakeEvents{}})
	m.SetSize(120, 36)
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("esc produced no command")
	}
	if _, ok := cmd().(tui.BackMsg); !ok {
		t.Fatal("esc did not send BackMsg")
	}
}
