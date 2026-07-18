package components

import (
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"
)

func TestTableRenderHeadersUppercase(t *testing.T) {
	table := Table{
		Width:   40,
		Height:  3,
		Columns: []Column{{Title: "Name", Min: 20}, {Title: "Age", Min: 6, Align: AlignRight}},
		Rows:    []Row{{Cells: []Cell{{Text: "api"}, {Text: "2d"}}}},
	}
	got := table.Render()
	if !strings.Contains(got, "NAME") {
		t.Fatalf("expected uppercase NAME header:\n%s", got)
	}
	if !strings.Contains(got, "AGE") {
		t.Fatalf("expected uppercase AGE header:\n%s", got)
	}
}

func TestTableRenderSortIndicator(t *testing.T) {
	table := Table{
		Width:   40,
		Height:  3,
		SortKey: "age",
		SortAsc: true,
		Columns: []Column{{Title: "Name", Min: 20}, {Title: "Age", Min: 10, Sort: "age"}},
		Rows:    []Row{{Cells: []Cell{{Text: "api"}, {Text: "2d"}}}},
	}
	got := strings.Split(table.Render(), "\n")[0]
	if !strings.Contains(got, "↑") {
		t.Fatalf("expected sort indicator in header:\n%s", got)
	}
}

func TestTableRenderRightAligns(t *testing.T) {
	table := Table{
		Width:   40,
		Height:  3,
		Columns: []Column{{Title: "Name", Min: 20}, {Title: "Age", Min: 8, Align: AlignRight}},
		Rows:    []Row{{Cells: []Cell{{Text: "api"}, {Text: "2d"}}}},
	}
	lines := strings.Split(table.Render(), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 lines:\n%s", table.Render())
	}
	if !strings.HasSuffix(strings.TrimRight(lines[1], " "), "2d") {
		t.Fatalf("expected right-aligned age at line end: %q", lines[1])
	}
}

func TestTableFlexColumnsFillWidth(t *testing.T) {
	table := Table{
		Width:   50,
		Height:  3,
		Columns: []Column{{Title: "Name", Min: 4, Flex: true}, {Title: "Age", Min: 6}},
		Rows:    []Row{{Cells: []Cell{{Text: "api"}, {Text: "2d"}}}},
	}
	for _, line := range strings.Split(table.Render(), "\n") {
		if runewidth.StringWidth(line) != 50 {
			t.Fatalf("line width = %d, want 50: %q", runewidth.StringWidth(line), line)
		}
	}
}

func TestTableRenderSelectedRowShowsBar(t *testing.T) {
	table := Table{
		Width:    30,
		Height:   4,
		Selected: 1,
		Columns:  []Column{{Title: "Name", Min: 20, Flex: true}, {Title: "Age", Min: 6}},
		Rows: []Row{
			{Cells: []Cell{{Text: "api"}, {Text: "2d"}}},
			{Cells: []Cell{{Text: "worker"}, {Text: "1d"}}},
		},
	}
	lines := strings.Split(table.Render(), "\n")
	if !strings.Contains(lines[1], "api") || strings.Contains(lines[1], tableSelBar) {
		t.Fatalf("row 0 should be unmarked: %q", lines[1])
	}
	if !strings.HasPrefix(lines[2], tableSelBar) {
		t.Fatalf("row 1 should start with the selection bar: %q", lines[2])
	}
}

func TestTableRenderGroupHeader(t *testing.T) {
	table := Table{
		Width:   30,
		Height:  4,
		Columns: []Column{{Title: "Name", Min: 20, Flex: true}, {Title: "Age", Min: 6}},
		Rows: []Row{
			{GroupHeader: "▸ default · 3 pods"},
			{Cells: []Cell{{Text: "api"}, {Text: "2d"}}},
		},
	}
	lines := strings.Split(table.Render(), "\n")
	if !strings.Contains(lines[1], "▸ default · 3 pods") {
		t.Fatalf("expected group header line, got: %q", lines[1])
	}
}

func TestTableVisibleRange(t *testing.T) {
	rows := make([]Row, 12)
	for i := range rows {
		rows[i] = Row{Cells: []Cell{{Text: "x"}}}
	}
	table := Table{Width: 20, Height: 5, Offset: 3, Columns: []Column{{Title: "Name", Min: 10}}, Rows: rows}
	r := table.VisibleRange()
	if r.Start != 3 || r.End != 7 || r.Total != 12 {
		t.Fatalf("VisibleRange() = %+v, want Start=3 End=7 Total=12", r)
	}
}

func TestTableFooterLine(t *testing.T) {
	rows := make([]Row, 36)
	for i := range rows {
		rows[i] = Row{Cells: []Cell{{Text: "x"}}}
	}
	table := Table{Width: 40, Height: 10, Columns: []Column{{Title: "Name", Min: 10}}, Rows: rows}
	got := table.FooterLine(40)
	if !strings.Contains(got, "1–9 of 36") {
		t.Fatalf("expected range text, got: %q", got)
	}
	if runewidth.StringWidth(got) != 40 {
		t.Fatalf("footer width = %d, want 40: %q", runewidth.StringWidth(got), got)
	}
}
