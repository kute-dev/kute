package fake

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	"github.com/kute-dev/kute/internal/kube"
)

func TestSetImagePatchesNamedContainerOnly(t *testing.T) {
	t.Parallel()
	c := New("default", "dev")
	c.Seed(kube.KindDeployment, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default"},
		Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{
			{Name: "app", Image: "app:1.0"},
			{Name: "sidecar", Image: "sidecar:1.0"},
		}}}},
	})

	if err := c.SetImage(context.Background(), kube.KindDeployment, "default", "api", "app", "app:2.0"); err != nil {
		t.Fatalf("SetImage: %v", err)
	}
	objs, _ := c.ListRaw(context.Background(), kube.KindDeployment, "default")
	deploy := objs[0].(*appsv1.Deployment)
	if deploy.Spec.Template.Spec.Containers[0].Image != "app:2.0" {
		t.Fatalf("app image = %q, want app:2.0", deploy.Spec.Template.Spec.Containers[0].Image)
	}
	if deploy.Spec.Template.Spec.Containers[1].Image != "sidecar:1.0" {
		t.Fatalf("sidecar image = %q, want unchanged sidecar:1.0", deploy.Spec.Template.Spec.Containers[1].Image)
	}
}

func TestSetImageRejectsUnknownContainer(t *testing.T) {
	t.Parallel()
	c := New("default", "dev")
	c.Seed(kube.KindDeployment, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default"},
		Spec:       appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "app:1.0"}}}}},
	})
	if err := c.SetImage(context.Background(), kube.KindDeployment, "default", "api", "missing", "app:2.0"); err == nil {
		t.Fatalf("expected an error for an unknown container name")
	}
}

// TestRolloutUndoPatchesTemplateAndAppendsNewRevision covers 16b's 'R'
// rollback against the fake cluster (--demo mode): RolloutUndo patches the
// Deployment's template to the target revision's, and — mirroring
// HelmRollback's own "rollback creates a new revision" shape — appends a
// synthesized new-highest-revision ReplicaSet so the rail visibly gains a
// new top entry.
func TestRolloutUndoPatchesTemplateAndAppendsNewRevision(t *testing.T) {
	t.Parallel()
	c := New("default", "dev")
	c.Seed(kube.KindDeployment, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default"},
		Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "app", Image: "api:2.0.0"}},
		}}},
	})
	c.Seed(kube.KindReplicaSet,
		&appsv1.ReplicaSet{
			ObjectMeta: metav1.ObjectMeta{
				Name: "api-old", Namespace: "default",
				Annotations:     map[string]string{"deployment.kubernetes.io/revision": "4"},
				OwnerReferences: []metav1.OwnerReference{{Kind: "Deployment", Name: "api"}},
			},
			Spec: appsv1.ReplicaSetSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "app", Image: "api:1.0.0"}},
			}}},
		},
		&appsv1.ReplicaSet{
			ObjectMeta: metav1.ObjectMeta{
				Name: "api-current", Namespace: "default",
				Annotations:     map[string]string{"deployment.kubernetes.io/revision": "5"},
				OwnerReferences: []metav1.OwnerReference{{Kind: "Deployment", Name: "api"}},
			},
			Spec: appsv1.ReplicaSetSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "app", Image: "api:2.0.0"}},
			}}},
		},
	)

	if err := c.RolloutUndo(context.Background(), "default", "api", 4); err != nil {
		t.Fatalf("RolloutUndo: %v", err)
	}

	deployObjs, _ := c.ListRaw(context.Background(), kube.KindDeployment, "default")
	deploy := deployObjs[0].(*appsv1.Deployment)
	if deploy.Spec.Template.Spec.Containers[0].Image != "api:1.0.0" {
		t.Fatalf("expected the deployment's template patched to revision 4's image, got %q", deploy.Spec.Template.Spec.Containers[0].Image)
	}

	rsObjs, _ := c.ListRaw(context.Background(), kube.KindReplicaSet, "default")
	if len(rsObjs) != 3 {
		t.Fatalf("expected a new synthesized ReplicaSet appended (3 total), got %d", len(rsObjs))
	}
	found := false
	for _, obj := range rsObjs {
		rs := obj.(*appsv1.ReplicaSet)
		if rs.Annotations["deployment.kubernetes.io/revision"] == "6" {
			found = true
			if rs.Spec.Template.Spec.Containers[0].Image != "api:1.0.0" {
				t.Fatalf("expected the new revision's template to carry the rolled-back image, got %q", rs.Spec.Template.Spec.Containers[0].Image)
			}
		}
	}
	if !found {
		t.Fatalf("expected a new revision 6 ReplicaSet among %+v", rsObjs)
	}
}

