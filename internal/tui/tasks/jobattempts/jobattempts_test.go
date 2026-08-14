package jobattempts

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/kube/fake"
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/actions"
)

// step drains a tea.Cmd chain synchronously — mirrors cronjobdetail_test.go's
// own step, duplicated per the repo's package-local-seam convention. Never
// called on Init()'s own batch: tickCmd/spinner.Tick both recur forever
// under synchronous draining.
func step(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			if c != nil {
				m = step(t, m, c())
			}
		}
		return m
	}
	updated, cmd := m.Update(msg)
	next := *updated.(*Model)
	if cmd != nil {
		return step(t, next, cmd())
	}
	return next
}

func newSession(namespace string) *tui.Session {
	return &tui.Session{Theme: tui.Dark(), Location: tui.Location{Context: "test-cluster", Namespace: namespace}}
}

func boolPtr(b bool) *bool    { return &b }
func int32Ptr(v int32) *int32 { return &v }

func controllerRef(kind, name string, uid types.UID) metav1.OwnerReference {
	return metav1.OwnerReference{Kind: kind, Name: name, UID: uid, Controller: boolPtr(true)}
}

func newModel(t *testing.T, c *fake.Cluster, namespace, name string) Model {
	t.Helper()
	m := New(Config{Session: newSession(namespace), Lister: c, Mutator: c, Namespace: namespace, Name: name})
	m.SetSize(120, 40)
	return step(t, m, m.load()())
}

func newFakeJob(namespace, name string) (*fake.Cluster, *batchv1.Job) {
	c := fake.New(namespace, "test-cluster")
	j := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, UID: "job-uid-1"},
		Spec:       batchv1.JobSpec{Completions: int32Ptr(1)},
	}
	c.Seed(kube.KindJob, j)
	return c, j
}

func TestLoadFoundTransitionsToReady(t *testing.T) {
	c, _ := newFakeJob("default", "batch-1")
	m := newModel(t, c, "default", "batch-1")
	if m.state != tui.TaskStateReady {
		t.Fatalf("state = %v, want Ready", m.state)
	}
	if !m.found {
		t.Fatalf("expected found = true")
	}
}

func TestLoadNotFoundRendersDeletedState(t *testing.T) {
	c := fake.New("default", "test-cluster")
	m := newModel(t, c, "default", "batch-missing")
	if m.state != tui.TaskStateReady {
		t.Fatalf("state = %v, want Ready (found=false still renders, just the deleted banner)", m.state)
	}
	if m.found {
		t.Fatalf("expected found = false for a Job that doesn't exist")
	}
}

func TestBeginRerunStagesCreateChoiceByDefault(t *testing.T) {
	c, _ := newFakeJob("default", "batch-1")
	m := newModel(t, c, "default", "batch-1")
	m.beginRerun()
	if m.pendingRerun == nil {
		t.Fatalf("expected beginRerun to stage a pendingRerun target")
	}
	if m.pendingRerun.newName != "batch-1-rerun-1" {
		t.Errorf("newName = %q, want %q", m.pendingRerun.newName, "batch-1-rerun-1")
	}
}

func TestCommitRerunCreateExecutesImmediately(t *testing.T) {
	c, _ := newFakeJob("default", "batch-1")
	m := newModel(t, c, "default", "batch-1")
	m.beginRerun()
	m = step(t, m, m.commitRerun()())
	if m.pendingRerun != nil {
		t.Fatalf("expected commitRerun to clear the staged preview")
	}
	if m.actions.Active() {
		t.Fatalf("expected create to execute at TierNone, not enter a confirm state")
	}
}

func TestCommitRerunReplaceGoesThroughOrdinaryConfirm(t *testing.T) {
	c, _ := newFakeJob("default", "batch-1")
	m := newModel(t, c, "default", "batch-1")
	m.beginRerun()
	m.pendingRerun.choice = 1 // resources.JobRerunReplace
	if cmd := m.commitRerun(); cmd != nil {
		// TierInline's Begin returns nil (it only moves to TaskStateConfirming,
		// never executes) — this branch would only fire if replace somehow
		// resolved to TierNone, which would itself be the bug under test.
		m = step(t, m, cmd())
	}
	if m.pendingRerun != nil {
		t.Fatalf("expected the staged preview cleared once handed to the ordinary confirm")
	}
	if !m.actions.Active() || m.actions.Tier() != actions.TierInline {
		t.Fatalf("expected replace to land on the ordinary TierInline confirm, tier=%v", m.actions.Tier())
	}
}

func TestMoveSiblingLoadsNextJob(t *testing.T) {
	c, _ := newFakeJob("default", "batch-1")
	c.Seed(kube.KindJob, &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "batch-2", Namespace: "default", UID: "job-uid-2"},
		Spec:       batchv1.JobSpec{Completions: int32Ptr(1)},
	})
	m := newModel(t, c, "default", "batch-1")
	m.siblings = []SiblingRef{{Namespace: "default", Name: "batch-1"}, {Namespace: "default", Name: "batch-2"}}
	m.siblingIndex = 0
	cmd := m.moveSibling(1)
	if cmd == nil {
		t.Fatalf("expected moveSibling to return a load command")
	}
	m = step(t, m, cmd())
	if m.name != "batch-2" {
		t.Fatalf("name = %q, want batch-2 after moving to the next sibling", m.name)
	}
	if m.state != tui.TaskStateReady || !m.found {
		t.Fatalf("expected the sibling load to settle Ready/found, got state=%v found=%v", m.state, m.found)
	}
}
