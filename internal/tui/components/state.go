package components

// VisibleRange is the [Start,End) row window currently rendered, out of
// Total available — used by Table's scrollbar/footer ("N of M").
type VisibleRange struct {
	Start int
	End   int
	Total int
}

// ListPosition is the cursor and first visible row of a bounded list.
type ListPosition struct {
	Selected int
	Offset   int
}

// MoveHalfPage moves a list cursor and its window together by half the active
// viewport. The result is always a valid, visible cursor position.
func MoveHalfPage(selected, offset, viewportRows, total, direction int) ListPosition {
	if total <= 0 {
		return ListPosition{}
	}
	if viewportRows < 1 {
		viewportRows = 1
	}
	selected = clampState(selected, 0, total-1)
	maxOffset := total - viewportRows
	if maxOffset < 0 {
		maxOffset = 0
	}
	offset = clampState(offset, 0, maxOffset)
	step := max(1, viewportRows/2)
	if direction < 0 {
		step = -step
	} else if direction == 0 {
		return ListPosition{Selected: selected, Offset: offset}
	}
	selected = clampState(selected+step, 0, total-1)
	offset = clampState(offset+step, 0, maxOffset)
	if selected < offset {
		offset = selected
	}
	if selected >= offset+viewportRows {
		offset = selected - viewportRows + 1
	}
	offset = clampState(offset, 0, maxOffset)
	return ListPosition{Selected: selected, Offset: offset}
}

// MoveByLineBudget advances across variable-height rows until budget rendered
// lines have been crossed, returning the nearest row in that direction.
func MoveByLineBudget(heights []int, selected, budget, direction int) int {
	if len(heights) == 0 {
		return 0
	}
	selected = clampState(selected, 0, len(heights)-1)
	if budget < 1 {
		budget = 1
	}
	if direction == 0 {
		return selected
	}
	step := 1
	if direction < 0 {
		step = -1
	}
	remaining := budget
	current := selected
	for {
		next := current + step
		if next < 0 || next >= len(heights) {
			return current
		}
		rowLines := heights[next]
		if rowLines < 1 {
			rowLines = 1
		}
		current = next
		remaining -= rowLines
		if remaining <= 0 {
			return current
		}
	}
}

func clampState(value, low, high int) int {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ClampRange computes the visible window for a viewport of size rows,
// starting at start, out of total items — clamping start into [0, total]
// and the window into [0, total].
func ClampRange(start, size, total int) VisibleRange {
	if total < 0 {
		total = 0
	}
	if size < 1 {
		size = 1
	}
	if start < 0 {
		start = 0
	}
	if start > total {
		start = total
	}
	end := start + size
	if end > total {
		end = total
	}
	return VisibleRange{Start: start, End: end, Total: total}
}
