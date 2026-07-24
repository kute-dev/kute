package palette

import (
	"regexp"
	"strings"
	"testing"

	"charm.land/bubbles/v2/textinput"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"

	"github.com/kute-dev/kute/internal/tui/components"
)

// testInput builds a focused textinput.Model with value pre-filled — the
// test-only equivalent of what NewInput + SetStyles + typing would produce,
// since Model.Input replaced the plain Query string field.
func testInput(value string) textinput.Model {
	ti := textinput.New()
	ti.SetValue(value)
	ti.CursorEnd()
	ti.Focus()
	return ti
}

// testStyles builds a Styles value with distinct (if arbitrary) colors —
// tests only assert on text content and layout, never on color. lipgloss
// v2's Render() always emits ANSI now (no ambient no-TTY downsampling the
// way v1 had), so any test whose target text can land inside a styled span
// boundary (an aliased first letter, a fuzzy-matched rune, …) needs
// ansi.Strip on the rendered output before comparing.
func testStyles() Styles {
	return Styles{
		Frame:        lipgloss.NewStyle(),
		Body:         lipgloss.NewStyle(),
		Input:        lipgloss.NewStyle(),
		Prompt:       lipgloss.NewStyle(),
		Placeholder:  lipgloss.NewStyle(),
		Hint:         lipgloss.NewStyle(),
		Match:        lipgloss.NewStyle(),
		Normal:       lipgloss.NewStyle(),
		Dim:          lipgloss.NewStyle(),
		Muted:        lipgloss.NewStyle(),
		Detail:       lipgloss.NewStyle(),
		RightOK:      lipgloss.NewStyle(),
		RightWarn:    lipgloss.NewStyle(),
		RightBad:     lipgloss.NewStyle(),
		SelBar:       lipgloss.NewStyle(),
		SelRow:       lipgloss.NewStyle(),
		SelBg:        lipgloss.NewStyle().Background(lipgloss.Color("#1d1633")),
		Rule:         lipgloss.NewStyle(),
		RecentRule:   lipgloss.NewStyle(),
		RecentLabel:  lipgloss.NewStyle(),
		RecentItem:   lipgloss.NewStyle(),
		KeyRow:       lipgloss.NewStyle(),
		KeyRowKey:    lipgloss.NewStyle(),
		FooterDetail: lipgloss.NewStyle(),
		FooterKey:    lipgloss.NewStyle(),
		FooterEm:     lipgloss.NewStyle(),
		AliasLabel:   lipgloss.NewStyle(),
	}
}

func TestFilterRanksAndPopulatesMatches(t *testing.T) {
	t.Parallel()
	items := []Item{
		{Label: "Deployments"},
		{Label: "Pods"},
		{Label: "PersistentVolumeClaims"},
	}
	got := Filter(items, "pod")
	if len(got) == 0 {
		t.Fatalf("expected at least one match")
	}
	if got[0].Label != "Pods" {
		t.Fatalf("expected best match to be Pods, got %+v", got)
	}
	if len(got[0].Matches) != 3 {
		t.Fatalf("expected 3 matched indexes for exact-ish match, got %v", got[0].Matches)
	}
}

func TestFilterEmptyQueryPassesThrough(t *testing.T) {
	t.Parallel()
	items := []Item{{Label: "a"}, {Label: "b"}}
	got := Filter(items, "")
	if len(got) != 2 {
		t.Fatalf("expected passthrough of 2 items, got %d", len(got))
	}
}

func TestFilterNoMatchesReturnsEmpty(t *testing.T) {
	t.Parallel()
	items := []Item{{Label: "Pods"}, {Label: "Nodes"}}
	got := Filter(items, "zzzzz")
	if len(got) != 0 {
		t.Fatalf("expected no matches, got %d", len(got))
	}
}

func TestModelMoveClampsToBounds(t *testing.T) {
	t.Parallel()
	m := Model{Items: []Item{{Label: "a"}, {Label: "b"}, {Label: "c"}}}
	m.Move(-1)
	if m.Sel != 0 {
		t.Fatalf("Sel = %d, want 0 after moving up from start", m.Sel)
	}
	m.Move(10)
	if m.Sel != 2 {
		t.Fatalf("Sel = %d, want 2 (clamped to last item)", m.Sel)
	}
	m.Move(-1)
	if m.Sel != 1 {
		t.Fatalf("Sel = %d, want 1", m.Sel)
	}
}

func TestModelMoveOnEmptyItems(t *testing.T) {
	t.Parallel()
	m := Model{}
	m.Move(3)
	if m.Sel != 0 {
		t.Fatalf("Sel = %d, want 0 for empty item list", m.Sel)
	}
}

