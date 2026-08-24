//go:build e2e && e2e_soak

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/clientcmd"
)

func TestEventStormConvergesWithoutAmplification(t *testing.T) {
	widgetPatches := soakCount(t, "KUTE_E2E_STORM_WIDGET_PATCHES", 300)
	podPatches := soakCount(t, "KUTE_E2E_STORM_POD_PATCHES", 300)
	eventsPerScreen := soakCount(t, "KUTE_E2E_STORM_EVENTS_PER_SCREEN", 180)
	run := soakName("storm")
	podName := run + "-pod"
	widgetName := run + "-widget"
	// Widget's fixture printer column is intentionally narrow. Keep the
	// unique sentinel short enough to render in full so convergence is
	// asserted on the real final value rather than an impossible-to-match
	// pre-truncation string.
	finalWidget := "f" + run[len(run)-6:]
	finalPodLabel := "final-" + run[len(run)-6:]
	selector := "kute.dev/soak-run=" + run

	client := e2eClientset(t)
	ctx, cancel := context.WithTimeout(t.Context(), Settle)
	createStormPod(t, ctx, client, podName, run)

	restCfg, err := clientcmd.BuildConfigFromFlags("", KubeconfigPath())
	if err != nil {
		t.Fatal(err)
	}
	dynamicClient, err := dynamic.NewForConfig(restCfg)
	if err != nil {
		t.Fatal(err)
	}
	widgets := dynamicClient.Resource(schema.GroupVersionResource{Group: "kute.dev", Version: "v1", Resource: "widgets"}).Namespace(Namespace)
	_, err = widgets.Create(ctx, &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "kute.dev/v1", "kind": "Widget",
		"metadata": map[string]any{"name": widgetName, "namespace": Namespace, "labels": map[string]any{"kute.dev/soak-run": run}},
		"spec":     map[string]any{"size": "storm", "colour": "start"},
		"status":   map[string]any{"phase": "Starting"},
	}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("creating storm Widget: %v", err)
	}
	cancel()
	t.Cleanup(func() {
		//nolint:usetesting // cleanup must survive testing.T's context cancellation
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		_ = widgets.Delete(cleanupCtx, widgetName, metav1.DeleteOptions{})
		_ = client.CoreV1().Events(Namespace).DeleteCollection(cleanupCtx, metav1.DeleteOptions{}, metav1.ListOptions{LabelSelector: selector})
	})

	a := Launch(t)
	a.WaitFor(podName, Connect)
	a.gotoKind(t, "widgets", "Widgets")
	a.WaitFor(widgetName, Settle)
	a.gotoKind(t, "pods", "Pods")
	a.WaitFor(podName, Settle)
	a.selectRow(t, podName)
	a.Enter()
	a.WaitForAll(Settle, podName, "CONTAINERS")

	// Warm every cache used below before the baseline. The storm must measure
	// repeated refreshes, not the intentional first informer for Events or the
	// Timeline's auxiliary kinds.
	a.Press("e")
	a.WaitLoaded(Settle)
	a.backToPodDetail()
	a.Press("t")
	a.WaitLoaded(Settle)
	a.backToPodDetail()
	baselineRuntime := settledSnapshot(a)
	baselineRequests := a.Proxy().Counts()

	runBurstWithFences(t, a, "Widget patches", func(ctx context.Context) error {
		for i := 0; i < widgetPatches; i++ {
			colour := fmt.Sprintf("storm-%03d", i)
			phase := fmt.Sprintf("Phase-%03d", i)
			if i == widgetPatches-1 {
				colour, phase = finalWidget, finalWidget
			}
			patch, _ := json.Marshal(map[string]any{"spec": map[string]any{"colour": colour}, "status": map[string]any{"phase": phase}})
			if _, err := widgets.Patch(ctx, widgetName, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
				return err
			}
		}
		return nil
	})

	runBurstWithFences(t, a, "Pod metadata and status patches", func(ctx context.Context) error {
		pods := client.CoreV1().Pods(Namespace)
		for i := 0; i < podPatches; i++ {
			label := fmt.Sprintf("seq-%03d", i)
			phase := corev1.PodPending
			if i == podPatches-1 {
				label, phase = finalPodLabel, corev1.PodFailed
			}
			metaPatch, _ := json.Marshal(map[string]any{"metadata": map[string]any{"labels": map[string]any{"storm-seq": label}}})
			if _, err := pods.Patch(ctx, podName, types.MergePatchType, metaPatch, metav1.PatchOptions{}); err != nil {
				return err
			}
			statusPatch, _ := json.Marshal(map[string]any{"status": map[string]any{
				"phase": phase,
				"conditions": []any{map[string]any{
					"type": "Ready", "status": "False", "reason": "Storming", "message": label,
					"lastProbeTime": metav1.Now().Format(time.RFC3339Nano), "lastTransitionTime": metav1.Now().Format(time.RFC3339Nano),
				}},
			}})
			if _, err := pods.Patch(ctx, podName, types.MergePatchType, statusPatch, metav1.PatchOptions{}, "status"); err != nil {
				return err
			}
		}
		return nil
	})
	a.WaitForAll(Settle, "storm-seq="+finalPodLabel, "Failed")

	a.Press("e")
	a.WaitLoaded(Settle)
	eventsFinal := run + "-events-final"
	runBurstWithFences(t, a, "Events-screen event objects", func(ctx context.Context) error {
		return createAndUpdateEventBurst(ctx, client.CoreV1().Events(Namespace), podName, run, "events", eventsPerScreen, eventsFinal)
	})
	a.WaitFor(eventsFinal, Settle)
	a.backToPodDetail()

	a.Press("t")
	a.WaitLoaded(Settle)
	timelineFinal := run + "-timeline-final"
	runBurstWithFences(t, a, "Timeline-screen event objects", func(ctx context.Context) error {
		return createAndUpdateEventBurst(ctx, client.CoreV1().Events(Namespace), podName, run, "timeline", eventsPerScreen, timelineFinal)
	})
	a.WaitFor(timelineFinal, Settle)

	// Final state wins and stays put after the input burst drains.
	for range 5 {
		if frame := a.WaitFor(timelineFinal, soakInputBudget); !strings.Contains(frame, timelineFinal) {
			t.Fatalf("timeline final marker became unstable:\n%s", frame)
		}
		_ = a.InputFence()
	}
	assertRequestGrowthBounded(t, baselineRequests, a.Proxy().History(), 20)
	a.Esc()
	a.WaitFor("CONTAINERS", Settle)
	a.Esc()
	a.gotoKind(t, "widgets", "Widgets")
	a.WaitForAll(Settle, widgetName, finalWidget)
	for range 5 {
		_ = a.InputFence()
		if !strings.Contains(a.Frame(), finalWidget) {
			t.Fatalf("Widget regressed after the storm settled:\n%s", a.Frame())
		}
	}

	// Remove the intentionally-retained Event payload before comparing heap;
	// the cache retaining live objects is policy, not a leak.
	deleteCtx, deleteCancel := context.WithTimeout(t.Context(), Settle)
	defer deleteCancel()
	if err := client.CoreV1().Events(Namespace).DeleteCollection(deleteCtx, metav1.DeleteOptions{}, metav1.ListOptions{LabelSelector: selector}); err != nil {
		t.Fatalf("deleting storm Events: %v", err)
	}
	a.gotoKind(t, "pods", "Pods")
	a.WaitFor(podName, Settle)
	a.selectRow(t, podName)
	a.Enter()
	a.WaitForAll(Settle, "storm-seq="+finalPodLabel, "CONTAINERS")
	a.WaitGone(timelineFinal, Settle)
	gotRuntime := settledSnapshot(a)
	gotRequests := a.Proxy().Counts()
	assertRuntimeBudget(t, baselineRuntime, gotRuntime, 64<<20, 40)
	requireOnlyWatchesActive(t, gotRequests)
}

