package browse

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/config"
	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui/actions"
)

func ptr32(v int32) *int32 { return &v }

func jobObj(ns, name string) *batchv1.Job {
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       batchv1.JobSpec{Completions: ptr32(1)},
		Status:     batchv1.JobStatus{Succeeded: 1},
	}
}

func suspendedJobObj(ns, name string, suspend bool) *batchv1.Job {
	j := jobObj(ns, name)
	j.Spec.Suspend = &suspend
	return j
}

func cronJobObj(ns, name string) *batchv1.CronJob {
	return &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       batchv1.CronJobSpec{Schedule: "*/5 * * * *"},
	}
}

// suspendedCronJobObj mirrors suspendedJobObj for CronJob.
func suspendedCronJobObj(ns, name string, suspend bool) *batchv1.CronJob {
	cj := cronJobObj(ns, name)
	cj.Spec.Suspend = &suspend
	return cj
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

// TestCtrlRShowsConfirmThenRetriesJobOnY mirrors
// TestCtrlRShowsConfirmThenRestartsRolloutOnY's shape for Job's own retry.
func TestCtrlRShowsConfirmThenRetriesJobOnY(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindJob: {jobObj("default", "batch-1")},
	}}
	mut := &fakeMutator{}
	session := newSession()
	session.Location.Kind = kube.KindJob
	m := New(Config{Session: session, Lister: lister, Mutator: mut})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Text: "ctrl+r"})
	if !m.actions.Active() || m.actions.Tier() != actions.TierInline {
		t.Fatalf("expected ctrl+r to open the inline prompt, tier=%v", m.actions.Tier())
	}
	kb := m.Keybar()
	if !strings.Contains(kb.RightNote, "kubectl create job") || !strings.Contains(kb.RightNote, "--from=job/batch-1") {
		t.Fatalf("expected the will-run line in the confirm, got %q", kb.RightNote)
	}
	if len(mut.retriedJobs) != 0 {
		t.Fatalf("expected no retry before 'y', got %v", mut.retriedJobs)
	}

	m = step(t, m, tea.KeyPressMsg{Text: "y"})
	if len(mut.retriedJobs) != 1 {
		t.Fatalf("retriedJobs = %v, want one entry", mut.retriedJobs)
	}
	if !strings.HasPrefix(mut.retriedJobs[0], "default/batch-1->batch-1-retry-") {
		t.Fatalf("retriedJobs[0] = %q, want a default/batch-1->batch-1-retry-<ts> entry", mut.retriedJobs[0])
	}
}

// TestJobRetryStaysInlineEvenInProd pins the deliberate choice not to
// escalate Retry to the type-the-name modal in PROD (browse/jobs.go's
// beginJobRetry doc comment): components.TypeNameModal is reserved for
// destructive confirms, and Retry is explicitly non-destructive (a clone,
// not a delete+recreate) — so a PROD context still gets the same plain
// inline y/N, just like a non-PROD one.
func TestJobRetryStaysInlineEvenInProd(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindJob: {jobObj("default", "batch-1")},
	}}
	mut := &fakeMutator{}
	session := newSession()
	session.Location.Kind = kube.KindJob
	session.Config = config.Config{ProdContexts: []string{session.Location.Context}}
	m := New(Config{Session: session, Lister: lister, Mutator: mut})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Text: "ctrl+r"})
	if !m.actions.Active() || m.actions.Tier() != actions.TierInline {
		t.Fatalf("expected ctrl+r to stay TierInline even in a prod context, tier=%v", m.actions.Tier())
	}

	m = step(t, m, tea.KeyPressMsg{Text: "y"})
	if len(mut.retriedJobs) != 1 {
		t.Fatalf("expected a plain 'y' to retry immediately, got %v", mut.retriedJobs)
	}
}

