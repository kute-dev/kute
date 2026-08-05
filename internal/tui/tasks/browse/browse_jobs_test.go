package browse

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
)

func ptr32(v int32) *int32 { return &v }

func jobObj(ns, name string) *batchv1.Job {
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       batchv1.JobSpec{Completions: ptr32(1)},
		Status:     batchv1.JobStatus{Succeeded: 1},
	}
}

func cronJobObj(ns, name string) *batchv1.CronJob {
	return &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       batchv1.CronJobSpec{Schedule: "*/5 * * * *"},
	}
}

func TestEnterOnJobSwitchesToPodsFilteredByName(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindJob: {jobObj("default", "batch-1")},
		kube.KindPod: {
			pod("default", "batch-1-x7f2k"),
			pod("default", "worker-0"),
		},
	}}
	session := newSession()
	session.Location.Kind = kube.KindJob
	m := New(Config{Session: session, Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter, Text: "enter"})
	if m.kind != kube.KindPod {
		t.Fatalf("expected kind switched to Pod, got %s", m.kind)
	}
	if m.filterInput.Value() != "batch-1" {
		t.Fatalf("filterQuery = %q, want %q", m.filterInput.Value(), "batch-1")
	}
	view := plain(m.Render())
	if !strings.Contains(view, "batch-1-x7f2k") {
		t.Fatalf("expected the owned pod to remain visible:\n%s", view)
	}
	if strings.Contains(view, "worker-0") {
		t.Fatalf("expected the unrelated pod to be filtered out:\n%s", view)
	}
}

func TestEscFromJobPodsReturnsToJobAndSelectsRow(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindJob: {
			jobObj("default", "batch-1"),
			jobObj("default", "batch-2"),
		},
		kube.KindPod: {
			pod("default", "batch-1-x7f2k"),
			pod("default", "batch-2-y8g3l"),
		},
	}}
	session := newSession()
	session.Location.Kind = kube.KindJob
	m := New(Config{Session: session, Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())
	m.moveSelection(1)
	row, ok := m.selectedRow()
	if !ok || row.Name != "batch-2" {
		t.Fatalf("expected batch-2 selected before opening its pods, got %+v (ok=%v)", row, ok)
	}

	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter, Text: "enter"})
	if m.kind != kube.KindPod || m.originName != "batch-2" {
		t.Fatalf("expected Pods filtered by batch-2, got kind=%s originName=%q", m.kind, m.originName)
	}

	m = step(t, m, tea.KeyPressMsg{Text: "esc"})
	if m.kind != kube.KindJob {
		t.Fatalf("expected esc to switch back to Jobs, got %s", m.kind)
	}
	if m.originName != "" {
		t.Fatalf("expected originName cleared after esc-back, got %q", m.originName)
	}
	selected, ok := m.selectedRow()
	if !ok || selected.Name != "batch-2" {
		t.Fatalf("expected batch-2 re-selected on Jobs, got %+v (ok=%v)", selected, ok)
	}
}

func TestBreadcrumbShowsOriginJobName(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindJob: {jobObj("default", "batch-1")},
		kube.KindPod: {pod("default", "batch-1-x7f2k")},
	}}
	session := newSession()
	session.Location.Kind = kube.KindJob
	m := New(Config{Session: session, Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	header := m.Header()
	before := crumbText(header)
	if strings.Contains(before, "batch-1 › Pods") {
		t.Fatalf("expected no job name in breadcrumb before opening pods:\n%s", before)
	}

	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter, Text: "enter"})
	after := crumbText(m.Header())
	if !strings.Contains(after, "batch-1 › Pods") {
		t.Fatalf("expected breadcrumb to include %q, got:\n%s", "batch-1 › Pods", after)
	}
}

// TestEnterOnCronJobSwitchesToPodsFilteredByNameSkippingIntermediateJob pins
// that a CronJob's ↵ jumps straight to its pods, filtered by the CronJob's
// own name, without ever showing an intermediate Jobs list — a CronJob
// spawns a Job named <cronjob>-<unixtime>, whose own pods are named
// <cronjob>-<unixtime>-<random>, still prefixed by the CronJob's name.
func TestEnterOnCronJobSwitchesToPodsFilteredByNameSkippingIntermediateJob(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindCronJob: {cronJobObj("default", "nightly")},
		kube.KindPod: {
			pod("default", "nightly-1712345678-x7f2k"),
			pod("default", "worker-0"),
		},
	}}
	session := newSession()
	session.Location.Kind = kube.KindCronJob
	m := New(Config{Session: session, Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter, Text: "enter"})
	if m.kind != kube.KindPod {
		t.Fatalf("expected kind switched directly to Pod (no intermediate Jobs list), got %s", m.kind)
	}
	if m.filterInput.Value() != "nightly" {
		t.Fatalf("filterQuery = %q, want %q", m.filterInput.Value(), "nightly")
	}
	view := plain(m.Render())
	if !strings.Contains(view, "nightly-1712345678-x7f2k") {
		t.Fatalf("expected the owned pod to remain visible:\n%s", view)
	}
	if strings.Contains(view, "worker-0") {
		t.Fatalf("expected the unrelated pod to be filtered out:\n%s", view)
	}
}

func TestEscFromCronJobPodsReturnsToCronJobAndSelectsRow(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindCronJob: {
			cronJobObj("default", "nightly"),
			cronJobObj("default", "hourly"),
		},
		kube.KindPod: {
			pod("default", "nightly-1712345678-x7f2k"),
			pod("default", "hourly-1712349999-y8g3l"),
		},
	}}
	session := newSession()
	session.Location.Kind = kube.KindCronJob
	m := New(Config{Session: session, Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())
	m.moveSelection(1)
	row, ok := m.selectedRow()
	if !ok || row.Name != "nightly" {
		t.Fatalf("expected nightly selected before opening its pods, got %+v (ok=%v)", row, ok)
	}

	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter, Text: "enter"})
	if m.kind != kube.KindPod || m.originName != "nightly" {
		t.Fatalf("expected Pods filtered by nightly, got kind=%s originName=%q", m.kind, m.originName)
	}

	m = step(t, m, tea.KeyPressMsg{Text: "esc"})
	if m.kind != kube.KindCronJob {
		t.Fatalf("expected esc to switch back to CronJobs, got %s", m.kind)
	}
	if m.originName != "" {
		t.Fatalf("expected originName cleared after esc-back, got %q", m.originName)
	}
	selected, ok := m.selectedRow()
	if !ok || selected.Name != "nightly" {
		t.Fatalf("expected nightly re-selected on CronJobs, got %+v (ok=%v)", selected, ok)
	}
}
