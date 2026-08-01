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

	// GlyphRevision is §32a's git-revision marker in the incident timeline
	// (`◆`) — the only accent-purple marker in that feed, because on a
	// GitOps cluster a commit is the answer to "what changed?".
	GlyphRevision = "◆"
	// GlyphSelectorJoin is 26a's inline "a Service selector matches this
	// label" warning glyph (`⚠`), rendered ahead of the joined label row's
	// "selector · svc/x" note.
	GlyphSelectorJoin = "⚠"
)