func TestModelSelected(t *testing.T) {
	t.Parallel()
	m := Model{Items: []Item{{Label: "a"}, {Label: "b"}}, Sel: 1}
	item, ok := m.Selected()
	if !ok || item.Label != "b" {
		t.Fatalf("Selected() = %+v, %v, want b, true", item, ok)
	}

	empty := Model{}
	if _, ok := empty.Selected(); ok {
		t.Fatalf("Selected() on empty model should report false")
	}
}

func TestWidthIsFixedAcrossStates(t *testing.T) {
	t.Parallel()
	// One fixed width (~69%) for both the browse grid and fuzzy results —
	// the panel must not resize when the user starts typing.
	if w := Width(100); w != 69 {
		t.Fatalf("width = %d, want 69", w)
	}
}

func TestWidthFloorsAtMinimum(t *testing.T) {
	t.Parallel()
	if w := Width(50); w != 40 {
		t.Fatalf("width = %d, want floor of 40", w)
	}
}

func TestWidthNeverExceedsScreen(t *testing.T) {
	t.Parallel()
	if w := Width(30); w != 30 {
		t.Fatalf("width = %d, want capped at screen width 30", w)
	}
}

func TestRenderFuzzyShowsPromptQueryAndResults(t *testing.T) {
	t.Parallel()
	m := Model{
		Scope:  ScopeGoto,
		Prompt: "›",
		Input:  testInput("pod"),
		Hint:   "jump anywhere",
		Items: []Item{
			{Label: "Pods", Detail: "kind · Workloads", Right: "12"},
			{Label: "PodDisruptionBudgets", Detail: "kind · Workloads", Right: "0"},
		},
		Sel: 1,
	}
	got := m.Render(testStyles(), 120)
	if !strings.Contains(got, "pod") {
		t.Fatalf("expected query in output:\n%s", got)
	}
	if !strings.Contains(got, "jump anywhere") {
		t.Fatalf("expected hint in output:\n%s", got)
	}
	if !strings.Contains(got, "Pods") || !strings.Contains(got, "PodDisruptionBudgets") {
		t.Fatalf("expected both result labels in output:\n%s", got)
	}
	if !strings.Contains(got, "↵ jump") {
		t.Fatalf("expected fuzzy key row, got:\n%s", got)
	}
}

// TestRenderBrowseHighlightsAliasFirstLetterAndNarrowKeyRow covers 12a's
// empty-query ranked list: aliased kinds carry Matches=[0] so their first
// letter renders through the Match style (no chip glyph, no gutter column),
// and the key row swaps to "type to narrow" (no "tab complete") while
// Browse.
func TestRenderBrowseHighlightsAliasFirstLetterAndNarrowKeyRow(t *testing.T) {
	t.Parallel()
	m := Model{
		Scope:  ScopeGoto,
		Browse: true,
		Items: []Item{
			{Label: "Pods", Right: "12", Matches: []int{0}},
			{Label: "Deployments", Right: "3", Matches: []int{0}},
			{Label: "Secrets", Right: "0", Dim: true},
		},
	}
	got := ansi.Strip(m.Render(testStyles(), 120))
	if !strings.Contains(got, "type") || !strings.Contains(got, "to narrow") {
		t.Fatalf("expected the 12a key row, got:\n%s", got)
	}
	if strings.Contains(got, "tab") {
		t.Fatalf("did not expect the 2b tab-complete hint in Browse mode:\n%s", got)
	}
	if !strings.Contains(got, "Pods") || !strings.Contains(got, "Secrets") {
		t.Fatalf("expected the ranked list items in output:\n%s", got)
	}
}

// TestRenderPinnedAliasKeyRow covers 12b's key row: with an alias match
// pinned, the hints swap to "↵ jump · ↑↓ move · type to keep narrowing"
// (no tab-complete).
func TestRenderPinnedAliasKeyRow(t *testing.T) {
	t.Parallel()
	m := Model{
		Scope: ScopeGoto,
		Input: testInput("d"),
		Items: []Item{
			{Label: "Deployments", Right: "3", Matches: []int{0}, AliasMatch: true},
			{Label: "DaemonSets", Right: "2"},
		},
	}
	got := m.Render(testStyles(), 120)
	if !strings.Contains(got, "to keep narrowing") {
		t.Fatalf("expected the 12b 'type to keep narrowing' key hint:\n%s", got)
	}
	if strings.Contains(got, "tab complete") {
		t.Fatalf("did not expect the tab-complete hint while an alias match is pinned:\n%s", got)
	}
}

