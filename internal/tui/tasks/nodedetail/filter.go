package nodedetail

import "github.com/sahilm/fuzzy"

// applyPodFilter fuzzy-matches query against each row's pod name — same
// library/behavior as browse's own filter (applyFilter), duplicated per the
// repo's package-local-seam convention. An empty query returns rows
// unchanged.
func applyPodFilter(rows []nodePodRow, query string) []nodePodRow {
	if query == "" {
		return rows
	}
	names := make([]string, len(rows))
	for i, r := range rows {
		names[i] = r.pod.Name
	}
	found := fuzzy.Find(query, names)
	out := make([]nodePodRow, 0, len(found))
	for _, m := range found {
		out = append(out, rows[m.Index])
	}
	return out
}
