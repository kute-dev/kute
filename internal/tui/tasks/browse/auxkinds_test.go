package browse

import (
	"context"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/resources"
	"github.com/kute-dev/kute/internal/tui"
)

// recordingLister notes every kind read, so a test can assert what a screen
// warmed rather than what it rendered.
type recordingLister struct {
	mu    sync.Mutex
	kinds []kube.ResourceKind
	inner fakeLister
}

func (l *recordingLister) ListRaw(ctx context.Context, kind kube.ResourceKind, ns string) ([]runtime.Object, error) {
	l.mu.Lock()
	l.kinds = append(l.kinds, kind)
	l.mu.Unlock()
	return l.inner.ListRaw(ctx, kind, ns)
}

func (l *recordingLister) read(kind kube.ResourceKind) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, k := range l.kinds {
		if k == kind {
			return true
		}
	}
	return false
}

// TestAuxKindsReloadTheList: browse subscribed only to the kind it displays,
// but several lists are built from more than one cache. A Deployment's IMAGE
// cell compares against ReplicaSets, so a rollout has to reload the list.
func TestAuxKindsReloadTheList(t *testing.T) {
	m := New(Config{Session: newSession(), Lister: fakeLister{}})
	m.SetSize(120, 36)
	m.kind = kube.KindDeployment

	before := m.reloadEpoch
	_, cmd := m.Update(kube.ResourceChangedMsg{Kind: kube.KindReplicaSet})
	if cmd == nil || m.reloadEpoch == before {
		t.Fatal("a ReplicaSet change must reload the Deployments list — it feeds the IMAGE column")
	}
}

func TestUnrelatedKindDoesNotReloadTheList(t *testing.T) {
	m := New(Config{Session: newSession(), Lister: fakeLister{}})
	m.SetSize(120, 36)
	m.kind = kube.KindDeployment

	before := m.reloadEpoch
	_, cmd := m.Update(kube.ResourceChangedMsg{Kind: kube.KindCronJob})
	if cmd != nil || m.reloadEpoch != before {
		t.Fatal("a CronJob change has nothing to do with the Deployments list")
	}
}

func TestAuxKindOfIsDirectional(t *testing.T) {
	t.Parallel()
	if !auxKindOf(kube.KindPod, kube.KindNode) {
		t.Error("the Pods health strip counts Nodes")
	}
	if !auxKindOf(kube.KindNode, kube.KindPod) {
		t.Error("the Nodes list counts Pods per node")
	}
	if auxKindOf(kube.KindSecret, kube.KindNode) {
		t.Error("Secrets don't read Nodes")
	}
}

// TestPrefetchWarmsAuxCaches: with informers starting on first read, the
// synchronous prompt handlers (rollout history, the scale prompt) would
// otherwise hit an empty cache on first keypress and render a plausible
// wrong answer instead of the truth.
func TestPrefetchWarmsAuxCaches(t *testing.T) {
	lister := &recordingLister{inner: fakeLister{objs: map[kube.ResourceKind][]runtime.Object{}}}
	m := New(Config{Session: newSession(), Lister: lister})
	m.SetSize(120, 36)
	m.kind = kube.KindDeployment
	m.desc, _ = m.session.Registry.Descriptor(kube.KindDeployment)

	cmd := m.resetAndLoad()
	if cmd == nil {
		t.Fatal("expected resetAndLoad to return commands")
	}
	drain(cmd)

	for _, kind := range []kube.ResourceKind{kube.KindReplicaSet, kube.KindHorizontalPodAutoscaler} {
		if !lister.read(kind) {
			t.Errorf("opening the Deployments list did not warm the %s cache", kind)
		}
	}
}

// TestHelmListDoesNotWarmTheSharedSecretCache: releases come from their own
// server-side-filtered cache (docs/lazy-informers.md §5.2), so the Helm list
// must not touch KindSecret at all. Reading it here starts the shared,
// unfiltered cluster-wide Secret informer — 12.3 MB on the cluster this was
// measured against — for a screen that never reads a single object from it,
// and on a slow link that read is what the Helm list ends up queued behind.
func TestHelmListDoesNotWarmTheSharedSecretCache(t *testing.T) {
	lister := &recordingLister{inner: fakeLister{objs: map[kube.ResourceKind][]runtime.Object{}}}
	m := New(Config{Session: newSession(), Lister: lister})
	m.SetSize(120, 36)
	m.kind = kube.KindHelmRelease
	m.desc, _ = m.session.Registry.Descriptor(kube.KindHelmRelease)

	drain(m.resetAndLoad())

	if lister.read(kube.KindSecret) {
		t.Error("opening the Helm list read KindSecret, starting the shared Secret cache it never reads from")
	}
	if auxKindOf(kube.KindHelmRelease, kube.KindSecret) {
		t.Error("a Secret change must not reload the Helm list; the release cache emits KindHelmRelease itself")
	}
}