// TestRenderShowsAliasMatchOnPinnedRow covers 12b: a pinned alias-matched
// row highlights its first letter and appends the "alias match" label.
func TestRenderShowsAliasMatchOnPinnedRow(t *testing.T) {
	t.Parallel()
	m := Model{
		Scope: ScopeGoto,
		Input: testInput("d"),
		Items: []Item{
			{Label: "Deployments", Right: "3", Matches: []int{0}, AliasMatch: true},
			{Label: "DaemonSets", Right: "2"},
		},
		Sel: 0,
	}
	got := m.Render(testStyles(), 120)
	if !strings.Contains(got, "alias match") {
		t.Fatalf("expected the 'alias match' label on the pinned row:\n%s", got)
	}
	if !strings.Contains(got, "DaemonSets") {
		t.Fatalf("expected the normal fuzzy match to still be listed below:\n%s", got)
	}
}

// TestRenderNoteLineIsPlainAndNotSelectable covers 12a's "+ N more kinds"
// trailer: it renders as plain text and Move skips over it.
func TestRenderNoteLineIsPlainAndNotSelectable(t *testing.T) {
	t.Parallel()
	m := Model{Items: []Item{
		{Label: "Pods"},
		{Label: "Deployments"},
		{Note: "+ 4 more kinds · type to narrow"},
	}}
	got := m.Render(testStyles(), 120)
	if !strings.Contains(got, "+ 4 more kinds · type to narrow") {
		t.Fatalf("expected the Note trailer line in output:\n%s", got)
	}
	m.Sel = 1 // Deployments
	m.Move(1)
	if got := m.Items[m.Sel].Label; got != "Deployments" {
		t.Fatalf("Move(1) landed on %q, want to stay on Deployments (Note isn't selectable)", got)
	}
}

func TestMoveSkipsNoteLines(t *testing.T) {
	t.Parallel()
	m := Model{Items: []Item{
		{Label: "Pods"},
		{Note: "+ 4 more kinds · type to narrow"},
		{Label: "Deployments"},
	}}
	m.Sel = 0 // Pods
	m.Move(1)
	if got := m.Items[m.Sel].Label; got != "Deployments" {
		t.Fatalf("Move(1) landed on %q, want Deployments (Note skipped)", got)
	}
	m.Move(-1)
	if got := m.Items[m.Sel].Label; got != "Pods" {
		t.Fatalf("Move(-1) landed on %q, want Pods (Note skipped)", got)
	}
}

func TestRenderFooterShowsModelFooterLine(t *testing.T) {
	t.Parallel()
	m := Model{
		Scope:  ScopeGoto,
		Browse: true,
		Items:  []Item{{Label: "Pods", Matches: []int{0}}},
		Footer: []FooterSpan{
			{Text: "alias", Tone: FooterKey},
			{Text: " — typing an alias letter pins that kind to rank 1 · ↵ jumps"},
		},
	}
	got := m.Render(testStyles(), 120)
	if !strings.Contains(got, "alias — typing an alias letter pins that kind to rank 1") {
		t.Fatalf("expected Model.Footer text in output:\n%s", got)
	}
}

func TestRenderFooterAbsentWhenEmpty(t *testing.T) {
	t.Parallel()
	m := Model{
		Items: []Item{{Label: "Pods"}},
		Sel:   0,
	}
	got := m.Render(testStyles(), 120)
	if strings.Contains(got, "alias") {
		t.Fatalf("did not expect a footer line when Model.Footer is empty:\n%s", got)
	}
}

func TestRenderShowsRecentSection(t *testing.T) {
	t.Parallel()
	m := Model{
		Items:  []Item{{Label: "Pods"}},
		Recent: []string{"Deployments", "Services"},
	}
	got := m.Render(testStyles(), 120)
	if !strings.Contains(got, "RECENT") {
		t.Fatalf("expected RECENT section, got:\n%s", got)
	}
	if !strings.Contains(got, "Deployments") {
		t.Fatalf("expected recent item listed, got:\n%s", got)
	}
}

