package podlogs

import (
	"fmt"
	"testing"
)

// benchEntry is one realistic streamed line: long enough to wrap at 120
// columns once the timestamp/severity prefix is accounted for, with the
// severity mix that drives the toolbar's counts and the tinted ERR row.
func benchEntry(i int) LogEntry {
	entry := LogEntry{
		Container: "app",
		Timestamp: "10:24:05",
		Message:   fmt.Sprintf("handled request %d in 12ms upstream=cart-api path=/v1/checkout/session trace=%08x", i, i*2654435761),
	}
	switch i % 17 {
	case 3:
		entry.Severity = SeverityWarn
	case 11:
		entry.Severity = SeverityErr
	default:
		entry.Severity = SeverityInfo
	}
	return entry
}

// benchModel builds a following log viewer holding n entries at a realistic
// terminal size, filled through the same appendEntry the live stream uses.
func benchModel(n int) Model {
	model := New(Config{Pod: SelectedPod{
		Context:    "prod-eks",
		Namespace:  "default",
		Name:       "nva-worker-9k2ss",
		Containers: []string{"worker"},
	}})
	model.SetSize(120, 36)
	model.stream = StreamStreaming
	model.view.Timestamps = true
	for i := range n {
		model.appendEntry(benchEntry(i))
	}
	return model
}

// BenchmarkPodLogsRender measures one full 5b frame. The buffer holds up to
// DefaultMaxEntries lines while the viewport shows ~30 of them, so
// entries=5000 must cost about what entries=500 does. It did not before the
// row index: a frame laid the entire buffer out twice — once for the body and
// once for the toolbar's severity counts — to display a viewport's worth
// (docs/performance.md). Run with -benchmem; the allocation count is the
// number that matters more than ns/op.
func BenchmarkPodLogsRender(b *testing.B) {
	for _, n := range []int{500, 5000} {
		b.Run(fmt.Sprintf("entries=%d", n), func(b *testing.B) {
			model := benchModel(n)
			b.ReportAllocs()
			for b.Loop() {
				_ = model.Render()
			}
		})
	}
}

// BenchmarkPodLogsRenderScrolled is the same frame with following paused
// partway up the buffer — the path that has to find the window instead of
// taking the tail.
func BenchmarkPodLogsRenderScrolled(b *testing.B) {
	model := benchModel(5000)
	model.view.AutoScroll = false
	model.view.VerticalOffset = model.maxVerticalOffset() / 3
	b.ReportAllocs()
	for b.Loop() {
		_ = model.Render()
	}
}

// BenchmarkPodLogsAppend is the per-line cost at the steady state of a
// high-rate burst: one entry onto an already-saturated buffer, which also
// evicts one.
func BenchmarkPodLogsAppend(b *testing.B) {
	model := benchModel(DefaultMaxEntries)
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		model.appendEntry(benchEntry(i))
		i++
	}
}
