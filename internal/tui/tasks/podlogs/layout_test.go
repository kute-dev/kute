package podlogs

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// layoutEntry is a deterministic streamed line with enough length to wrap at
// the test widths, plus the severity mix that drives the toolbar counts and
// the tinted most-significant-ERR row.
func layoutEntry(i int) LogEntry {
	entry := LogEntry{
		Container: "worker",
		Timestamp: fmt.Sprintf("10:%02d:%02d", i/60%60, i%60),
		Message:   fmt.Sprintf("handled request %d in 12ms upstream=cart-api path=/v1/checkout/session", i),
	}
	switch i % 9 {
	case 2:
		entry.Severity = SeverityWarn
	case 5:
		entry.Severity = SeverityErr
		entry.Message = fmt.Sprintf("request %d failed: upstream cart-api returned 503 after 3 retries, giving up", i)
	case 7:
		entry = LogEntry{Boundary: true, Timestamp: "10:24:02", Message: "container restarted · restart 3"}
	default:
		entry.Severity = SeverityInfo
	}
	return entry
}

func layoutModel(maxEntries int) Model {
	model := New(Config{
		MaxEntries: maxEntries,
		Pod: SelectedPod{
			Context:    "prod-eks",
			Namespace:  "default",
			Name:       "nva-worker-9k2ss",
			Containers: []string{"worker", "metrics-sidecar"},
		},
	})
	model.SetSize(120, 24)
	model.stream = StreamStreaming
	model.view.Timestamps = true
	return model
}

// TestWindowedLayoutMatchesFullLayout is the guarantee the row index rests
// on: a frame built from just the visible window must be byte-identical to
// the frame the whole-buffer layout pass produces. The two models hold the
// same entries — one filled through appendEntry so the index is maintained
// (the fast path), the other through buffer.Append behind the index's back
// so layoutValid is false (the fallback path golden fixtures and older tests
// already exercise).
func TestWindowedLayoutMatchesFullLayout(t *testing.T) {
	tests := map[string]struct {
		entries    int
		maxEntries int
		tune       func(*Model)
	}{
		"following the tail": {entries: 200},
		"no wrap": {entries: 200, tune: func(m *Model) {
			m.view.Wrap = false
		}},
		"no timestamps": {entries: 200, tune: func(m *Model) {
			m.view.Timestamps = false
		}},
		// HorizontalOffset is deliberately not part of layoutKey: with wrap
		// off an entry is one row whatever the offset, so h/l must not
		// invalidate the index — but it does change what each row shows.
		"no wrap, scrolled right": {entries: 200, tune: func(m *Model) {
			m.view.Wrap = false
			m.view.HorizontalOffset = 20
		}},
		"scrolled into the middle": {entries: 200, tune: func(m *Model) {
			m.view.AutoScroll = false
			m.view.VerticalOffset = 97
		}},
		"scrolled to the top": {entries: 200, tune: func(m *Model) {
			m.view.AutoScroll = false
			m.view.VerticalOffset = 0
		}},
		"fewer entries than the viewport": {entries: 3},
		"filtered": {entries: 200, tune: func(m *Model) {
			m.filterActive = true
			m.filterInput.SetValue("failed")
		}},
		"filtered and scrolled": {entries: 200, tune: func(m *Model) {
			m.filterActive = true
			m.filterInput.SetValue("handled request 1")
			m.view.AutoScroll = false
			m.view.VerticalOffset = 12
		}},
		"filter matches nothing": {entries: 200, tune: func(m *Model) {
			m.filterActive = true
			m.filterInput.SetValue("no such line anywhere")
		}},
		"saturated buffer": {entries: 300, maxEntries: 40},
		"saturated and scrolled": {entries: 300, maxEntries: 40, tune: func(m *Model) {
			m.view.AutoScroll = false
			m.view.VerticalOffset = 5
		}},
		"narrow terminal wraps harder": {entries: 200, tune: func(m *Model) {
			m.SetSize(60, 20)
		}},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			indexed, unindexed := layoutModel(tc.maxEntries), layoutModel(tc.maxEntries)
			if tc.tune != nil {
				tc.tune(&indexed)
				tc.tune(&unindexed)
			}
			for i := range tc.entries {
				indexed.appendEntry(layoutEntry(i))
				unindexed.buffer.Append(layoutEntry(i))
			}
			// appendEntry moves a following viewport; mirror wherever it landed
			// so both models render the same window.
			unindexed.view = indexed.view

			if !indexed.layoutValid() {
				t.Fatalf("the appendEntry-filled model has a stale index, so this compares the fallback with itself")
			}
			if tc.entries > 0 && unindexed.layoutValid() {
				t.Fatalf("the hand-filled model has a live index, so this compares the fast path with itself")
			}
			if got, want := indexed.Render(), unindexed.Render(); got != want {
				t.Fatalf("windowed frame differs from the full-layout frame\nwindowed:\n%s\nfull:\n%s", got, want)
			}
		})
	}
}