func TestRolloutUndoRejectsUnknownDeployment(t *testing.T) {
	t.Parallel()
	c := New("default", "dev")
	if err := c.RolloutUndo(context.Background(), "default", "missing", 1); err == nil {
		t.Fatalf("expected an error for an unknown deployment")
	}
}

func TestListRawFiltersByNamespace(t *testing.T) {
	t.Parallel()
	c := New("default", "dev")
	c.Seed(kube.KindPod,
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "a", Namespace: "default"}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "b", Namespace: "other"}},
	)

	all, err := c.ListRaw(context.Background(), kube.KindPod, "")
	if err != nil || len(all) != 2 {
		t.Fatalf("ListRaw(all) = %v, %v, want 2 objects", all, err)
	}
	scoped, err := c.ListRaw(context.Background(), kube.KindPod, "default")
	if err != nil || len(scoped) != 1 {
		t.Fatalf("ListRaw(default) = %v, %v, want 1 object", scoped, err)
	}
}

func TestDeleteResourceRemovesAndNotifies(t *testing.T) {
	t.Parallel()
	c := New("default", "dev")
	c.Seed(kube.KindPod, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "a", Namespace: "default"}})

	if err := c.DeleteResource(context.Background(), kube.KindPod, "default", "a"); err != nil {
		t.Fatalf("DeleteResource: %v", err)
	}
	remaining, _ := c.ListRaw(context.Background(), kube.KindPod, "default")
	if len(remaining) != 0 {
		t.Fatalf("expected pod removed, got %d remaining", len(remaining))
	}
	select {
	case msg := <-c.Events():
		if msg.Kind != kube.KindPod {
			t.Fatalf("notify kind = %v, want Pod", msg.Kind)
		}
	default:
		t.Fatalf("expected a ResourceChangedMsg after delete")
	}
}

func TestDeleteResourceMissingReturnsError(t *testing.T) {
	t.Parallel()
	c := New("default", "dev")
	if err := c.DeleteResource(context.Background(), kube.KindPod, "default", "missing"); err == nil {
		t.Fatalf("expected an error deleting a nonexistent pod")
	}
}

func TestCordonAndDrainSkipDaemonSetAndMirrorPods(t *testing.T) {
	t.Parallel()
	c := New("default", "dev")
	c.Seed(kube.KindNode, &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}})
	c.Seed(kube.KindPod,
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "default"}, Spec: corev1.PodSpec{NodeName: "node-1"}},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "ds", Namespace: "default", OwnerReferences: []metav1.OwnerReference{{Kind: "DaemonSet"}}},
			Spec:       corev1.PodSpec{NodeName: "node-1"},
		},
	)

	evicted, err := c.Drain(context.Background(), "node-1")
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if evicted != 1 {
		t.Fatalf("evicted = %d, want 1", evicted)
	}
	remaining, _ := c.ListRaw(context.Background(), kube.KindPod, "default")
	if len(remaining) != 1 {
		t.Fatalf("expected the DaemonSet pod to survive, got %d pods remaining", len(remaining))
	}
}

// TestTriggerCronJobStampsManualAnnotationsAndNotifies pins Plan Phase 2
// task 13's fake/live parity for TriggerCronJob: the same source/creator/
// time annotations kube.Cluster's real implementation stamps, an ownerless
// standalone Job, and a KindJob change notification (Phase 2 test 9).
func TestTriggerCronJobStampsManualAnnotationsAndNotifies(t *testing.T) {
	t.Parallel()
	c := New("default", "dev")
	c.Seed(kube.KindCronJob, &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{Name: "nightly", Namespace: "default", UID: types.UID("cj-uid-1")},
		Spec: batchv1.CronJobSpec{
			Schedule: "0 2 * * *",
			JobTemplate: batchv1.JobTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "batch"}},
				Spec: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "app:1.0"}}},
					},
				},
			},
		},
	})

	at := time.Date(2026, 3, 4, 2, 30, 0, 0, time.UTC)
	if err := c.TriggerCronJob(context.Background(), "default", "nightly", "nightly-manual-0230", "operator@example.com", at); err != nil {
		t.Fatalf("TriggerCronJob: %v", err)
	}

	objs, _ := c.ListRaw(context.Background(), kube.KindJob, "default")
	if len(objs) != 1 {
		t.Fatalf("expected one triggered Job, got %d", len(objs))
	}
	job := objs[0].(*batchv1.Job)
	if len(job.OwnerReferences) != 0 {
		t.Errorf("expected the triggered job detached (no OwnerReferences), got %+v", job.OwnerReferences)
	}
	if job.Annotations[kube.AnnotationCronJobName] != "nightly" {
		t.Errorf("AnnotationCronJobName = %q, want %q", job.Annotations[kube.AnnotationCronJobName], "nightly")
	}
	if job.Annotations[kube.AnnotationCronJobUID] != "cj-uid-1" {
		t.Errorf("AnnotationCronJobUID = %q, want %q", job.Annotations[kube.AnnotationCronJobUID], "cj-uid-1")
	}
	if job.Annotations[kube.AnnotationTriggeredBy] != "operator@example.com" {
		t.Errorf("AnnotationTriggeredBy = %q, want %q", job.Annotations[kube.AnnotationTriggeredBy], "operator@example.com")
	}
	if job.Annotations[kube.AnnotationTriggeredAt] != "2026-03-04T02:30:00Z" {
		t.Errorf("AnnotationTriggeredAt = %q, want %q", job.Annotations[kube.AnnotationTriggeredAt], "2026-03-04T02:30:00Z")
	}
	if job.Annotations["cronjob.kubernetes.io/instantiate"] != "manual" {
		t.Errorf("expected the manual-instantiate annotation, got %+v", job.Annotations)
	}

	select {
	case msg := <-c.Events():
		if msg.Kind != kube.KindJob {
			t.Fatalf("notify kind = %v, want Job", msg.Kind)
		}
	default:
		t.Fatalf("expected a KindJob ResourceChangedMsg after TriggerCronJob")
	}
}

