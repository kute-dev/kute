package resources

import (
	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui/components"
)

// RightAlignTitles are the column titles that read as numeric/right-aligned
// in every mockup (AGE etc.) across kinds. Restarts is not among them — the
// 2a mockup renders the restart count left-aligned under its ↺ header.
// Exported so browse's own 1-9 manual sort (sort.go) can reuse the same
// "reads as numeric" set for its descending-first default, instead of
// re-deriving it.
var RightAlignTitles = map[string]bool{
	"Age": true, "Data": true, "Replicas": true,
	"Completions": true, "Active": true, "Capacity": true,
	"Traffic": true, "Rev": true, "Updated": true,
}

// Columns builds the components.Table column specs for d from its
// Descriptor.Columns titles, led by the 2a status-glyph column (untitled,
// 1ch — the table's 2ch inter-column gap makes up the mockup's "status dot
// (2ch)" slot): the free-text column ("Name", or the first column when
// there is none — Events lead with "Type") flexes to fill leftover width;
// known numeric-ish titles right-align. Takes the Descriptor directly
// (rather than looking it up in a freshly built DefaultRegistry()) so
// discovered kinds — never present in DefaultRegistry(), only in a
// session's own rebuilt Registry — get correct columns too.
func Columns(d Descriptor) []components.Column {
	flexTitle := d.FlexColumn
	if flexTitle == "" {
		flexTitle = "Name"
	}
	flexIndex := 0
	for i, title := range d.Columns {
		if title == flexTitle {
			flexIndex = i
			break
		}
	}

	cols := make([]components.Column, 0, len(d.Columns)+1)
	cols = append(cols, components.Column{Title: "", Min: 1})
	for i, title := range d.Columns {
		col := components.Column{Title: title, Min: minWidthFor(title)}
		if RightAlignTitles[title] {
			col.Align = components.AlignRight
		}
		if title == "Status" {
			col.Sort = "status"
		}
		if i == flexIndex {
			col.Flex = true
		}
		cols = append(cols, col)
	}
	return cols
}

// MetricColumnWidth is the fixed rendered width of the CPU/MEM columns
// (browse's mini-usage bars are built at this exact width — kept in sync
// via this constant rather than measuring the rendered column at draw
// time). Neither column flexes (see Columns above), so this Min is also
// the column's actual on-screen width.
const MetricColumnWidth = 12

// fixedWidths are the docs/design §2a column widths (mock grid mapped to
// cells). Status is 16 — the mockup renders "CrashLoopBackOff" untruncated,
// so the README's approximate "13ch" loses to the mock here.
var fixedWidths = map[string]int{
	"Rdy":      5,
	"Status":   16,
	"Health":   13, // "●12 ◐1 ✕1" — matches the namespace palette's HEALTH width
	"Restarts": 4,
	"Node":     9,
	"Age":      4,
	"CPU":      MetricColumnWidth,
	"MEM":      MetricColumnWidth,
	"Pods":     9,  // "62/110"
	"Rollout":  20, // "12m 34s progressing ▸"
	"Image":    24, // truncates long registry paths
	"Class":    9,  // ingress class name, e.g. "nginx"
	"Hosts":    30, // comma-joined rule hosts, e.g. "api.example.com"
	"Address":  15, // LB IP/hostname, e.g. "203.0.113.10"
	"Ports":    10, // "80" / "80, 443"
	"Local":    16, // "localhost:65535"
	"Uptime":   7,  // "12h34m"
	"Traffic":  22, // "retry 3 · next in 8s" / "idle 12m"
	"Chart":    22, // "postgresql 12.1.9"
	"App Ver":  10, // "15.4.0"
	"Rev":      4,  // revision number, right-aligned
	"Updated":  10, // "3d ago" / "12m ago"
}

func minWidthFor(title string) int {
	if w, ok := fixedWidths[title]; ok {
		return w
	}
	return max(len(title)+2, 6)
}

// Cells converts row into display cells, one per Columns(kind) entry — the
// leading cell is the row's status glyph (empty when the projection didn't
// set one; browse fills the per-status fallback, keeping glyph choice at
// the tui/glyphs.go choke point). width/metrics are accepted now so the
// signature doesn't change once a kind grows a live bar column (CPU/MEM
// mini-bars keyed by pod name via metrics) — no built-in descriptor has one
// yet, so both go unused today.
func Cells(row Row, _ int, _ map[string]kube.PodMetrics) []components.Cell {
	cells := make([]components.Cell, 0, len(row.Cells)+1)
	cells = append(cells, components.Cell{Text: row.Glyph})
	for _, text := range row.Cells {
		cells = append(cells, components.Cell{Text: text})
	}
	return cells
}