// drain runs a command and any batched children, so prefetch side effects
// land before assertions.
func drain(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var wg sync.WaitGroup
		for _, c := range batch {
			if c == nil {
				continue
			}
			wg.Add(1)
			go func(c tea.Cmd) { defer wg.Done(); c() }(c)
		}
		wg.Wait()
	}
}

// TestCRDChangePicksUpNewPrinterColumns covers the visible half of lazy CRD
// discovery. A custom kind first renders with neutral NAME/AGE columns,
// because its printer columns live in the part of a CRD that is megabytes
// and are fetched only when the kind is actually opened. When they land, the
// root shell rebuilds the registry and this screen has to notice.
func TestCRDChangePicksUpNewPrinterColumns(t *testing.T) {
	sess := newSession()
	widget := kube.DiscoveredKind{
		GVR:  schema.GroupVersionResource{Group: "example.com", Version: "v1", Resource: "widgets"},
		Kind: "Widget", Plural: "widgets", Group: "example.com", Established: true,
	}
	// Connect-time state: discovered, but columns not fetched yet.
	sess.Registry.Register(resources.CustomDescriptor(widget))

	m := New(Config{Session: sess, Lister: fakeLister{}})
	m.SetSize(120, 36)
	m.kind = kube.ResourceKind("Widget")
	m.desc, _ = sess.Registry.Descriptor(m.kind)

	if got := len(m.desc.Columns); got != 2 {
		t.Fatalf("precondition: a column-less custom kind should render Name/Age, got %d columns", got)
	}

	// The columns arrive; the root shell has already rebuilt the registry
	// by the time the message reaches this screen.
	withCols := widget
	withCols.PrinterColumns = []kube.PrinterColumn{
		{Name: "Phase", Type: "string", JSONPath: ".status.phase"},
	}
	sess.Registry.Register(resources.CustomDescriptor(withCols))

	before := m.reloadEpoch
	_, cmd := m.Update(kube.ResourceChangedMsg{Kind: kube.KindCustomResourceDefinition})

	if cmd == nil || m.reloadEpoch == before {
		t.Fatal("expected a reload once the kind's real columns arrived")
	}
	if len(m.desc.Columns) != 3 || m.desc.Columns[1] != "Phase" {
		t.Fatalf("descriptor columns = %v, want Name/Phase/Age", m.desc.Columns)
	}
}

// widgetKind is the discovered-kind fixture the CRD column tests share, in
// its two shapes: before its printer columns have been fetched, and after.
func widgetKind(printerColumns ...kube.PrinterColumn) kube.DiscoveredKind {
	return kube.DiscoveredKind{
		GVR:  schema.GroupVersionResource{Group: "example.com", Version: "v1", Resource: "widgets"},
		Kind: "Widget", Plural: "widgets", Group: "example.com", Established: true,
		PrinterColumns: printerColumns,
	}
}

// TestColumnsShrinkingDropsRowsProjectedWithTheOldOnes is the crash this
// pairs with: a custom kind loaded with its printer columns, then a context
// switch (which resets discovery, columns included) putting the same kind
// back on its interim NAME/AGE pair. The rows on screen still carry a cell
// per old column, so rendering them under the new, narrower column set walked
// straight off the end of it — index out of range, inside View, which takes
// the program down with it.
func TestColumnsShrinkingDropsRowsProjectedWithTheOldOnes(t *testing.T) {
	sess := newSession()
	withCols := widgetKind(
		kube.PrinterColumn{Name: "Phase", Type: "string", JSONPath: ".status.phase"},
		kube.PrinterColumn{Name: "Host", Type: "string", JSONPath: ".status.host"},
	)
	sess.Registry.Register(resources.CustomDescriptor(withCols))

	m := New(Config{Session: sess, Lister: fakeLister{}})
	m.SetSize(120, 36)
	m.kind = kube.ResourceKind("Widget")
	m.desc, _ = sess.Registry.Descriptor(m.kind)

	m.applyRowsLoaded(rowsLoadedMsg{
		kind: m.kind, columns: len(m.desc.Columns),
		rows: []resources.Row{{
			Namespace: "default", Name: "w1",
			Cells:  []string{"w1", "Ready", "widget.example.com", "3m"},
			Status: resources.StatusOK,
		}},
	})
	if m.state != tui.TaskStateReady {
		t.Fatalf("precondition: state = %s, want ready", m.state)
	}

	// The columns go away again, and the root shell has already rebuilt the
	// registry by the time the message reaches this screen.
	sess.Registry.Register(resources.CustomDescriptor(widgetKind()))
	_, cmd := m.Update(kube.ResourceChangedMsg{Kind: kube.KindCustomResourceDefinition})

	if cmd == nil {
		t.Fatal("expected a reload once the kind's columns changed shape")
	}
	if len(m.rows) != 0 || m.state != tui.TaskStateLoading {
		t.Fatalf("rows = %d, state = %s — rows projected against the old columns must not survive the swap", len(m.rows), m.state)
	}
	m.View() // the panic was here
}