// TestTriggerCronJobDeepCopiesTemplate pins Phase 2 test 1: unlike
// kube.Cluster's own equivalent test, this one is meaningful precisely
// because kube/fake.Cluster has no client-go serialization boundary between
// Create and Get — objects are stored as the same in-memory pointers that
// were passed in, so an aliased (not cloned) Labels map or Spec would be
// directly observable by mutating the returned Job and re-reading the
// source CronJob's own jobTemplate.
func TestTriggerCronJobDeepCopiesTemplate(t *testing.T) {
	t.Parallel()
	c := New("default", "dev")
	c.Seed(kube.KindCronJob, &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{Name: "nightly", Namespace: "default"},
		Spec: batchv1.CronJobSpec{
			Schedule: "0 2 * * *",
			JobTemplate: batchv1.JobTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "batch"}},
				Spec: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "app:1.0", Env: []corev1.EnvVar{{Name: "X", Value: "1"}}}}},
					},
				},
			},
		},
	})

	if err := c.TriggerCronJob(context.Background(), "default", "nightly", "nightly-manual-0230", "op", time.Now()); err != nil {
		t.Fatalf("TriggerCronJob: %v", err)
	}
	objs, _ := c.ListRaw(context.Background(), kube.KindJob, "default")
	job := objs[0].(*batchv1.Job)

	// Mutate the created Job's own copies...
	job.Labels["app"] = "mutated"
	job.Spec.Template.Spec.Containers[0].Image = "mutated:2.0"
	job.Spec.Template.Spec.Containers[0].Env[0].Value = "mutated"

	// ...and confirm the source CronJob's own template is untouched.
	cjObjs, _ := c.ListRaw(context.Background(), kube.KindCronJob, "default")
	tpl := cjObjs[0].(*batchv1.CronJob).Spec.JobTemplate
	if tpl.Labels["app"] != "batch" {
		t.Errorf("source template Labels mutated through the created Job's own copy: %+v", tpl.Labels)
	}
	if tpl.Spec.Template.Spec.Containers[0].Image != "app:1.0" {
		t.Errorf("source template container image mutated through the created Job's own copy: %q", tpl.Spec.Template.Spec.Containers[0].Image)
	}
	if tpl.Spec.Template.Spec.Containers[0].Env[0].Value != "1" {
		t.Errorf("source template container env mutated through the created Job's own copy: %q", tpl.Spec.Template.Spec.Containers[0].Env[0].Value)
	}
}

// TestTriggerCronJobRejectsDuplicateName pins task 13's "fake create rejects
// duplicate names" requirement and task 14's restage seam: the error wraps
// kube.ErrManualJobNameConflict just like a real AlreadyExists race would.
func TestTriggerCronJobRejectsDuplicateName(t *testing.T) {
	t.Parallel()
	c := New("default", "dev")
	c.Seed(kube.KindCronJob, &batchv1.CronJob{ObjectMeta: metav1.ObjectMeta{Name: "nightly", Namespace: "default"}})
	c.Seed(kube.KindJob, &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "nightly-manual-0230", Namespace: "default"}})

	err := c.TriggerCronJob(context.Background(), "default", "nightly", "nightly-manual-0230", "op", time.Now())
	if err == nil {
		t.Fatal("expected an error for a name already taken")
	}
	if !errors.Is(err, kube.ErrManualJobNameConflict) {
		t.Fatalf("expected errors.Is(err, kube.ErrManualJobNameConflict), got %v", err)
	}
}

