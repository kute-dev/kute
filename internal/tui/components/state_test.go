package components

import "testing"

func TestMoveHalfPage(t *testing.T) {
	tests := []struct {
		name                                         string
		selected, offset, viewport, total, direction int
		want                                         ListPosition
	}{
		{"empty", 3, 4, 5, 0, 1, ListPosition{}},
		{"down", 0, 0, 10, 20, 1, ListPosition{5, 5}},
		{"up", 10, 8, 10, 20, -1, ListPosition{5, 3}},
		{"short list", 0, 0, 10, 3, 1, ListPosition{2, 0}},
		{"bottom clamp", 18, 10, 10, 20, 1, ListPosition{19, 10}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MoveHalfPage(tt.selected, tt.offset, tt.viewport, tt.total, tt.direction); got != tt.want {
				t.Fatalf("MoveHalfPage() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestMoveByLineBudget(t *testing.T) {
	if got := MoveByLineBudget([]int{1, 3, 1, 2}, 0, 4, 1); got != 2 {
		t.Fatalf("crossed budget = %d, want 2", got)
	}
	if got := MoveByLineBudget([]int{1, 3, 1, 2}, 3, 4, -1); got != 1 {
		t.Fatalf("reverse budget = %d, want 1", got)
	}
}