// TestRenderRecentNumGutterOnUnselectedRowOnly pins 6a/7a's numbered
// recent-pick gutter (docs/design README.md §6a/§7a): an unselected row with
// Item.RecentNum > 0 shows its digit in the leading gutter cell instead of a
// blank; the selected row keeps showing the cursor accent bar there instead
// (it's already the current pick, a digit would be redundant). Exercises
// both the free-form path (7a's renderResultLine) and the fixed-column path
// (6a's renderColumnsResultLine, via ColumnHeaders).
func TestRenderRecentNumGutterOnUnselectedRowOnly(t *testing.T) {
	t.Parallel()

	t.Run("free-form (7a)", func(t *testing.T) {
		t.Parallel()
		m := Model{
			Items: []Item{
				{Label: "dev"},
				{Label: "prod-eks", RecentNum: 1},
			},
			Sel: 0,
		}
		got := m.Render(testStyles(), 80)
		// The digit renders immediately in the gutter cell right before the
		// label starts — assert loosely on adjacency since exact
		// inter-cell spacing is an implementation detail.
		if !regexp.MustCompile(`1\s*prod-eks`).MatchString(got) {
			t.Fatalf("expected digit '1' in prod-eks's gutter cell, got:\n%s", got)
		}
		if regexp.MustCompile(`\d\s*dev`).MatchString(got) {
			t.Fatalf("expected no digit before the selected row (dev), got:\n%s", got)
		}
	})

	t.Run("fixed-column (6a)", func(t *testing.T) {
		t.Parallel()
		m := Model{
			Items: []Item{
				{Label: "default", Cols: []Cell{{Text: "4"}, {Text: "–"}, {Text: "–"}}},
				{Label: "staging", Cols: []Cell{{Text: "0"}, {Text: "–"}, {Text: "–"}}, RecentNum: 2},
			},
			Sel:             0,
			ColumnHeaders:   []ColumnHeader{{Label: "PODS", Width: 5}, {Label: "HEALTH", Width: 13}, {Label: "CPU", Width: 5, Align: components.AlignRight}},
			NameColumnLabel: "NAMESPACE",
			GutterGlyph:     "▸",
		}
		got := m.Render(testStyles(), 90)
		if !regexp.MustCompile(`2\s*staging`).MatchString(got) {
			t.Fatalf("expected digit '2' in staging's gutter cell, got:\n%s", got)
		}
		if regexp.MustCompile(`\d\s*default`).MatchString(got) {
			t.Fatalf("expected no digit before the selected row (default), got:\n%s", got)
		}
	})
}

// TestRenderInputRowTruncatesHintInsteadOfDropping pins the padBetweenStyled
// fix: a right-hand hint that doesn't fit alongside the input row's left
// side (prompt + placeholder/query + cursor) must ellipsize, not disappear
// outright — 7a's real right hint ("~/.kube/config · N contexts") could
// silently vanish with a longer-than-~/.kube/config $KUBECONFIG path (any
// repo-relative override, for instance), which reads as a bug, not a
// graceful degrade.
func TestRenderInputRowTruncatesHintInsteadOfDropping(t *testing.T) {
	t.Parallel()
	m := Model{
		Input: testInput(""),
		Hint:  "a very long right-hand hint that will not fit in a narrow panel at all",
		Items: []Item{{Label: "Pods"}},
	}
	got := m.Render(testStyles(), 44) // narrow screen ⇒ narrow panel (Width floors at 40)
	if !strings.Contains(got, "…") {
		t.Fatalf("expected the overlong hint to ellipsize rather than vanish:\n%s", got)
	}
	if strings.Contains(got, m.Hint) {
		t.Fatalf("expected the hint to be truncated, not rendered in full:\n%s", got)
	}
}

// TestRenderRecentReservesSpaceForHint pins the renderRecent fix: a full
// RECENT list (state.MaxRecent = 8 real names) must not push RecentHint off
// the line — entries are dropped first (with at least one always kept) so
// the hint stays legible instead of getting silently truncated away along
// with a chunk of the last entry's name.
func TestRenderRecentReservesSpaceForHint(t *testing.T) {
	t.Parallel()
	m := Model{
		Items:  []Item{{Label: "Pods"}},
		Recent: []string{"shop-frontend", "monitoring", "cert-manager", "data-platform", "kube-system", "batch-jobs", "shop-checkout", "nva-qa"},
		RecentHint: []FooterSpan{
			{Text: "1-9", Tone: FooterKey},
			{Text: " pick · ", Tone: FooterDim},
			{Text: "↵", Tone: FooterKey},
			{Text: " toggles last", Tone: FooterDim},
		},
	}
	got := m.Render(testStyles(), 84) // Width(84) ⇒ frame ~58 cols, tight enough to force a drop
	if !strings.Contains(got, "toggles last") {
		t.Fatalf("expected the RecentHint to survive a full 8-entry RECENT row:\n%s", got)
	}
	if strings.Contains(got, "nva-qa") {
		t.Fatalf("expected the last (least-recent) entries to be dropped to make room, not shown truncated:\n%s", got)
	}
	if !strings.Contains(got, "shop-frontend") {
		t.Fatalf("expected at least the most-recent entry to still be shown:\n%s", got)
	}
}

func TestRenderNoMatchesFuzzy(t *testing.T) {
	t.Parallel()
	m := Model{Input: testInput("zzz")}
	got := m.Render(testStyles(), 120)
	if !strings.Contains(got, "no matches") {
		t.Fatalf("expected empty-state text, got:\n%s", got)
	}
}

func TestRenderRespectsWidth(t *testing.T) {
	t.Parallel()
	m := Model{Items: []Item{{Label: "Pods"}}}
	got := ansi.Strip(m.Render(testStyles(), 100))
	lines := strings.Split(got, "\n")
	want := Width(100)
	for i, l := range lines {
		if w := runewidth.StringWidth(l); w != want {
			t.Fatalf("line %d width = %d, want %d: %q", i, w, want, l)
		}
	}
}