// TestSetCronJobSuspendStampsAndClearsAnnotations pins task 13's suspend/
// resume parity: annotations stamped atomically with spec.suspend on
// suspend, cleared on resume, resourceVersion/generation bumped either way.
func TestSetCronJobSuspendStampsAndClearsAnnotations(t *testing.T) {
	t.Parallel()
	c := New("default", "dev")
	c.Seed(kube.KindCronJob, &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{Name: "nightly", Namespace: "default", ResourceVersion: "1", Generation: 3},
	})

	at := time.Date(2026, 5, 1, 9, 30, 0, 0, time.UTC)
	if err := c.SetCronJobSuspend(context.Background(), "default", "nightly", true, "1", 3, at); err != nil {
		t.Fatalf("SetCronJobSuspend(true): %v", err)
	}
	objs, _ := c.ListRaw(context.Background(), kube.KindCronJob, "default")
	cj := objs[0].(*batchv1.CronJob)
	if cj.Spec.Suspend == nil || !*cj.Spec.Suspend {
		t.Fatalf("Spec.Suspend = %v, want true", cj.Spec.Suspend)
	}
	if cj.Annotations[kube.AnnotationSuspendedAt] != "2026-05-01T09:30:00Z" {
		t.Errorf("AnnotationSuspendedAt = %q, want %q", cj.Annotations[kube.AnnotationSuspendedAt], "2026-05-01T09:30:00Z")
	}
	if cj.Annotations[kube.AnnotationSuspendedGeneration] != "4" {
		t.Errorf("AnnotationSuspendedGeneration = %q, want %q", cj.Annotations[kube.AnnotationSuspendedGeneration], "4")
	}
	if cj.Generation != 4 {
		t.Errorf("Generation = %d, want 4 (bumped by the suspend patch)", cj.Generation)
	}
	suspendedRV := cj.ResourceVersion
	if suspendedRV == "1" {
		t.Errorf("expected resourceVersion bumped past the original %q", "1")
	}

	if err := c.SetCronJobSuspend(context.Background(), "default", "nightly", false, suspendedRV, 4, time.Time{}); err != nil {
		t.Fatalf("SetCronJobSuspend(false): %v", err)
	}
	objs, _ = c.ListRaw(context.Background(), kube.KindCronJob, "default")
	cj = objs[0].(*batchv1.CronJob)
	if cj.Spec.Suspend == nil || *cj.Spec.Suspend {
		t.Fatalf("Spec.Suspend after resume = %v, want false", cj.Spec.Suspend)
	}
	if _, ok := cj.Annotations[kube.AnnotationSuspendedAt]; ok {
		t.Errorf("expected AnnotationSuspendedAt cleared on resume, got %+v", cj.Annotations)
	}
	if _, ok := cj.Annotations[kube.AnnotationSuspendedGeneration]; ok {
		t.Errorf("expected AnnotationSuspendedGeneration cleared on resume, got %+v", cj.Annotations)
	}
	if cj.ResourceVersion == suspendedRV {
		t.Errorf("expected resourceVersion bumped again on resume, still %q", cj.ResourceVersion)
	}
}

// TestSetCronJobSuspendConflictsOnStaleResourceVersion pins task 13's
// "conflicts are testable" requirement: a stale resourceVersion precondition
// returns a typed Conflict error, and the CronJob is left untouched.
func TestSetCronJobSuspendConflictsOnStaleResourceVersion(t *testing.T) {
	t.Parallel()
	c := New("default", "dev")
	c.Seed(kube.KindCronJob, &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{Name: "nightly", Namespace: "default", ResourceVersion: "5", Generation: 1},
	})

	err := c.SetCronJobSuspend(context.Background(), "default", "nightly", true, "stale", 1, time.Now())
	if err == nil {
		t.Fatal("expected an error for a stale resourceVersion")
	}
	if !apierrors.IsConflict(err) {
		t.Fatalf("expected apierrors.IsConflict(err), got %v", err)
	}
	objs, _ := c.ListRaw(context.Background(), kube.KindCronJob, "default")
	cj := objs[0].(*batchv1.CronJob)
	if cj.Spec.Suspend != nil {
		t.Errorf("expected Spec.Suspend untouched after a conflict, got %v", cj.Spec.Suspend)
	}
	if cj.ResourceVersion != "5" {
		t.Errorf("expected ResourceVersion untouched after a conflict, got %q", cj.ResourceVersion)
	}
}