// TestRowsLoadedAgainstOldColumnsIsIgnored: the same mismatch arriving from
// the other direction — a load already in flight when the descriptor changed
// shape. Its rows are as misaligned as the ones on screen were.
func TestRowsLoadedAgainstOldColumnsIsIgnored(t *testing.T) {
	sess := newSession()
	sess.Registry.Register(resources.CustomDescriptor(widgetKind()))

	m := New(Config{Session: sess, Lister: fakeLister{}})
	m.SetSize(120, 36)
	m.kind = kube.ResourceKind("Widget")
	m.desc, _ = sess.Registry.Descriptor(m.kind)

	m.applyRowsLoaded(rowsLoadedMsg{
		kind: m.kind, columns: len(m.desc.Columns) + 1,
		rows: []resources.Row{{Namespace: "default", Name: "w1", Cells: []string{"w1", "Ready", "3m"}}},
	})

	if len(m.rows) != 0 {
		t.Fatalf("rows = %d, want none — this reply was projected against a descriptor the screen no longer has", len(m.rows))
	}
}

// TestCachedRowsAreNotReusedAcrossAColumnChange: 15a's cached-rows loading
// view outlives a context switch (rowCache is never cleared), so it's the
// other way a set of rows can meet a descriptor that has since changed shape.
func TestCachedRowsAreNotReusedAcrossAColumnChange(t *testing.T) {
	sess := newSession()
	withCols := widgetKind(kube.PrinterColumn{Name: "Phase", Type: "string", JSONPath: ".status.phase"})
	sess.Registry.Register(resources.CustomDescriptor(withCols))

	m := New(Config{Session: sess, Lister: fakeLister{}})
	m.SetSize(120, 36)
	m.kind = kube.ResourceKind("Widget")
	m.namespace = "default"
	m.desc, _ = sess.Registry.Descriptor(m.kind)

	m.applyRowsLoaded(rowsLoadedMsg{
		kind: m.kind, columns: len(m.desc.Columns),
		rows: []resources.Row{{Namespace: "default", Name: "w1", Cells: []string{"w1", "Ready", "3m"}}},
	})
	if _, ok := m.cachedRowsFor(m.kind, m.namespace); !ok {
		t.Fatal("precondition: a successful load should be cached for the 15a loading view")
	}

	m.desc = resources.CustomDescriptor(widgetKind()) // the new context's interim columns
	if _, ok := m.cachedRowsFor(m.kind, m.namespace); ok {
		t.Fatal("the cached snapshot was projected against different columns — reusing it puts every cell under the wrong header")
	}
}

// TestRowCellsNeverIndexPastItsColumns pins the render path's own half of the
// guarantee, independent of whatever the model believes: View is not a place
// that may panic, so a row wider than the columns it's handed is clamped
// rather than fatal.
func TestRowCellsNeverIndexPastItsColumns(t *testing.T) {
	m := New(Config{Session: newSession(), Lister: fakeLister{}})
	m.SetSize(120, 36)
	m.desc = resources.CustomDescriptor(widgetKind())
	m.kind = m.desc.Kind

	cols := browseColumns(m.desc)
	row := resources.Row{Name: "w1", Cells: []string{"w1", "Ready", "widget.example.com", "3m"}}
	cells := m.rowCells(row, nil, cols, 120, newRowCellStyles(m.Theme(), false, false, false), m.Theme(), 0, 0, "", false, false)

	if len(cells) != len(cols) {
		t.Fatalf("cells = %d, want %d — one per column handed in", len(cells), len(cols))
	}
}

// TestCRDChangeIsIgnoredWhenColumnsAreUnchanged: browse reloads on plenty of
// signals already; a CRD event that changes nothing about this kind must not
// add another.
func TestCRDChangeIsIgnoredWhenColumnsAreUnchanged(t *testing.T) {
	m := New(Config{Session: newSession(), Lister: fakeLister{}})
	m.SetSize(120, 36)
	m.kind = kube.KindPod

	before := m.reloadEpoch
	_, cmd := m.Update(kube.ResourceChangedMsg{Kind: kube.KindCustomResourceDefinition})
	if cmd != nil || m.reloadEpoch != before {
		t.Fatal("a CRD change reloaded the Pods list, which has nothing to do with CRDs")
	}
}
