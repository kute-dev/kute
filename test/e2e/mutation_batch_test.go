//go:build e2e

package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/kute-dev/kute/internal/kube"
)

func TestJobRerunCreatesAVisibleDetachedJob(t *testing.T) {
	const sourceName = "phase3-job"
	client := mutationClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	source, err := client.BatchV1().Jobs(Namespace).Get(ctx, sourceName, metav1.GetOptions{})
	cancel()
	if err != nil {
		t.Fatalf("getting Job fixture %s: %v", sourceName, err)
	}
	// Remove leftovers from an interrupted local run so the staged name is
	// deterministic and the fixture remains reusable.
	ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
	jobs, err := client.BatchV1().Jobs(Namespace).List(ctx, metav1.ListOptions{})
	if err == nil {
		for _, job := range jobs.Items {
			if strings.HasPrefix(job.Name, sourceName+"-rerun-") {
				deleteJobNow(t, job.Name)
			}
		}
	}
	cancel()

	a := Launch(t)
	a.WaitFor("api-", Connect)
	a.gotoKind(t, "jobs", "Jobs")
	a.WaitFor(sourceName, Settle)
	a.filterTo(t, sourceName)
	a.Press("R")
	a.WaitForAll(Settle, "RERUN", sourceName+"-rerun-1")
	a.Enter()

	var rerunName, rerunRV string
	waitForAPI(t, "rerun Job", func(ctx context.Context) (bool, error) {
		list, err := client.BatchV1().Jobs(Namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return false, err
		}
		for i := range list.Items {
			job := &list.Items[i]
			if strings.HasPrefix(job.Name, sourceName+"-rerun-") {
				rerunName, rerunRV = job.Name, job.ResourceVersion
				if len(job.OwnerReferences) != 0 || job.Annotations[kube.AnnotationTriggeredBy] == "" {
					return false, nil
				}
				return true, nil
			}
		}
		return false, nil
	})
	if rerunRV == "" || rerunRV == source.ResourceVersion {
		t.Fatalf("rerun resourceVersion = %q, source = %q", rerunRV, source.ResourceVersion)
	}
	t.Cleanup(func() { deleteJobNow(t, rerunName) })
	a.WaitForAll(Settle, "Jobs", rerunName)

	ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
	afterSource, err := client.BatchV1().Jobs(Namespace).Get(ctx, sourceName, metav1.GetOptions{})
	cancel()
	if err != nil {
		t.Fatalf("source Job disappeared after rerun: %v", err)
	}
	if afterSource.UID != source.UID {
		t.Fatalf("rerun replaced the source Job: before UID=%s after UID=%s", source.UID, afterSource.UID)
	}
}

func TestCronJobRunNowAndScheduleEdit(t *testing.T) {
	const name = "phase3-cron"
	const beforeSchedule, afterSchedule = "0 2 * * *", "15 3 * * *"
	client := mutationClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	before, err := client.BatchV1().CronJobs(Namespace).Get(ctx, name, metav1.GetOptions{})
	cancel()
	if err != nil {
		t.Fatalf("getting CronJob fixture %s: %v", name, err)
	}
	if before.Spec.Schedule != beforeSchedule {
		t.Fatalf("CronJob fixture schedule = %q, want %q", before.Spec.Schedule, beforeSchedule)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
	jobs, err := client.BatchV1().Jobs(Namespace).List(ctx, metav1.ListOptions{})
	if err == nil {
		for _, job := range jobs.Items {
			if job.Annotations[kube.AnnotationCronJobName] == name {
				deleteJobNow(t, job.Name)
			}
		}
	}
	cancel()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, err := client.BatchV1().CronJobs(Namespace).Patch(ctx, name, types.MergePatchType, []byte("{\"spec\":{\"schedule\":\"0 2 * * *\"}}"), metav1.PatchOptions{})
		if err != nil {
			t.Errorf("restoring CronJob schedule: %v", err)
		}
	})

	a := Launch(t)
	a.WaitFor("api-", Connect)
	a.gotoKind(t, "cronjobs", "CronJobs")
	a.WaitFor(name, Settle)
	a.filterTo(t, name)
	a.Press("R")
	a.WaitForAll(Settle, "RUN NOW", "create job", name)
	a.Enter()
	a.WaitFor("job created", Settle)
	manual := getJobByAnnotation(t, kube.AnnotationCronJobName, name)
	t.Cleanup(func() { deleteJobNow(t, manual.Name) })
	a.WaitForAll(Settle, "CronJobs", manual.Name)

	// Schedule is a pushed, context-preserving editor with a real
	// resourceVersion precondition and a one-step undo.
	a.Press("S")
	a.WaitForAll(Settle, "cronjob/"+name, "Schedule", beforeSchedule)
	replaceTypedValue(a, beforeSchedule, afterSchedule)
	a.Enter()
	a.WaitFor("updated schedule to "+afterSchedule, Settle)
	var scheduleRV string
	waitForAPI(t, "CronJob schedule edit", func(ctx context.Context) (bool, error) {
		cj, err := client.BatchV1().CronJobs(Namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		scheduleRV = cj.ResourceVersion
		return cj.Spec.Schedule == afterSchedule, nil
	})
	requireNewResourceVersion(t, before.ResourceVersion, scheduleRV)
	a.WaitForAll(Settle, "SCHEDULE", afterSchedule, "u undo")
}
