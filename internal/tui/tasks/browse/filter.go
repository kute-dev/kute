package browse

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/sahilm/fuzzy"

	"github.com/kute-dev/kute/internal/resources"
)

// filterMatch is one row that survived the filter, with its fuzzy match
// indexes (into the row's Name) for highlight rendering.
type filterMatch struct {
	row     resources.Row
	matches []int
}

// applyFilter fuzzy-matches query against each row's Name (sahilm/fuzzy,
// the same library and behavior as the jump palette), ranked by score. An
// empty query matches every row with no highlight indexes.
func applyFilter(rows []resources.Row, query string) []filterMatch {
	if query == "" {
		out := make([]filterMatch, len(rows))
		for i, r := range rows {
			out[i] = filterMatch{row: r}
		}
		return out
	}
	names := make([]string, len(rows))
	for i, r := range rows {
		names[i] = r.Name
	}
	found := fuzzy.Find(query, names)
	out := make([]filterMatch, 0, len(found))
	for _, m := range found {
		out = append(out, filterMatch{row: rows[m.Index], matches: m.MatchedIndexes})
	}
	return out
}

// highlightName renders name with matched rune positions styled matchStyle
// and the rest styled base — the table-row equivalent of the jump
// palette's match highlighting.
func highlightName(name string, matches []int, matchStyle, base lipgloss.Style) string {
	if len(matches) == 0 {
		return base.Render(name)
	}
	set := make(map[int]bool, len(matches))
	for _, i := range matches {
		set[i] = true
	}
	var b strings.Builder
	for i, r := range []rune(name) {
		if set[i] {
			b.WriteString(matchStyle.Render(string(r)))
		} else {
			b.WriteString(base.Render(string(r)))
		}
	}
	return b.String()
}