// TestSetCronJobScheduleConflictsAgainstFakeCluster is the fake-cluster
// counterpart to mutate_test.go's TestSetCronJobSchedulePropagatesConflict —
// this one exercises kube/fake's own resourceVersion enforcement rather
// than a reactor standing in for one, so a caller can drive a schedule-
// editor Conflict path (§36d) entirely against --demo/task tests.
func TestSetCronJobScheduleConflictsAgainstFakeCluster(t *testing.T) {
	t.Parallel()
	c := New("default", "dev")
	c.Seed(kube.KindCronJob, &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{Name: "nightly", Namespace: "default", ResourceVersion: "5"},
		Spec:       batchv1.CronJobSpec{Schedule: "0 2 * * *"},
	})

	_, err := c.SetCronJobSchedule(context.Background(), "default", "nightly", kube.CronJobScheduleEdit{
		Schedule: "*/15 * * * *", ResourceVersion: "stale",
	})
	if err == nil {
		t.Fatal("expected an error for a stale resourceVersion")
	}
	if !apierrors.IsConflict(err) {
		t.Fatalf("expected apierrors.IsConflict(err), got %v", err)
	}
	objs, _ := c.ListRaw(context.Background(), kube.KindCronJob, "default")
	cj := objs[0].(*batchv1.CronJob)
	if cj.Spec.Schedule != "0 2 * * *" {
		t.Errorf("expected Spec.Schedule untouched after a conflict, got %q", cj.Spec.Schedule)
	}
}

// TestSetCronJobScheduleSetsAndClearsTimeZone is the fake-cluster
// counterpart to mutate_test.go's TestSetCronJobScheduleSetsAndClearsTimeZone
// — same set/clear/untouched contract, plus the returned
// kube.CronJobScheduleResult and a KindCronJob notification each time
// (Phase 2 test 9).
func TestSetCronJobScheduleSetsAndClearsTimeZone(t *testing.T) {
	t.Parallel()
	c := New("default", "dev")
	c.Seed(kube.KindCronJob, &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{Name: "nightly", Namespace: "default", ResourceVersion: "1"},
		Spec:       batchv1.CronJobSpec{Schedule: "0 2 * * *"},
	})

	tz := "America/New_York"
	result, err := c.SetCronJobSchedule(context.Background(), "default", "nightly", kube.CronJobScheduleEdit{
		Schedule: "0 2 * * *", TimeZone: &tz, ResourceVersion: "1",
	})
	if err != nil {
		t.Fatalf("SetCronJobSchedule (set timezone): %v", err)
	}
	if result.TimeZone != tz {
		t.Errorf("result.TimeZone = %q, want %q", result.TimeZone, tz)
	}
	select {
	case msg := <-c.Events():
		if msg.Kind != kube.KindCronJob {
			t.Fatalf("notify kind = %v, want CronJob", msg.Kind)
		}
	default:
		t.Fatalf("expected a KindCronJob ResourceChangedMsg after SetCronJobSchedule")
	}

	// A nil TimeZone (schedule-only edit) must never touch the existing
	// value.
	result2, err := c.SetCronJobSchedule(context.Background(), "default", "nightly", kube.CronJobScheduleEdit{
		Schedule: "*/30 * * * *", ResourceVersion: result.ResourceVersion,
	})
	if err != nil {
		t.Fatalf("SetCronJobSchedule (schedule only): %v", err)
	}
	if result2.TimeZone != tz {
		t.Errorf("result2.TimeZone = %q, want the untouched %q", result2.TimeZone, tz)
	}

	// An explicit clear removes it.
	emptyTZ := ""
	result3, err := c.SetCronJobSchedule(context.Background(), "default", "nightly", kube.CronJobScheduleEdit{
		Schedule: "*/30 * * * *", TimeZone: &emptyTZ, ResourceVersion: result2.ResourceVersion,
	})
	if err != nil {
		t.Fatalf("SetCronJobSchedule (clear timezone): %v", err)
	}
	if result3.TimeZone != "" {
		t.Errorf("result3.TimeZone = %q, want cleared \"\"", result3.TimeZone)
	}
}

func TestGetYAMLStripsManagedFields(t *testing.T) {
	t.Parallel()
	c := New("default", "dev")
	c.Seed(kube.KindPod, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "api", Namespace: "default", ResourceVersion: "7",
			ManagedFields: []metav1.ManagedFieldsEntry{{Manager: "kubectl"}},
		},
	})

	yaml, rv, err := c.GetYAML(context.Background(), kube.KindPod, "default", "api")
	if err != nil {
		t.Fatalf("GetYAML: %v", err)
	}
	if rv != "7" {
		t.Fatalf("resourceVersion = %q, want 7", rv)
	}
	if strings.Contains(yaml, "managedFields") {
		t.Fatalf("expected managedFields stripped from yaml:\n%s", yaml)
	}
	if !strings.Contains(yaml, "name: api") {
		t.Fatalf("expected the pod name in the yaml:\n%s", yaml)
	}
}

