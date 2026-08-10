package tui

// Status glyphs, per docs/design/README.md §Design Tokens. Views reference
// these constants rather than inlining the Unicode runes, so a future ASCII
// fallback (terminal degradation, deferred post-MVP) is a one-file change.
const (
	GlyphRunning   = "●"
	GlyphPending   = "▲"
	GlyphFailed    = "✕"
	GlyphCompleted = "○"
	GlyphProbing   = "◌"
	GlyphRestarts  = "↺"
	GlyphStale     = "⧗"
	GlyphFollowing = "▶"
	GlyphWarning   = "▲"
	GlyphCordoned  = "◈"
	GlyphAllNS     = "∗"
	GlyphSelBar    = "▎"
	GlyphExpand    = "▸"
	GlyphCollapse  = "▾"
	GlyphTab       = "↹"
	GlyphForward   = "⇄"
	GlyphRollout   = "⇅"
	// GlyphMarked is 20a's bulk-operations mark glyph (`▪`), rendered in the
	// table's leading mark column and the health strip's "▪ N marked" segment.
	GlyphMarked = "▪"

	// GlyphBrandMark is the header wordmark's "❯" chevron, prefixing "kute"
	// in every screen's Header() via BrandCrumbs (chrome.go).
	GlyphBrandMark = "❯"

	// GlyphRevision is §32a's git-revision marker in the incident timeline
	// (`◆`) — the only accent-purple marker in that feed, because on a
	// GitOps cluster a commit is the answer to "what changed?".
	GlyphRevision = "◆"
	// GlyphSelectorJoin is 26a's inline "a Service selector matches this
	// label" warning glyph (`⚠`), rendered ahead of the joined label row's
	// "selector · svc/x" note.
	GlyphSelectorJoin = "⚠"

	// GlyphSuspended is §30a/§30b's paused-reconciliation marker (`‖`) — the
	// one status glyph Flux adds. resources/flux.go can't reference it (that
	// package must not import tui, see resources.go's own note), so the
	// projection carries the same rune as a literal and this constant serves
	// the tui-side surfaces: §30b's strip, and any future one.
	GlyphSuspended = "‖"

	// GlyphExpiring is §35b's cross-cutting "<30d" health-strip segment
	// glyph (`◷`) for a ready-but-expiring Certificate — browse/view.go's
	// own use, same reasoning as GlyphSuspended: resources/certmanager.go
	// carries the identical rune as a literal on the row itself.
	GlyphExpiring = "◷"

	// GlyphCronActive is v0.8.0 §36a/§36c's amber "job currently active"
	// marker (`◐`) — a CronJob row's status glyph while ACT>0, and the
	// browse health strip's cross-cutting "active now" segment (this
	// outranks a healthy/failed LAST RUN reading in the row's own glyph
	// priority, per docs/design README.md §4.3, even though the strip's
	// Status tally still counts the row by its last *terminal* outcome).
	// resources/cronjobs.go can't reference this constant directly (that
	// package must not import tui, per resources.go's own note) so it
	// carries the identical rune as a literal; this is the source-of-truth
	// documented on both sides.
	GlyphCronActive = "◐"

	// GlyphCronPaused is v0.8.0 §36a/§36c's dim "schedule suspended" marker
	// (`⏸`) — a suspended CronJob's row glyph and the health strip's
	// "suspended" segment. Deliberately a different rune from
	// GlyphSuspended (`‖`) above: that one is Flux's §30a/§30b
	// paused-reconciliation glyph, a different kind's status vocabulary
	// even though both describe "not running right now, on purpose".
	// resources/cronjobs.go carries the identical rune as a literal, same
	// reasoning as GlyphCronActive.
	GlyphCronPaused = "⏸"
)