// assertIndexInSync recomputes every whole-buffer fact the index caches and
// compares. A stale index still renders correctly (visibleWindow falls back),
// so nothing else in the suite would notice the screen quietly going back to
// laying out the whole buffer for every frame.
func assertIndexInSync(t *testing.T, model Model, step string) {
	t.Helper()
	if !model.layoutValid() {
		t.Fatalf("%s: row index went stale", step)
	}
	if got, want := model.totalRows, len(model.allVisualRows(model.view.Width)); got != want {
		t.Fatalf("%s: totalRows = %d, want %d", step, got, want)
	}
	if got, want := model.matchCount, len(model.filteredEntries()); got != want {
		t.Fatalf("%s: matchCount = %d, want %d", step, got, want)
	}
	wantErr := -1
	for i, entry := range model.buffer.Entries {
		if entry.Severity == SeverityErr && model.entryMatchesFilter(entry) {
			wantErr = i
		}
	}
	if model.lastErr != wantErr {
		t.Fatalf("%s: lastErr = %d, want %d", step, model.lastErr, wantErr)
	}
	rows := 0
	for i, n := range model.rowCounts {
		want := 0
		if model.entryMatchesFilter(model.buffer.Entries[i]) {
			want = model.entryRowCount(model.buffer.Entries[i])
		}
		if n != want {
			t.Fatalf("%s: rowCounts[%d] = %d, want %d", step, i, n, want)
		}
		rows += n
	}
	if rows != model.totalRows {
		t.Fatalf("%s: rowCounts sum to %d, totalRows says %d", step, rows, model.totalRows)
	}
}

// TestRowIndexStaysInStepThroughRealInteractions walks the paths the app
// actually takes. Each one has to leave the index describing the buffer,
// because the moment it doesn't the screen silently reverts to the
// whole-buffer layout the index exists to avoid.
func TestRowIndexStaysInStepThroughRealInteractions(t *testing.T) {
	model := layoutModel(40)

	for i := range 20 {
		model.appendEntry(layoutEntry(i))
	}
	assertIndexInSync(t, model, "after 20 lines")

	for i := 20; i < 120; i++ {
		model.appendEntry(layoutEntry(i))
	}
	assertIndexInSync(t, model, "after saturating the buffer")
	if model.buffer.DroppedCount == 0 {
		t.Fatalf("the buffer never evicted, so eviction bookkeeping went untested")
	}

	press(&model, "w") // wrap off
	assertIndexInSync(t, model, "wrap off")
	press(&model, "t") // timestamps off
	assertIndexInSync(t, model, "timestamps off")
	press(&model, "w")
	press(&model, "t")
	assertIndexInSync(t, model, "wrap and timestamps back on")

	model.SetSize(60, 18)
	assertIndexInSync(t, model, "resized narrow")
	model.appendEntry(layoutEntry(200))
	assertIndexInSync(t, model, "line after a resize")

	press(&model, "/")
	for _, r := range "failed" {
		press(&model, string(r))
	}
	if model.filterInput.Value() != "failed" {
		t.Fatalf("filter query = %q, want the typed text", model.filterInput.Value())
	}
	assertIndexInSync(t, model, "filter typed")
	model.appendEntry(layoutEntry(201))
	assertIndexInSync(t, model, "line arriving under a filter")
	for i := 202; i < 260; i++ {
		model.appendEntry(layoutEntry(i))
	}
	assertIndexInSync(t, model, "eviction under a filter")

	_, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	assertIndexInSync(t, model, "filter cleared")

	press(&model, "e") // jump to next error
	assertIndexInSync(t, model, "severity jump")
	press(&model, "k")
	press(&model, "pgup")
	assertIndexInSync(t, model, "scrolled up")
}

