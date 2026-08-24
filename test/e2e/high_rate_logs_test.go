//go:build e2e && e2e_soak

package e2e

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
)

func TestHighRateLogsStayBoundedAndResponsive(t *testing.T) {
	RequireCluster(t)
	linesPerBatch := soakCount(t, "KUTE_E2E_SOAK_LOG_LINES_PER_BATCH", 12000)
	if linesPerBatch <= 5000 {
		t.Fatalf("KUTE_E2E_SOAK_LOG_LINES_PER_BATCH must exceed the 5000-entry viewer buffer, got %d", linesPerBatch)
	}
	name := soakName("log-soak")
	client := e2eClientset(t)
	restCfg, err := clientcmd.BuildConfigFromFlags("", KubeconfigPath())
	if err != nil {
		t.Fatal(err)
	}
	createLogSoakPod(t, client, name)
	waitForAPI(t, "log soak pod to run", func(ctx context.Context) (bool, error) {
		pod, err := client.CoreV1().Pods(Namespace).Get(ctx, name, metav1.GetOptions{})
		return err == nil && pod.Status.Phase == corev1.PodRunning, err
	})

	a := Launch(t)
	a.WaitFor(name, Connect)
	a.filterTo(t, name)
	a.Enter()
	a.WaitFor("CONTAINERS", Settle)
	baseline := settledSnapshot(a)
	fence := a.Proxy().Fence()
	a.Press("l")
	stream := a.Proxy().WaitForRequest(fence, RequestMatcher{Resource: "pods", Verb: "STREAM"}, Settle)

	firstMarker := name + "-batch-a-final"
	firstElapsed := runBurstWithFences(t, a, "first high-rate log batch", func(ctx context.Context) (int, error) {
		return emitPodLogBatch(ctx, restCfg, client, name, "a", linesPerBatch, firstMarker)
	})
	a.WaitForAll(Settle, firstMarker, "older log lines dropped")
	plateauA := settledSnapshot(a)

	secondMarker := name + "-batch-b-final"
	secondElapsed := runBurstWithFences(t, a, "second high-rate log batch", func(ctx context.Context) (int, error) {
		return emitPodLogBatch(ctx, restCfg, client, name, "b", linesPerBatch, secondMarker)
	})
	a.WaitForAll(Settle, secondMarker, "older log lines dropped")
	plateauB := settledSnapshot(a)

	// Once the 5,000-entry buffer is full, another >buffer-sized batch may
	// allocate transient parsing/rendering data but must not retain it.
	if plateauB.HeapAlloc > plateauA.HeapAlloc+(32<<20) {
		t.Errorf("heap did not plateau after the log buffer filled: %.1f MiB -> %.1f MiB", mib(plateauA.HeapAlloc), mib(plateauB.HeapAlloc))
	}
	firstAlloc := plateauA.TotalAlloc - baseline.TotalAlloc
	secondAlloc := plateauB.TotalAlloc - plateauA.TotalAlloc
	// TotalAlloc includes every complete Bubble Tea frame and PTY diff built
	// for these 12,000 individual stream turns, not only LogBuffer storage.
	// A healthy saturated run is currently about 2.2 GiB per batch; the old
	// append-to-nil buffer path adds several more GiB of 5,000-entry copies.
	// Keep an absolute regression ceiling while the ratio below is the sharper
	// test that saturation itself does not make the second batch degrade.
	const allocationBudget = 4 << 30
	if firstAlloc > allocationBudget || secondAlloc > allocationBudget {
		t.Errorf("high-rate log batch allocations exceeded 4 GiB: first %.1f MiB, second %.1f MiB", mib(firstAlloc), mib(secondAlloc))
	}
	if secondAlloc > firstAlloc*2+(64<<20) {
		t.Errorf("allocations degraded after saturation: first %.1f MiB, second %.1f MiB", mib(firstAlloc), mib(secondAlloc))
	}
	if secondElapsed > firstElapsed*3+5*time.Second {
		t.Errorf("second saturated batch took %s after first took %s", secondElapsed, firstElapsed)
	}
	for range 5 {
		if latency := a.InputFence(); latency > soakInputBudget {
			t.Fatalf("post-saturation input fence took %s, budget %s", latency, soakInputBudget)
		}
		if !strings.Contains(a.Frame(), secondMarker) {
			t.Fatalf("latest log marker did not remain stable:\n%s", a.Frame())
		}
	}

	a.Esc()
	if completed := a.Proxy().WaitForCompletion(stream.ID, 5*time.Second); !completed.Cancelled {
		t.Errorf("leaving saturated logs did not cancel follow request: %+v", completed)
	}
	a.WaitFor("CONTAINERS", Settle)
	after := settledSnapshot(a)
	assertRuntimeBudget(t, baseline, after, 32<<20, 20)
	if after.Classes["forwards"] > baseline.Classes["forwards"] || after.Classes["streams"] > baseline.Classes["streams"] {
		t.Errorf("background session stacks grew after leaving logs: before=%v after=%v", baseline.Classes, after.Classes)
	}
}