// TestJobSuspendEscalatesToModalConfirmInProd pins the fix for
// beginJobSuspend's own broken PROD escalation (the same bug class
// TestCtrlRProdOpensTypeNameModal pins for rollout-restart): unlike Retry,
// JobSuspend has a real destructive side effect (tears down the Job's active
// pods immediately), so a prod-tagged context must escalate 's' from
// TierInline to TierModal — landing on the plain ConfirmCard (Drain's/
// Rollback's treatment), not the typed-name modal, since job-suspend isn't
// in requiresTypeNameConfirm/requiresTypedName.
func TestJobSuspendEscalatesToModalConfirmInProd(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindJob: {suspendedJobObj("default", "batch-1", false)},
	}}
	mut := &fakeMutator{}
	session := newSession()
	session.Location.Kind = kube.KindJob
	session.Config = config.Config{ProdContexts: []string{session.Location.Context}}
	m := New(Config{Session: session, Lister: lister, Mutator: mut})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Text: "s"})
	if !m.actions.Active() || m.actions.Tier() != actions.TierModal {
		t.Fatalf("expected 's' in a prod context to escalate to TierModal, tier=%v", m.actions.Tier())
	}

	m = step(t, m, tea.KeyPressMsg{Text: "y"})
	if len(mut.jobSuspends) != 1 || mut.jobSuspends[0] != "default/batch-1=true" {
		t.Fatalf("expected a suspend call, got %v", mut.jobSuspends)
	}
}

// TestSKeyTogglesJobSuspendAndResumeLabel mirrors
// TestFluxSuspendVerbFlipsDirectionWithTheRow, adjusted for TierInline
// (Job's own suspend needs 'y' after 's', unlike Flux's TierNone).
func TestSKeyTogglesJobSuspendAndResumeLabel(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindJob: {suspendedJobObj("default", "batch-1", false)},
	}}
	mut := &fakeMutator{}
	session := newSession()
	session.Location.Kind = kube.KindJob
	m := New(Config{Session: session, Lister: lister, Mutator: mut})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	if !strings.Contains(plain(m.Render()), "suspend") {
		t.Errorf("keybar should read 'suspend' on an active row:\n%s", plain(m.Render()))
	}
	m = step(t, m, tea.KeyPressMsg{Text: "s"})
	if !m.actions.Active() || m.actions.Tier() != actions.TierInline {
		t.Fatalf("expected 's' to open the inline prompt, tier=%v", m.actions.Tier())
	}
	m = step(t, m, tea.KeyPressMsg{Text: "y"})
	if len(mut.jobSuspends) != 1 || mut.jobSuspends[0] != "default/batch-1=true" {
		t.Fatalf("expected a suspend call, got %v", mut.jobSuspends)
	}

	mut2 := &fakeMutator{}
	lister2 := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindJob: {suspendedJobObj("default", "batch-1", true)},
	}}
	session2 := newSession()
	session2.Location.Kind = kube.KindJob
	m2 := New(Config{Session: session2, Lister: lister2, Mutator: mut2})
	m2.SetSize(120, 36)
	m2 = step(t, m2, m2.Init()())

	if !strings.Contains(plain(m2.Render()), "resume") {
		t.Errorf("keybar should read 'resume' on a suspended row:\n%s", plain(m2.Render()))
	}
	m2 = step(t, m2, tea.KeyPressMsg{Text: "s"})
	m2 = step(t, m2, tea.KeyPressMsg{Text: "y"})
	if len(mut2.jobSuspends) != 1 || mut2.jobSuspends[0] != "default/batch-1=false" {
		t.Fatalf("expected a resume call on a suspended row, got %v", mut2.jobSuspends)
	}
}

// TestCtrlRShowsConfirmThenTriggersCronJobRunNowOnY mirrors
// TestCtrlRShowsConfirmThenRetriesJobOnY for CronJob's own run-now.
func TestCtrlRShowsConfirmThenTriggersCronJobRunNowOnY(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindCronJob: {cronJobObj("default", "nightly")},
	}}
	mut := &fakeMutator{}
	session := newSession()
	session.Location.Kind = kube.KindCronJob
	m := New(Config{Session: session, Lister: lister, Mutator: mut})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Text: "ctrl+r"})
	if !m.actions.Active() || m.actions.Tier() != actions.TierInline {
		t.Fatalf("expected ctrl+r to open the inline prompt, tier=%v", m.actions.Tier())
	}
	kb := m.Keybar()
	if !strings.Contains(kb.RightNote, "kubectl create job") || !strings.Contains(kb.RightNote, "--from=cronjob/nightly") {
		t.Fatalf("expected the will-run line in the confirm, got %q", kb.RightNote)
	}
	if len(mut.triggeredCronJobs) != 0 {
		t.Fatalf("expected no trigger before 'y', got %v", mut.triggeredCronJobs)
	}

	m = step(t, m, tea.KeyPressMsg{Text: "y"})
	if len(mut.triggeredCronJobs) != 1 {
		t.Fatalf("triggeredCronJobs = %v, want one entry", mut.triggeredCronJobs)
	}
	if !strings.HasPrefix(mut.triggeredCronJobs[0], "default/nightly->nightly-manual-") {
		t.Fatalf("triggeredCronJobs[0] = %q, want a default/nightly->nightly-manual-<ts> entry", mut.triggeredCronJobs[0])
	}
}