func TestObjectEventsFiltersByInvolvedObject(t *testing.T) {
	t.Parallel()
	c := New("default", "dev")
	c.Seed(kube.KindEvent,
		&corev1.Event{
			ObjectMeta:     metav1.ObjectMeta{Name: "ev-1", Namespace: "default"},
			InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "api"},
			Reason:         "BackOff",
		},
		&corev1.Event{
			ObjectMeta:     metav1.ObjectMeta{Name: "ev-2", Namespace: "default"},
			InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "other"},
		},
	)

	got, err := c.ObjectEvents(context.Background(), "default", kube.KindPod, "api")
	if err != nil {
		t.Fatalf("ObjectEvents: %v", err)
	}
	if len(got) != 1 || got[0].Reason != "BackOff" {
		t.Fatalf("ObjectEvents = %+v, want one BackOff event", got)
	}
}

func TestSwitchNamespaceAndContext(t *testing.T) {
	t.Parallel()
	c := New("default", "dev")
	c.AddContext("prod", "prod-ns")

	c.SwitchNamespace("kube-system")
	if c.CurrentNamespace() != "kube-system" {
		t.Fatalf("CurrentNamespace = %q, want kube-system", c.CurrentNamespace())
	}

	if err := c.SwitchContext(context.Background(), "prod"); err != nil {
		t.Fatalf("SwitchContext: %v", err)
	}
	if c.CurrentContext() != "prod" || c.CurrentNamespace() != "prod-ns" {
		t.Fatalf("after SwitchContext: context=%q namespace=%q, want prod/prod-ns", c.CurrentContext(), c.CurrentNamespace())
	}

	if err := c.SwitchContext(context.Background(), "does-not-exist"); err == nil {
		t.Fatalf("expected an error switching to an unknown context")
	}
}

func TestStreamPodLogsReplaysSeededLines(t *testing.T) {
	t.Parallel()
	c := New("default", "dev")
	c.SeedLogs("default", "api", []string{"line one", "line two"})

	rc, err := c.StreamPodLogs(context.Background(), kube.LogStreamRequest{Namespace: "default", PodName: "api"})
	if err != nil {
		t.Fatalf("StreamPodLogs: %v", err)
	}
	defer func() { _ = rc.Close() }()
	buf := make([]byte, 1024)
	n, _ := rc.Read(buf)
	got := string(buf[:n])
	if !strings.Contains(got, "line one") || !strings.Contains(got, "line two") {
		t.Fatalf("streamed content = %q, want both seeded lines", got)
	}
}

func TestSetConnStateEmitsOnChannel(t *testing.T) {
	t.Parallel()
	c := New("default", "dev")
	c.SetConnState(kube.ConnState{Phase: kube.ConnReconnecting, Err: "dial timeout"})

	if got := c.ConnState().Phase; got != kube.ConnReconnecting {
		t.Fatalf("ConnState().Phase = %v, want Reconnecting", got)
	}
	select {
	case msg := <-c.ConnEvents():
		if msg.Phase != kube.ConnReconnecting {
			t.Fatalf("ConnEvents phase = %v, want Reconnecting", msg.Phase)
		}
	default:
		t.Fatalf("expected a ConnStateMsg after SetConnState")
	}
}

