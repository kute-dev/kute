package resources

import (
	"testing"

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

func TestDefaultRegistryEveryDescriptorHasHealthAndDescribe(t *testing.T) {
	t.Parallel()
	reg := DefaultRegistry()
	for _, kind := range []kube.ResourceKind{
		kube.KindPod, kube.KindDeployment, kube.KindDaemonSet, kube.KindStatefulSet,
		kube.KindReplicaSet, kube.KindJob, kube.KindCronJob, kube.KindService,
		kube.KindConfigMap, kube.KindSecret, kube.KindPersistentVolumeClaim,
		kube.KindNode, kube.KindNamespace, kube.KindEvent,
	} {
		d, ok := reg.Descriptor(kind)
		if !ok {
			t.Fatalf("missing descriptor for %s", kind)
		}
		if d.Health == nil {
			t.Errorf("%s: Health is nil", kind)
		}
		if d.Describe == "" {
			t.Errorf("%s: Describe is empty", kind)
		}
	}
}

func TestDefaultRegistryClusterScopedKinds(t *testing.T) {
	t.Parallel()
	reg := DefaultRegistry()
	for _, kind := range []kube.ResourceKind{kube.KindNode, kube.KindNamespace} {
		d, _ := reg.Descriptor(kind)
		if !d.ClusterScoped {
			t.Errorf("%s should be ClusterScoped", kind)
		}
	}
	d, _ := reg.Descriptor(kube.KindPod)
	if d.ClusterScoped {
		t.Errorf("Pod should not be ClusterScoped")
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