// TestCronJobRunNowStaysInlineEvenInProd mirrors TestJobRetryStaysInlineEvenInProd:
// CronJobRunNow is a clone into a new object, not destructive, so it never
// escalates past the plain inline y/N even in a PROD context.
func TestCronJobRunNowStaysInlineEvenInProd(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindCronJob: {cronJobObj("default", "nightly")},
	}}
	mut := &fakeMutator{}
	session := newSession()
	session.Location.Kind = kube.KindCronJob
	session.Config = config.Config{ProdContexts: []string{session.Location.Context}}
	m := New(Config{Session: session, Lister: lister, Mutator: mut})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Text: "ctrl+r"})
	if !m.actions.Active() || m.actions.Tier() != actions.TierInline {
		t.Fatalf("expected ctrl+r to stay TierInline even in a prod context, tier=%v", m.actions.Tier())
	}

	m = step(t, m, tea.KeyPressMsg{Text: "y"})
	if len(mut.triggeredCronJobs) != 1 {
		t.Fatalf("expected a plain 'y' to trigger immediately, got %v", mut.triggeredCronJobs)
	}
}

// TestSKeyTogglesCronJobSuspendImmediatelyNoConfirm pins CronJobSuspend's
// TierNone contract (unlike Job's own 's', TierInline): pressing 's' applies
// the suspend/resume immediately, with no confirm state to pass through
// first — the same shape beginFluxSuspend/beginCordon use.
func TestSKeyTogglesCronJobSuspendImmediatelyNoConfirm(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindCronJob: {suspendedCronJobObj("default", "nightly", false)},
	}}
	mut := &fakeMutator{}
	session := newSession()
	session.Location.Kind = kube.KindCronJob
	m := New(Config{Session: session, Lister: lister, Mutator: mut})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Text: "s"})
	if m.actions.Active() {
		t.Fatalf("expected TierNone to execute immediately with no confirm state")
	}
	if len(mut.cronJobSuspends) != 1 || mut.cronJobSuspends[0] != "default/nightly=true" {
		t.Fatalf("expected a suspend call, got %v", mut.cronJobSuspends)
	}
}

// TestSKeyOnCronJobFlipsSuspendResumeKeybarLabel mirrors
// TestSKeyTogglesJobSuspendAndResumeLabel's keybar-label assertions.
func TestSKeyOnCronJobFlipsSuspendResumeKeybarLabel(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindCronJob: {suspendedCronJobObj("default", "nightly", false)},
	}}
	mut := &fakeMutator{}
	session := newSession()
	session.Location.Kind = kube.KindCronJob
	m := New(Config{Session: session, Lister: lister, Mutator: mut})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	if !strings.Contains(plain(m.Render()), "suspend") {
		t.Errorf("keybar should read 'suspend' on an active row:\n%s", plain(m.Render()))
	}
	m = step(t, m, tea.KeyPressMsg{Text: "s"})
	if len(mut.cronJobSuspends) != 1 || mut.cronJobSuspends[0] != "default/nightly=true" {
		t.Fatalf("expected a suspend call, got %v", mut.cronJobSuspends)
	}

	mut2 := &fakeMutator{}
	lister2 := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindCronJob: {suspendedCronJobObj("default", "nightly", true)},
	}}
	session2 := newSession()
	session2.Location.Kind = kube.KindCronJob
	m2 := New(Config{Session: session2, Lister: lister2, Mutator: mut2})
	m2.SetSize(120, 36)
	m2 = step(t, m2, m2.Init()())

	if !strings.Contains(plain(m2.Render()), "resume") {
		t.Errorf("keybar should read 'resume' on a suspended row:\n%s", plain(m2.Render()))
	}
	m2 = step(t, m2, tea.KeyPressMsg{Text: "s"})
	if len(mut2.cronJobSuspends) != 1 || mut2.cronJobSuspends[0] != "default/nightly=false" {
		t.Fatalf("expected a resume call on a suspended row, got %v", mut2.cronJobSuspends)
	}
}