func TestNewDemoIsFeatureComplete(t *testing.T) {
	t.Parallel()
	c := NewDemo()
	ctx := context.Background()

	pods, err := c.ListRaw(ctx, kube.KindPod, "")
	if err != nil || len(pods) != 14 {
		t.Fatalf("ListRaw(Pod) = %d, %v, want 14 fixture pods", len(pods), err)
	}
	if pod, ok := findPod(pods, "api-7d9f6c8-abcde"); !ok || len(pod.Spec.Containers) < 2 {
		t.Fatalf("expected a multi-container pod (10a's exec-picker is otherwise unreachable in --demo), got %+v (ok=%v)", pod, ok)
	}
	deploys, _ := c.ListRaw(ctx, kube.KindDeployment, "")
	if len(deploys) != 16 {
		t.Fatalf("expected 16 deployment fixtures, got %d", len(deploys))
	}
	nodes, _ := c.ListRaw(ctx, kube.KindNode, "")
	if len(nodes) != 4 {
		t.Fatalf("expected 4 node fixtures, got %d", len(nodes))
	}
	namespaces, _ := c.ListRaw(ctx, kube.KindNamespace, "")
	if len(namespaces) != 10 {
		t.Fatalf("expected 10 namespace fixtures, got %d", len(namespaces))
	}
	events, err := c.ObjectEvents(ctx, "default", kube.KindPod, "worker-0")
	if err != nil || len(events) == 0 {
		t.Fatalf("expected events for the crashlooping pod, got %d, %v", len(events), err)
	}
	nsEvents, err := c.NamespaceEvents(ctx, "default")
	if err != nil || len(nsEvents) != len(events) {
		t.Fatalf("NamespaceEvents(default) = %d, %v, want %d", len(nsEvents), err, len(events))
	}

	// Every consumer seam must at least be callable without panicking.
	podMetrics, err := c.PodMetricsByNamespace(ctx, "default")
	if err != nil {
		t.Fatalf("PodMetricsByNamespace: %v", err)
	}
	if pm, ok := podMetrics["api-7d9f6c8-abcde"]; !ok || pm.CPU == "n/a" || pm.CPUMilli == 0 {
		t.Fatalf("expected real (non-n/a) CPU usage for a Running pod, got %+v (ok=%v)", pm, ok)
	}
	if nm, err := c.NodeMetrics(ctx); err != nil || len(nm) != 4 {
		t.Fatalf("NodeMetrics = %d, %v, want 4 fixture nodes", len(nm), err)
	}
	if _, _, err := c.GetYAML(ctx, kube.KindPod, "default", "worker-0"); err != nil {
		t.Fatalf("GetYAML: %v", err)
	}
	if _, err := c.StreamPodLogs(ctx, kube.LogStreamRequest{Namespace: "default", PodName: "worker-0"}); err != nil {
		t.Fatalf("StreamPodLogs: %v", err)
	}
}

// TestPodAndNodeMetricsAreDeterministicNotNA pins the §6a/CLAUDE.md
// feature-completeness fix: --demo's CPU/MEM columns used to be
// unconditionally "n/a" for every pod and node, so those bars/columns could
// never actually be exercised by driving --demo mode. Usage is now
// synthesized from each object's own limits/allocatable, and must be the
// same across repeated calls (a stable demo, not flickering random noise).
func TestPodAndNodeMetricsAreDeterministicNotNA(t *testing.T) {
	t.Parallel()
	c := NewDemo()
	ctx := context.Background()

	first, err := c.PodMetricsByNamespace(ctx, "default")
	if err != nil {
		t.Fatalf("PodMetricsByNamespace: %v", err)
	}
	pm, ok := first["api-7d9f6c8-abcde"]
	if !ok {
		t.Fatal("expected api-7d9f6c8-abcde in the fixture set")
	}
	if pm.CPU == "n/a" || pm.MEM == "n/a" || pm.CPUMilli <= 0 || pm.MemBytes <= 0 {
		t.Fatalf("expected real usage for a Running pod, got %+v", pm)
	}

	second, _ := c.PodMetricsByNamespace(ctx, "default")
	if second["api-7d9f6c8-abcde"] != pm {
		t.Fatalf("expected deterministic usage across calls, got %+v then %+v", pm, second["api-7d9f6c8-abcde"])
	}

	nodeMetrics, err := c.NodeMetrics(ctx)
	if err != nil {
		t.Fatalf("NodeMetrics: %v", err)
	}
	for name, nm := range nodeMetrics {
		if nm.CPU == "n/a" || nm.MEM == "n/a" || nm.CPUMilli <= 0 || nm.MemBytes <= 0 {
			t.Fatalf("expected real usage for node %q, got %+v", name, nm)
		}
	}
}

// findPod locates name among objs (runtime.Object values from ListRaw),
// returning the underlying *corev1.Pod.
func findPod(objs []runtime.Object, name string) (*corev1.Pod, bool) {
	for _, obj := range objs {
		if pod, ok := obj.(*corev1.Pod); ok && pod.Name == name {
			return pod, true
		}
	}
	return nil, false
}

// TestDemoDiscoveredKindsCarryTheirOwnAPIGroup guards the fix for a fixture
// bug that reached the screen: demoDiscoveredKind hardcoded cert-manager.io
// as every demo kind's Group, so in --demo a ServiceMonitor's 14a breadcrumb
// tag, its 14c goto type label and its descriptor's "custom resource · …"
// Describe all claimed it was a cert-manager type. Each demo kind must
// report the group its own CRD declares.
func TestDemoDiscoveredKindsCarryTheirOwnAPIGroup(t *testing.T) {
	t.Parallel()
	want := map[string]string{
		"Certificate":    "cert-manager.io",
		"ServiceMonitor": "monitoring.coreos.com",
		"Prometheus":     "monitoring.coreos.com",
		"Application":    "argoproj.io",
		"HTTPRoute":      "gateway.networking.k8s.io",
	}
	got := map[string]string{}
	for _, dk := range NewDemo().DiscoveredKinds() {
		got[dk.Kind] = dk.Group
		// The GVR the dynamic client would use has to agree with it, or a
		// read of this kind goes to the wrong endpoint entirely.
		if dk.GVR.Group != dk.Group {
			t.Errorf("%s: GVR group %q disagrees with Group %q", dk.Kind, dk.GVR.Group, dk.Group)
		}
	}
	for kind, group := range want {
		if got[kind] != group {
			t.Errorf("%s: got group %q, want %q", kind, got[kind], group)
		}
	}
}

