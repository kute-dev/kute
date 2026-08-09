package resources

import (
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui/components"
)

func TestStatusHealthTallies(t *testing.T) {
	t.Parallel()
	rows := []Row{
		{Status: StatusOK}, {Status: StatusOK}, {Status: StatusWarn},
		{Status: StatusFail}, {Status: StatusNeutral},
	}
	got := StatusHealth(rows)
	want := HealthCounts{OK: 2, Warn: 1, Fail: 1, Neutral: 1}
	if got != want {
		t.Fatalf("StatusHealth() = %+v, want %+v", got, want)
	}
	if got.Total() != 5 {
		t.Fatalf("Total() = %d, want 5", got.Total())
	}
}

// TestDefaultRegistryEveryDescriptorIsComplete is what makes "resource kinds
// are registry entries, not bespoke screens" an enforced invariant rather
// than a stated one: registering a kind with no columns or no projection
// fails here instead of rendering a blank list.
//
// It ranges over Kinds() deliberately. The hardcoded list this replaced had
// drifted to 14 of the 17 registered kinds, silently skipping the three most
// recently added ones — the failure mode a hardcoded list always has.
//
// The assertions are the fields Register does *not* backfill. Health and
// HealthLabel are absent for that reason: registry.go fills both in when
// nil, so a `d.Health == nil` check can never fail for anything registered
// through the normal path.
func TestDefaultRegistryEveryDescriptorIsComplete(t *testing.T) {
	t.Parallel()
	reg := DefaultRegistry()
	kinds := reg.Kinds()
	if len(kinds) == 0 {
		t.Fatal("DefaultRegistry() registered no kinds")
	}
	for _, kind := range kinds {
		d, ok := reg.Descriptor(kind)
		if !ok {
			t.Fatalf("Kinds() returned %s but Descriptor() doesn't know it", kind)
		}
		if d.Kind != kind {
			t.Errorf("%s: registered under the wrong key (Descriptor.Kind = %s)", kind, d.Kind)
		}
		if d.Describe == "" {
			t.Errorf("%s: Describe is empty", kind)
		}
		if d.Display == "" {
			t.Errorf("%s: Display is empty", kind)
		}
		if len(d.Columns) == 0 {
			t.Errorf("%s: Columns is empty", kind)
		}
		if d.Project == nil {
			t.Errorf("%s: Project is nil", kind)
		}
		if d.FlexColumn != "" && !slices.Contains(d.Columns, d.FlexColumn) {
			t.Errorf("%s: FlexColumn %q names no column in %v", kind, d.FlexColumn, d.Columns)
		}
	}
}

// TestDefaultRegistryClusterScopedKinds partitions every registered kind by
// scope rather than spot-checking a few, so adding a kind can't slip through
// unclassified: a kind missing from want fails outright.
func TestDefaultRegistryClusterScopedKinds(t *testing.T) {
	t.Parallel()
	want := map[kube.ResourceKind]bool{
		kube.KindPod:                      false,
		kube.KindDeployment:               false,
		kube.KindDaemonSet:                false,
		kube.KindStatefulSet:              false,
		kube.KindReplicaSet:               false,
		kube.KindJob:                      false,
		kube.KindCronJob:                  false,
		kube.KindService:                  false,
		kube.KindIngress:                  false,
		kube.KindConfigMap:                false,
		kube.KindSecret:                   false,
		kube.KindPersistentVolumeClaim:    false,
		kube.KindEvent:                    false,
		kube.KindHelmRelease:              false,
		kube.KindNode:                     true,
		kube.KindNamespace:                true,
		kube.KindForward:                  true,
		kube.KindCustomResourceDefinition: true,
	}
	reg := DefaultRegistry()
	for _, kind := range reg.Kinds() {
		wantScoped, known := want[kind]
		if !known {
			t.Errorf("%s is registered but this test doesn't say whether it's cluster-scoped", kind)
			continue
		}
		d, _ := reg.Descriptor(kind)
		if d.ClusterScoped != wantScoped {
			t.Errorf("%s: ClusterScoped = %v, want %v", kind, d.ClusterScoped, wantScoped)
		}
	}
}

// descFor resolves kind's built-in Descriptor for Columns' new
// Descriptor-taking signature — Columns itself no longer does this lookup
// (see columns.go), so tests that want a built-in kind's columns do it
// themselves, same as every real caller (browse's m.desc).
func descFor(t *testing.T, kind kube.ResourceKind) Descriptor {
	t.Helper()
	d, ok := DefaultRegistry().Descriptor(kind)
	if !ok {
		t.Fatalf("no descriptor registered for %s", kind)
	}
	return d
}