// TestShiftSOpensCronScheduleEditPanelPrefilledWithCurrentSchedule pins
// beginCronJobSetSchedule's pre-fill contract.
func TestShiftSOpensCronScheduleEditPanelPrefilledWithCurrentSchedule(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindCronJob: {cronJobObj("default", "nightly")},
	}}
	mut := &fakeMutator{}
	session := newSession()
	session.Location.Kind = kube.KindCronJob
	m := New(Config{Session: session, Lister: lister, Mutator: mut})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Text: "S"})
	if m.pendingCronSchedule == nil {
		t.Fatalf("expected 'S' to open the schedule panel")
	}
	if got := m.pendingCronSchedule.input.Value(); got != "*/5 * * * *" {
		t.Fatalf("pendingCronSchedule.input = %q, want the row's current schedule %q", got, "*/5 * * * *")
	}
}

// TestCronScheduleEditRejectsInvalidCronExpressionInline pins
// commitCronSchedule's client-side validation: an invalid expression keeps
// the panel open with parseErr set, and never reaches the mutator.
func TestCronScheduleEditRejectsInvalidCronExpressionInline(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindCronJob: {cronJobObj("default", "nightly")},
	}}
	mut := &fakeMutator{}
	session := newSession()
	session.Location.Kind = kube.KindCronJob
	m := New(Config{Session: session, Lister: lister, Mutator: mut})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Text: "S"})
	m.pendingCronSchedule.input.SetValue("not a cron expression")
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter, Text: "enter"})

	if m.pendingCronSchedule == nil {
		t.Fatalf("expected the panel to stay open after an invalid commit")
	}
	if m.pendingCronSchedule.parseErr == nil {
		t.Fatalf("expected parseErr set for an invalid schedule")
	}
	if len(mut.cronJobSchedules) != 0 {
		t.Fatalf("expected the mutator never called for an invalid schedule, got %v", mut.cronJobSchedules)
	}
}

// TestCronScheduleEditCommitsAndPanelStaysOpenShowingResult pins §36a's
// keep-open contract (CLAUDE.md: "confirm → execute → refresh → show
// result → remain on screen") — unlike SetImage/Scale, a successful commit
// never closes the panel on its own; only esc does.
func TestCronScheduleEditCommitsAndPanelStaysOpenShowingResult(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindCronJob: {cronJobObj("default", "nightly")},
	}}
	mut := &fakeMutator{}
	session := newSession()
	session.Location.Kind = kube.KindCronJob
	m := New(Config{Session: session, Lister: lister, Mutator: mut})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Text: "S"})
	m.pendingCronSchedule.input.SetValue("*/15 * * * *")
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter, Text: "enter"})

	if len(mut.cronJobSchedules) != 1 || mut.cronJobSchedules[0] != "default/nightly=*/15 * * * *" {
		t.Fatalf("expected a SetCronJobSchedule call, got %v", mut.cronJobSchedules)
	}
	if m.pendingCronSchedule == nil {
		t.Fatalf("expected the panel to stay open after a successful commit")
	}
	if m.pendingCronSchedule.resultNote == "" {
		t.Fatalf("expected an inline result note after a successful commit")
	}

	m = step(t, m, tea.KeyPressMsg{Text: "esc"})
	if m.pendingCronSchedule != nil {
		t.Fatalf("expected esc to close the panel")
	}
}

// TestCronJobSetScheduleEscalatesToInlineConfirmInProd pins
// TierForCronJobSetSchedule's PROD gate: TierNone outside PROD (the commit
// above applies immediately), TierInline — never TierModal — in PROD.
func TestCronJobSetScheduleEscalatesToInlineConfirmInProd(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindCronJob: {cronJobObj("default", "nightly")},
	}}
	mut := &fakeMutator{}
	session := newSession()
	session.Location.Kind = kube.KindCronJob
	session.Config = config.Config{ProdContexts: []string{session.Location.Context}}
	m := New(Config{Session: session, Lister: lister, Mutator: mut})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Text: "S"})
	m.pendingCronSchedule.input.SetValue("*/15 * * * *")
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter, Text: "enter"})

	if !m.actions.Active() || m.actions.Tier() != actions.TierInline {
		t.Fatalf("expected a valid commit in PROD to escalate to TierInline, tier=%v", m.actions.Tier())
	}
	if len(mut.cronJobSchedules) != 0 {
		t.Fatalf("expected no call before 'y', got %v", mut.cronJobSchedules)
	}

	m = step(t, m, tea.KeyPressMsg{Text: "y"})
	if len(mut.cronJobSchedules) != 1 || mut.cronJobSchedules[0] != "default/nightly=*/15 * * * *" {
		t.Fatalf("expected a SetCronJobSchedule call after confirming, got %v", mut.cronJobSchedules)
	}
}