// Compile-time interface satisfaction: the fake must actually implement
// kube.Mutator, the only formally exported multi-method consumer contract
// it stands in for today.
var _ kube.Mutator = (*Cluster)(nil)

// TestDemoSeedsFluxKinds guards --demo's §30a fixtures. The demo cluster is
// the only way to exercise the Flux screens without a live Flux install, so
// a fixture that silently stops being seeded takes the screens with it.
func TestDemoSeedsFluxKinds(t *testing.T) {
	t.Parallel()
	c := NewDemo()
	ctx := context.Background()

	for kind, wantMin := range map[kube.ResourceKind]int{
		"Kustomization":          5,
		kube.KindFluxHelmRelease: 2,
		"GitRepository":          1,
	} {
		objs, err := c.ListRaw(ctx, kind, "flux-system")
		if err != nil {
			t.Fatalf("ListRaw(%s): %v", kind, err)
		}
		if len(objs) < wantMin {
			t.Errorf("%s: seeded %d objects, want at least %d", kind, len(objs), wantMin)
		}
	}

	// Flux's HelmReleases must be seeded under the substituted registry
	// kind. Seeding the bare "HelmRelease" would route them into §18a's
	// Helm-3 list — the exact collision §30a exists to prevent.
	if objs, _ := c.ListRaw(ctx, kube.ResourceKind("HelmRelease"), "flux-system"); len(objs) != 0 {
		t.Errorf("Flux HelmReleases must not be seeded under the bare API Kind, found %d", len(objs))
	}

	// Every branch of §30a's status precedence needs a fixture, or the demo
	// can't show what the feature is for.
	var suspended, stalled, reconciling bool
	objs, _ := c.ListRaw(ctx, kube.ResourceKind("Kustomization"), "flux-system")
	for _, o := range objs {
		u := o.(*unstructured.Unstructured)
		if v, _, _ := unstructured.NestedBool(u.Object, "spec", "suspend"); v {
			suspended = true
		}
		conds, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
		for _, cd := range conds {
			cm := cd.(map[string]any)
			typ, _, _ := unstructured.NestedString(cm, "type")
			st, _, _ := unstructured.NestedString(cm, "status")
			if typ == "Reconciling" && st == "True" {
				reconciling = true
			}
		}
	}
	hrs, _ := c.ListRaw(ctx, kube.KindFluxHelmRelease, "flux-system")
	for _, o := range hrs {
		conds, _, _ := unstructured.NestedSlice(o.(*unstructured.Unstructured).Object, "status", "conditions")
		for _, cd := range conds {
			cm := cd.(map[string]any)
			typ, _, _ := unstructured.NestedString(cm, "type")
			st, _, _ := unstructured.NestedString(cm, "status")
			if typ == "Stalled" && st == "True" {
				stalled = true
			}
		}
	}
	if !suspended || !stalled || !reconciling {
		t.Errorf("demo must cover every status branch: suspended=%v stalled=%v reconciling=%v",
			suspended, stalled, reconciling)
	}
}

// TestDemoFluxEventsCarryTheRevisionAnnotation pins the fake's event
// readers: §32a reads the revision from metadata.annotations, so a reader
// that drops them makes every revision row disappear under the fake while
// the real cluster still works.
func TestDemoFluxEventsCarryTheRevisionAnnotation(t *testing.T) {
	t.Parallel()
	c := NewDemo()
	events, err := c.NamespaceEvents(context.Background(), "flux-system")
	if err != nil {
		t.Fatalf("NamespaceEvents: %v", err)
	}
	var withRevision, withSubject int
	for _, e := range events {
		if kube.FluxEventRevision(e) != "" {
			withRevision++
		}
		if kube.FluxCommitSubject(e.Message) != "" {
			withSubject++
		}
	}
	if withRevision < 2 {
		t.Errorf("expected at least 2 events carrying a Flux revision annotation, got %d", withRevision)
	}
	if withSubject < 1 {
		t.Errorf("expected a source-controller event carrying a commit subject, got %d", withSubject)
	}
}