func createStormPod(t *testing.T, ctx context.Context, client kubernetes.Interface, name, run string) {
	t.Helper()
	_, err := client.CoreV1().Pods(Namespace).Create(ctx, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: Namespace, Labels: map[string]string{"kute.dev/soak-run": run, "storm-seq": "start"}},
		Spec: corev1.PodSpec{
			SchedulerName: "kute-e2e-never-schedule",
			Containers:    []corev1.Container{{Name: "storm", Image: "busybox:1.37@sha256:9532d8c39891ca2ecde4d30d7710e01fb739c87a8b9299685c63704296b16028"}},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("creating storm Pod: %v", err)
	}
	t.Cleanup(func() {
		//nolint:usetesting // cleanup must survive testing.T's context cancellation
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		zero := int64(0)
		_ = client.CoreV1().Pods(Namespace).Delete(cleanupCtx, name, metav1.DeleteOptions{GracePeriodSeconds: &zero})
	})
}

func createAndUpdateEventBurst(ctx context.Context, events typedcorev1.EventInterface, podName, run, prefix string, count int, finalMarker string) error {
	for i := 0; i < count; i++ {
		marker := fmt.Sprintf("%s-%03d", prefix, i)
		if i == count-1 {
			marker = finalMarker
		}
		now := metav1.Now()
		created, err := events.Create(ctx, &corev1.Event{
			ObjectMeta:     metav1.ObjectMeta{Name: fmt.Sprintf("%s-%s-%03d", run, prefix, i), Namespace: Namespace, Labels: map[string]string{"kute.dev/soak-run": run}},
			InvolvedObject: corev1.ObjectReference{Kind: "Pod", Namespace: Namespace, Name: podName},
			Type:           corev1.EventTypeWarning, Reason: "Storm" + fmt.Sprintf("%03d", i), Message: marker,
			Source: corev1.EventSource{Component: "kute-e2e-soak"}, FirstTimestamp: now, LastTimestamp: now, Count: 1,
		}, metav1.CreateOptions{})
		if err != nil {
			return err
		}
		created.Count = 2
		created.Message = marker
		created.LastTimestamp = metav1.Now()
		if _, err := events.Update(ctx, created, metav1.UpdateOptions{}); err != nil {
			return err
		}
	}
	return nil
}