func createLogSoakPod(t *testing.T, client kubernetes.Interface, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), Settle)
	defer cancel()
	_, err := client.CoreV1().Pods(Namespace).Create(ctx, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: Namespace, Labels: map[string]string{"kute.dev/soak": "high-rate-logs"}},
		Spec: corev1.PodSpec{RestartPolicy: corev1.RestartPolicyNever, Containers: []corev1.Container{{
			Name: "logger", Image: "busybox:1.37@sha256:9532d8c39891ca2ecde4d30d7710e01fb739c87a8b9299685c63704296b16028",
			// One history line is required before podlogs transitions from its
			// bounded history request to follow=true. The batches themselves are
			// still emitted only after the proxy observes that follow request.
			Command: []string{"/bin/sh", "-c", "echo log-soak-ready; while true; do sleep 3600; done"},
		}}},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("creating high-rate log Pod: %v", err)
	}
	t.Cleanup(func() {
		//nolint:usetesting // cleanup must survive testing.T's context cancellation
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		zero := int64(0)
		_ = client.CoreV1().Pods(Namespace).Delete(cleanupCtx, name, metav1.DeleteOptions{GracePeriodSeconds: &zero})
	})
}

// emitPodLogBatch execs a producer in the fixture pod and returns the number
// of log lines it asked for — the burst's "writes" for runBurstWithFences,
// which refuses a burst that produced none.
func emitPodLogBatch(ctx context.Context, cfg *rest.Config, client kubernetes.Interface, pod, batch string, lines int, finalMarker string) (int, error) {
	script := fmt.Sprintf(`i=1; while [ "$i" -le %d ]; do printf 'soak-%s-%%05d payload payload payload\n' "$i" > /proc/1/fd/1; i=$((i+1)); done; printf '%%s\n' "$1" > /proc/1/fd/1`, lines, batch)
	req := client.CoreV1().RESTClient().Post().Resource("pods").Namespace(Namespace).Name(pod).SubResource("exec").VersionedParams(&corev1.PodExecOptions{
		Container: "logger", Command: []string{"/bin/sh", "-c", script, "soak", finalMarker}, Stdout: true, Stderr: true,
	}, scheme.ParameterCodec)
	executor, err := remotecommand.NewSPDYExecutor(cfg, "POST", req.URL())
	if err != nil {
		return 0, err
	}
	var stdout, stderr bytes.Buffer
	if err := executor.StreamWithContext(ctx, remotecommand.StreamOptions{Stdout: &stdout, Stderr: &stderr}); err != nil {
		return 0, fmt.Errorf("exec log producer: %w (stderr %q)", err, stderr.String())
	}
	// The producer wrote `lines` payload lines plus the final marker; the
	// exec returning cleanly is what makes that count real rather than
	// intended.
	return lines + 1, nil
}