func TestColumnsFlexesNameColumn(t *testing.T) {
	t.Parallel()
	cols := Columns(descFor(t, kube.KindPod))
	if len(cols) != 9 { // leading status-glyph column + 8 data columns
		t.Fatalf("got %d columns, want 9", len(cols))
	}
	if cols[0].Title != "" || cols[0].Min != 1 || cols[0].Flex {
		t.Fatalf("expected an untitled fixed 1ch glyph column first, got %+v", cols[0])
	}
	if cols[1].Title != "Name" || !cols[1].Flex {
		t.Fatalf("expected Name column to flex, got %+v", cols[1])
	}
}

func TestColumnsRightAlignsKnownNumericTitles(t *testing.T) {
	t.Parallel()
	cols := Columns(descFor(t, kube.KindPod))
	var age components.Column
	for _, c := range cols {
		if c.Title == "Age" {
			age = c
		}
	}
	if age.Align != components.AlignRight {
		t.Fatalf("Age column should right-align, got %+v", age)
	}
}

func TestColumnsZeroDescriptorReturnsOnlyGlyphColumn(t *testing.T) {
	t.Parallel()
	// Columns no longer looks kind up itself (see columns.go) — an
	// unregistered kind is now the caller's problem (Registry.Descriptor's
	// ok bool), not something Columns can detect. A zero-value Descriptor
	// (no Columns titles at all) still gets the leading glyph column.
	got := Columns(Descriptor{})
	if len(got) != 1 || got[0].Title != "" {
		t.Fatalf("expected just the glyph column for a zero-value Descriptor, got %+v", got)
	}
}

func TestColumnsEventsFlexesFirstColumnWhenNoName(t *testing.T) {
	t.Parallel()
	cols := Columns(descFor(t, kube.KindEvent))
	if len(cols) < 2 || cols[1].Title != "Type" || !cols[1].Flex {
		t.Fatalf("expected Events to flex its first data column (Type), got %+v", cols)
	}
}

func TestArgoSyncColumnFitsOutOfSync(t *testing.T) {
	t.Parallel()
	d := argoDescriptor(argoApplicationDiscoveredKind())
	for _, col := range Columns(d) {
		if col.Title == "Sync" {
			if col.Min < len("OutOfSync") {
				t.Fatalf("Sync width = %d, want at least %d to render OutOfSync", col.Min, len("OutOfSync"))
			}
			return
		}
	}
	t.Fatal("Argo descriptor has no Sync column")
}

// TestFixedWidthsLeaveRoomForSortArrow guards the invariant documented on
// fixedWidths: a fixed column never flexes, so its entry is its on-screen
// width, and components.Table's renderHeaderV2 drops the " ↑"/" ↓" sort
// indicator without a trace when the title plus two cells doesn't fit. AGE
// and REV were both 4 against a 3-cell title and so could never show which
// way the list was sorted.
func TestFixedWidthsLeaveRoomForSortArrow(t *testing.T) {
	t.Parallel()
	const arrow = 2 // " ↑"
	for title, width := range fixedWidths {
		if title == "Restarts" {
			// browse renders this one as the 1-cell ↺ glyph, not the word
			// (browse/view.go's browseColumns), so its title never measures 8.
			continue
		}
		// Measured the way renderHeaderV2 measures it — the uppercased title,
		// in display cells, not bytes.
		cells := ansi.StringWidth(strings.ToUpper(title))
		if want := cells + arrow; width < want {
			t.Errorf("fixedWidths[%q] = %d, want >= %d: a %d-cell column can't fit %q plus the sort arrow, "+
				"so renderHeaderV2 drops the arrow and the column looks unsortable",
				title, width, want, width, strings.ToUpper(title))
		}
	}
}

func TestCellsMapsRowCellsAfterGlyph(t *testing.T) {
	t.Parallel()
	row := Row{Glyph: "●", Cells: []string{"api", "1/1", "Running", "0", "2d"}}
	got := Cells(row, 80, nil)
	if len(got) != 6 {
		t.Fatalf("got %d cells, want 6", len(got))
	}
	if got[0].Text != "●" {
		t.Errorf("cell 0 = %q, want the row's status glyph", got[0].Text)
	}
	for i, want := range row.Cells {
		if got[i+1].Text != want {
			t.Errorf("cell %d = %q, want %q", i+1, got[i+1].Text, want)
		}
	}
}
