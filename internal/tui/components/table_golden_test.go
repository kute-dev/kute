package components

import (
	"os"
	"path/filepath"
	"testing"
)

// goldenTables are the fixtures mvp-plan.md §0.5 calls for
// (test/golden/table/*.golden). Golden comparisons run without a
// TTY, so lipgloss emits no ANSI — these assert on structure/glyphs, not
// color, same as every other golden fixture in this repo.
func goldenTables() map[string]Table {
	basicRows := []Row{
		{Cells: []Cell{{Text: "api-7d9f-abcde"}, {Text: "Running"}, {Text: "2d"}}},
		{Cells: []Cell{{Text: "worker-0"}, {Text: "CrashLoop"}, {Text: "14h"}}},
		{Cells: []Cell{{Text: "cache-0"}, {Text: "Pending"}, {Text: "3m"}}},
	}
	basic := Table{
		Width:    60,
		Height:   6,
		Selected: 1,
		SortKey:  "age",
		SortAsc:  true,
		Columns: []Column{
			{Title: "Name", Min: 20, Flex: true},
			{Title: "Status", Min: 12},
			{Title: "Age", Min: 6, Align: AlignRight, Sort: "age"},
		},
		Rows: basicRows,
	}

	groupRows := []Row{
		{GroupHeader: "▸ default · 3 pods · all running"},
		{Cells: []Cell{{Text: "api-7d9f-abcde"}, {Text: "Running"}, {Text: "2d"}}},
		{GroupHeader: "▸ kube-system · 2 pods · 1 degraded"},
		{Cells: []Cell{{Text: "coredns-abc"}, {Text: "Degraded"}, {Text: "10d"}}},
	}
	groups := Table{
		Width:  60,
		Height: 8,
		Columns: []Column{
			{Title: "Name", Min: 20, Flex: true},
			{Title: "Status", Min: 12},
			{Title: "Age", Min: 6, Align: AlignRight},
		},
		Rows: groupRows,
	}

	return map[string]Table{
		"basic.golden":  basic,
		"groups.golden": groups,
	}
}

func TestGenerateTableGoldenFixtures(t *testing.T) {
	if os.Getenv("UPDATE_GOLDEN") != "1" {
		t.Skip("set UPDATE_GOLDEN=1 to regenerate table golden fixtures")
	}
	for name, table := range goldenTables() {
		path := filepath.Join("..", "..", "..", "test", "golden", "table", name)
		if err := os.WriteFile(path, []byte(table.Render()), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func TestTableGoldenFixtures(t *testing.T) {
	for name, table := range goldenTables() {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join("..", "..", "..", "test", "golden", "table", name)
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden %s: %v", path, err)
			}
			got := table.Render()
			if got != string(want) {
				t.Fatalf("golden mismatch for %s\nwant:\n%s\ngot:\n%s", name, string(want), got)
			}
		})
	}
}