// The index has to survive a stream restart, which empties the buffer under
// it (tab/s and the reconnect path all funnel through beginStream).
func TestBeginStreamResetsRowIndex(t *testing.T) {
	t.Parallel()

	model := layoutModel(0)
	for i := range 50 {
		model.appendEntry(layoutEntry(i))
	}
	model.beginStream(StreamReconnecting)
	if !model.layoutValid() {
		t.Fatalf("row index survived the buffer reset")
	}
	if model.totalRows != 0 || model.matchCount != 0 || model.lastErr != -1 || len(model.rowCounts) != 0 {
		t.Fatalf("index not cleared: rows=%d matched=%d lastErr=%d counts=%d",
			model.totalRows, model.matchCount, model.lastErr, len(model.rowCounts))
	}
}

func TestEntryAtRowWalksFromEitherEnd(t *testing.T) {
	t.Parallel()

	// Entry 2 is hidden by a filter (0 rows); the rest occupy 3, 1, 2 and 4
	// rows, so the row ranges are [0,3) [3,4) — [4,6) [6,10).
	model := Model{rowCounts: []int{3, 1, 0, 2, 4}, totalRows: 10}
	tests := []struct {
		offset      int
		wantIndex   int
		wantRowsPre int
	}{
		{offset: 0, wantIndex: 0, wantRowsPre: 0},
		{offset: 2, wantIndex: 0, wantRowsPre: 0},
		{offset: 3, wantIndex: 1, wantRowsPre: 3},
		{offset: 4, wantIndex: 3, wantRowsPre: 4}, // skips the filtered-out entry
		{offset: 5, wantIndex: 3, wantRowsPre: 4},
		{offset: 6, wantIndex: 4, wantRowsPre: 6}, // past the midpoint: walks backward
		{offset: 9, wantIndex: 4, wantRowsPre: 6},
		{offset: 10, wantIndex: 5, wantRowsPre: 10}, // one past the end
		{offset: 40, wantIndex: 5, wantRowsPre: 10},
	}
	for _, tc := range tests {
		index, rowsBefore := model.entryAtRow(tc.offset)
		if index != tc.wantIndex || rowsBefore != tc.wantRowsPre {
			t.Errorf("entryAtRow(%d) = (%d, %d), want (%d, %d)", tc.offset, index, rowsBefore, tc.wantIndex, tc.wantRowsPre)
		}
	}
}

// Both walk directions must agree for every offset, since which one runs is
// only a function of where the viewport happens to sit.
func TestEntryAtRowAgreesFromBothDirections(t *testing.T) {
	t.Parallel()

	model := Model{rowCounts: []int{2, 0, 1, 5, 0, 3, 1}}
	for _, n := range model.rowCounts {
		model.totalRows += n
	}
	for offset := range model.totalRows + 3 {
		forward := Model{rowCounts: model.rowCounts, totalRows: 1 << 30} // forces the forward branch
		wantIndex, wantRows := forward.entryAtRow(offset)
		gotIndex, gotRows := model.entryAtRow(offset)
		if gotIndex != wantIndex || gotRows != wantRows {
			t.Fatalf("offset %d: index walk = (%d, %d), forward walk = (%d, %d)", offset, gotIndex, gotRows, wantIndex, wantRows)
		}
	}
}

// The '/' filter narrows what the toolbar counts and what ctrl-y copies, both
// of which now read the window rather than the whole buffer.
func TestWindowConsumersRespectTheFilter(t *testing.T) {
	t.Parallel()

	model := layoutModel(0)
	for i := range 60 {
		model.appendEntry(layoutEntry(i))
	}
	press(&model, "/")
	for _, r := range "failed" {
		press(&model, string(r))
	}
	text := model.visibleViewText()
	if text == "" || strings.Contains(text, "handled request") {
		t.Fatalf("ctrl-y text ignored the filter:\n%s", text)
	}
	warn, err := model.visibleSeverityCounts()
	if warn != 0 || err == 0 {
		t.Fatalf("severity counts = %d WRN / %d ERR, want only the ERR lines the filter keeps", warn, err)
	}
}
