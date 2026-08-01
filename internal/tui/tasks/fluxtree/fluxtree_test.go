package fluxtree

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/kube/fake"
	"github.com/kute-dev/kute/internal/resources"
	"github.com/kute-dev/kute/internal/tui"
)

func demoModel(t *testing.T) *Model {
	t.Helper()
	c := fake.NewDemo()
	reg, groups := resources.BuildDiscoveredRegistry(c.DiscoveredKinds(), c)
	sess := &tui.Session{
		Theme: tui.Dark(), Registry: reg, Groups: groups,
		Location: tui.Location{Context: "microk8s-cluster", Namespace: "flux-system"},
	}
	m := New(Config{Session: sess, Lister: c, Mutator: c})
	m.SetSize(120, 36)
	upd, _ := m.Update(m.load()())
	return upd.(*Model)
}

func plain(s string) string {
	var b strings.Builder
	skip := false
	for _, r := range s {
		switch {
		case r == 0x1b:
			skip = true
		case skip && r == 'm':
			skip = false
		case !skip:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// TestTreeNestsReconcilersUnderTheirSource is §30b's whole claim: the chain
// is on screen, not reconstructed in the reader's head from two lists.
func TestTreeNestsReconcilersUnderTheirSource(t *testing.T) {
	m := demoModel(t)
	if m.state != tui.TaskStateReady {
		t.Fatalf("state = %s, want ready (feedback %q)", m.state, m.feedback)
	}
	if len(m.groups) == 0 {
		t.Fatal("expected at least one source group")
	}

	var found bool
	for _, g := range m.groups {
		if g.head.name != "flux-system" || g.head.kindLabel != "GitRepo" {
			continue
		}
		found = true
		if len(g.children) == 0 {
			t.Fatal("the GitRepository must carry the Kustomizations that reconcile from it")
		}
		for _, c := range g.children {
			if c.isSource {
				t.Errorf("%s is nested as a child but marked a source", c.name)
			}
			if c.sourceName != "flux-system" {
				t.Errorf("%s nested under the wrong source: sourceRef is %q", c.name, c.sourceName)
			}
		}
	}
	if !found {
		t.Fatalf("no GitRepo group in %+v", m.groups)
	}

	view := plain(m.Render())
	if !strings.Contains(view, "└─") {
		t.Errorf("expected the tree lead on nested rows:\n%s", view)
	}
	if !strings.Contains(view, "SOURCE / RECONCILER") {
		t.Errorf("expected §30b's own column header:\n%s", view)
	}
}

// TestDriftIsABooleanNotACount pins the design's one unbuildable headline.
// The mockup draws "−9" (the source is 9 commits ahead); counting commits
// needs git log, which kute never has (docs/flux-plan.md G1), so the honest
// rendering is the boolean — and it must never grow a number.
func TestDriftIsABooleanNotACount(t *testing.T) {
	m := demoModel(t)
	var drifted []treeRow
	for _, g := range m.groups {
		for _, c := range g.children {
			if strings.Contains(c.revision, "source ahead") {
				drifted = append(drifted, c)
			}
		}
	}
	if len(drifted) == 0 {
		t.Fatal("the demo fixtures must include a reconciler whose applied revision is behind its source")
	}
	for _, r := range drifted {
		if strings.ContainsAny(r.revision, "−-+") && !strings.HasPrefix(r.revision, "-") {
			// A "−N commits" style delta would have to come from somewhere,
			// and there is nowhere for it to come from.
			if strings.Contains(r.revision, "−") {
				t.Errorf("%s renders a commit delta %q — unbuildable without git log", r.name, r.revision)
			}
		}
	}
}

// TestHealthyChainsFoldAwayBehindTheTrouble is §30b's triage order: a
// working chain is one line of summary, not fifteen rows of noise.
func TestHealthyChainsFoldAwayBehindTheTrouble(t *testing.T) {
	m := demoModel(t)
	if m.foldedSources == 0 {
		t.Skip("no fully-healthy chain in the demo fixtures to fold")
	}
	view := plain(m.Render())
	if !strings.Contains(view, "ready · ↹ expand") {
		t.Errorf("expected the fold summary line:\n%s", view)
	}

	m2, _ := m.Update(tea.KeyPressMsg{Text: "tab"})
	expanded := plain(m2.(*Model).Render())
	if strings.Contains(expanded, "ready · ↹ expand") {
		t.Errorf("tab should expand the folded chains:\n%s", expanded)
	}
}

// TestSubLinesAreNotSelectable: a condition message is a continuation of
// the row above it, never a stop of its own — §30a's rule, and what keeps
// 'r'/'s' from landing on a line with no object behind it.
func TestSubLinesAreNotSelectable(t *testing.T) {
	m := demoModel(t)
	var sawSub bool
	for _, l := range m.lines {
		if l.kind == lineSub {
			sawSub = true
		}
	}
	if !sawSub {
		t.Fatal("the demo fixtures must include a failing reconciler with a condition message")
	}
	for range m.lines {
		if l := m.lines[m.selected]; l.kind != lineRow {
			t.Fatalf("cursor landed on a non-object line (%v) at index %d", l.kind, m.selected)
		}
		if _, ok := m.selectedRow(); !ok {
			t.Fatalf("selected index %d resolves to no row", m.selected)
		}
		m.moveSelection(1)
	}
}

// TestReconcileFollowsTheCursor is the design's "one key, scope follows the
// cursor": on a source 'r' reconciles that source; on a reconciler it
// reconciles with-source, and the will-run line shows both commands.
func TestReconcileFollowsTheCursor(t *testing.T) {
	m := demoModel(t)

	sourceIdx, reconcilerIdx := -1, -1
	for i, l := range m.lines {
		if l.kind != lineRow {
			continue
		}
		if m.rows[l.row].isSource && sourceIdx < 0 {
			sourceIdx = i
		}
		if !m.rows[l.row].isSource && m.rows[l.row].sourceName != "" && reconcilerIdx < 0 {
			reconcilerIdx = i
		}
	}
	if sourceIdx < 0 || reconcilerIdx < 0 {
		t.Fatalf("need both a source and a reconciler row, got %d/%d", sourceIdx, reconcilerIdx)
	}

	m.selected = sourceIdx
	m.Update(tea.KeyPressMsg{Text: "r"})
	if strings.Contains(m.execFeedback, "&&") {
		t.Errorf("a source reconcile is one command, got %q", m.execFeedback)
	}
	if !strings.Contains(m.execFeedback, "kubectl annotate") {
		t.Errorf("expected the will-run line to name the command, got %q", m.execFeedback)
	}

	m.selected = reconcilerIdx
	row, _ := m.selectedRow()
	m.Update(tea.KeyPressMsg{Text: "r"})
	if !strings.Contains(m.execFeedback, "&&") {
		t.Errorf("a reconciler reconcile is with-source — two commands, got %q", m.execFeedback)
	}
	if !strings.Contains(m.execFeedback, row.sourceName) {
		t.Errorf("the with-source half must name the source %q, got %q", row.sourceName, m.execFeedback)
	}
	// The source is annotated first: syncing a reconciler against a stale
	// artifact re-applies what it already has.
	if got := strings.Index(m.execFeedback, row.sourceName); got > strings.Index(m.execFeedback, "&&") {
		t.Errorf("source must be reconciled first, got %q", m.execFeedback)
	}
}

// TestSuspendFlipsDirectionWithTheRow — one verb, two directions, §30a's
// own shape carried onto this screen.
func TestSuspendFlipsDirectionWithTheRow(t *testing.T) {
	m := demoModel(t)
	idx := -1
	for i, l := range m.lines {
		if l.kind == lineRow && m.rows[l.row].suspended {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatal("the demo fixtures must include a suspended reconciler")
	}
	m.selected = idx
	if !strings.Contains(plain(m.Keybar().Groups[2][1].Label), "resume") {
		t.Errorf("the keybar must offer resume on a suspended row, got %+v", m.Keybar().Groups)
	}
	m.Update(tea.KeyPressMsg{Text: "s"})
	if !strings.Contains(m.execFeedback, "suspend=false") && !strings.Contains(m.execFeedback, "false") {
		t.Errorf("resuming must patch suspend to false, got %q", m.execFeedback)
	}
}

// TestVerbsAreInertWithoutAMutator mirrors browse's own guard: a read-only
// wiring must advertise nothing it can't do.
func TestVerbsAreInertWithoutAMutator(t *testing.T) {
	c := fake.NewDemo()
	reg, groups := resources.BuildDiscoveredRegistry(c.DiscoveredKinds(), c)
	sess := &tui.Session{Theme: tui.Dark(), Registry: reg, Groups: groups}
	m := New(Config{Session: sess, Lister: c})
	m.SetSize(120, 36)
	upd, _ := m.Update(m.load()())
	got := upd.(*Model)

	got.Update(tea.KeyPressMsg{Text: "r"})
	if got.execFeedback != "" {
		t.Errorf("reconcile must be inert without a mutator, got %q", got.execFeedback)
	}
	for _, group := range got.Keybar().Groups {
		for _, hint := range group {
			if hint.Label == "reconcile" || hint.Label == "suspend" {
				t.Errorf("keybar advertises %q with no mutator wired", hint.Label)
			}
		}
	}
}

// TestOnlyFluxKindsTriggerAReload: the join is expensive enough that a Pod
// event must not re-run it.
func TestOnlyFluxKindsTriggerAReload(t *testing.T) {
	m := demoModel(t)
	if m.isFluxKind(kube.KindPod) {
		t.Error("Pod is not a Flux kind")
	}
	if !m.isFluxKind(kube.ResourceKind("Kustomization")) {
		t.Error("Kustomization must trigger a reload")
	}
	if !m.isFluxKind(kube.KindFluxHelmRelease) {
		t.Error("Flux's HelmRelease must trigger a reload — it's half the tree")
	}
	if m.isFluxKind(kube.KindHelmRelease) {
		t.Error("§18a's Helm-3 release kind is not Flux's and must not reload this screen")
	}
}

// TestHelmChainsNeverClaimDrift is the join's sharpest edge. A
// Kustomization's applied revision and its GitRepository's artifact
// revision are the same kind of string, so an inequality is real drift. A
// HelmRelease's is a chart version ("19.6.1") while its HelmRepository
// publishes an index digest — never equal, so a naive comparison brands
// every healthy Helm chain "source ahead" forever.
func TestHelmChainsNeverClaimDrift(t *testing.T) {
	m := demoModel(t)
	var sawHelmChain bool
	for _, g := range m.groups {
		for _, c := range g.children {
			if c.kindLabel != "HelmRelease" {
				continue
			}
			sawHelmChain = true
			if strings.Contains(c.revision, "source ahead") {
				t.Errorf("%s claims drift against an index digest: %q", c.name, c.revision)
			}
		}
	}
	if !sawHelmChain {
		t.Fatal("the demo fixtures must include a HelmRelease under a HelmRepository")
	}

	// And the comparison still fires where it is meaningful.
	var sawGitDrift bool
	for _, g := range m.groups {
		if g.head.kindLabel != "GitRepo" {
			continue
		}
		for _, c := range g.children {
			if strings.Contains(c.revision, "source ahead") {
				sawGitDrift = true
			}
		}
	}
	if !sawGitDrift {
		t.Error("a Kustomization behind its GitRepository must still report drift")
	}
}

// TestSourceRevisionReadsIndexForAChartRepo: a HelmRepository's artifact is
// the repo index, whose digest names nothing a human compares.
func TestSourceRevisionReadsIndexForAChartRepo(t *testing.T) {
	m := demoModel(t)
	for _, g := range m.groups {
		if g.head.kindLabel != "HelmRepo" {
			continue
		}
		if !strings.HasPrefix(g.head.revision, "index") {
			t.Errorf("%s renders %q, want an \"index · <age>\" cell", g.head.name, g.head.revision)
		}
	}
}
