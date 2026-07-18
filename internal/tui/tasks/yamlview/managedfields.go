// managedFields semantics (docs/design README.md §8a: "managedFields...
// collapse to one dim line — ▸ managedFields (212 lines folded)... ↹
// fold/unfold at cursor, f show all"). kube.GetYAML clears
// metadata.managedFields before marshaling the rest of the object (a
// typical object's YAML would otherwise be dominated by hundreds of noisy
// lines nobody reads by default), so its real content is fetched
// separately (kube.ManagedFieldsYAML, from the same pre-clear object
// load.go already has in hand) and spliced back in here — using the exact
// same fold idiom as any other block, not a permanent, un-unfoldable
// placeholder.
package yamlview

import (
	"fmt"
	"strings"
)

// applyManagedFields inserts managedFields right after "metadata:": a
// "▸ managedFields (N lines folded)" summary while folded["managedFields"]
// is true (the default — see update.go's applyLoaded), or its real,
// separately-fetched content once unfolded. No-op when there's no
// managedFields content (managedFieldsLines empty) or metadata itself is
// folded (its own summary line's Text won't match "metadata:").
func applyManagedFields(rendered []renderLine, managedFieldsLines []string, folded map[string]bool) []renderLine {
	if len(managedFieldsLines) == 0 {
		return rendered
	}
	out := make([]renderLine, 0, len(rendered)+len(managedFieldsLines)+1)
	for _, rl := range rendered {
		out = append(out, rl)
		if rl.Text != "metadata:" || rl.LineNo == 0 {
			continue
		}
		if folded["managedFields"] {
			out = append(out, renderLine{
				Text:    fmt.Sprintf("  ▸ managedFields (%d lines folded)", len(managedFieldsLines)),
				FoldKey: "managedFields",
			})
			continue
		}
		out = append(out, renderLine{Text: "  managedFields:", FoldableKey: "managedFields"})
		for _, l := range managedFieldsLines {
			out = append(out, renderLine{Text: "  " + l})
		}
	}
	return out
}

// splitManagedFieldsLines turns kube.ManagedFieldsYAML's marshaled text
// into the line list applyManagedFields inserts, or nil when there's no
// managedFields content (an empty text — never an error; load.go already
// treats a marshal failure as "no content" rather than surfacing it, since
// managedFields is supplementary detail, not the object itself).
func splitManagedFieldsLines(text string) []string {
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}
