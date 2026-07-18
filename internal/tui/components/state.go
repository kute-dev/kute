package components

// VisibleRange is the [Start,End) row window currently rendered, out of
// Total available — used by Table's scrollbar/footer ("N of M").
type VisibleRange struct {
	Start int
	End   int
	Total int
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
