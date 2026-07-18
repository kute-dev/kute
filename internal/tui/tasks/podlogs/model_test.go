package podlogs

import "testing"

func testModel() Model {
	model := New(Config{Pod: SelectedPod{
		Context:    "ctx",
		Namespace:  "default",
		Name:       "api",
		Containers: []string{"app", "sidecar"},
		Restarts:   2,
	}})
	model.SetSize(80, 24)
	return model
}

func seedLines(model *Model, count int) {
	for i := 0; i < count; i++ {
		model.appendEntry(LogEntry{Container: "app", Message: string(rune('a' + i))})
	}
}

func TestNewDefaultsToFirstContainerFollowingAndWrap(t *testing.T) {
	t.Parallel()

	model := New(Config{Pod: SelectedPod{Name: "api", Containers: []string{"app", "sidecar"}}})
	if container, ok := model.activeContainer(); !ok || container != "app" {
		t.Fatalf("active container = %q, %v", container, ok)
	}
	if !model.view.AutoScroll || !model.view.Wrap {
		t.Fatalf("view defaults = %+v, want autoscroll and wrap enabled", model.view)
	}
	if model.sinceLabel() != "15m" {
		t.Fatalf("since label = %q, want 15m default", model.sinceLabel())
	}
}

func TestLogBufferBoundsEntries(t *testing.T) {
	t.Parallel()

	buffer := LogBuffer{MaxEntries: 2}
	buffer.Append(LogEntry{Message: "one"})
	buffer.Append(LogEntry{Message: "two"})
	buffer.Append(LogEntry{Message: "three"})
	if len(buffer.Entries) != 2 || buffer.Entries[0].Message != "two" || buffer.DroppedCount != 1 {
		t.Fatalf("buffer = %+v", buffer)
	}
}

func TestClampOffsets(t *testing.T) {
	t.Parallel()

	model := New(Config{Pod: SelectedPod{Name: "api", Containers: []string{"app"}}, MaxEntries: 10})
	model.SetSize(80, 8)
	for i := 0; i < 10; i++ {
		model.buffer.Append(LogEntry{Message: "line"})
	}
	model.view.VerticalOffset = 100
	model.view.HorizontalOffset = -1
	model.clampOffsets()
	if model.view.VerticalOffset != model.maxVerticalOffset() || model.view.HorizontalOffset != 0 {
		t.Fatalf("offsets = %+v", model.view)
	}
}

func TestCycleContainerWrapsAndNoopsForSingleContainer(t *testing.T) {
	t.Parallel()

	model := testModel() // app, sidecar
	model.cycleContainer()
	if container, _ := model.activeContainer(); container != "sidecar" {
		t.Fatalf("container after cycle = %q", container)
	}
	model.cycleContainer()
	if container, _ := model.activeContainer(); container != "app" {
		t.Fatalf("container after wrap = %q", container)
	}

	single := New(Config{Pod: SelectedPod{Name: "api", Containers: []string{"solo"}}})
	single.cycleContainer()
	if container, _ := single.activeContainer(); container != "solo" {
		t.Fatalf("single-container cycle changed container to %q", container)
	}
}

func TestCycleSinceWrapsThroughAllOptions(t *testing.T) {
	t.Parallel()

	model := testModel()
	labels := make([]string, 0, len(sinceOptions)+1)
	labels = append(labels, model.sinceLabel())
	for range sinceOptions {
		model.cycleSince()
		labels = append(labels, model.sinceLabel())
	}
	if labels[0] != labels[len(labels)-1] {
		t.Fatalf("since cycle did not return to start: %v", labels)
	}
	if labels[0] != "15m" {
		t.Fatalf("default since label = %q, want 15m", labels[0])
	}
}

func TestFilteredEntriesMatchesSubstringAndKeepsBoundaries(t *testing.T) {
	t.Parallel()

	model := testModel()
	model.buffer.Append(LogEntry{Message: "starting up"})
	model.buffer.Append(LogEntry{Message: "request failed: boom"})
	model.buffer.Append(LogEntry{Boundary: true, Message: "container restarted · restart 1"})

	model.filterQuery = "failed"
	filtered := model.filteredEntries()
	if len(filtered) != 2 {
		t.Fatalf("filtered = %+v, want message match + boundary", filtered)
	}
	hasBoundary, hasMatch := false, false
	for _, e := range filtered {
		if e.Boundary {
			hasBoundary = true
		} else if e.Message == "request failed: boom" {
			hasMatch = true
		}
	}
	if !hasBoundary || !hasMatch {
		t.Fatalf("filtered entries = %+v, want boundary + substring match", filtered)
	}
}
