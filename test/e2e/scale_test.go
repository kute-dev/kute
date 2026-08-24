//go:build e2e && e2e_scale

// The scale row of the suite, behind its own tag on top of e2e so it never
// runs in the PR job: it needs the kwok cluster scripts/e2e-scale-cluster.sh
// builds, and it takes minutes rather than seconds.
//
//	scripts/e2e-scale-cluster.sh up
//	go test -tags "e2e e2e_scale" -count=1 -timeout 30m ./test/e2e/...
//
// What it measures only exists at size. Connect fills three informer caches;
// against 5,000 pods that is the moment the "the app pays for the screen
// you're on" rule either holds or doesn't, and the number that shows whether
// it held is how much heap those three caches occupy — not how the frame
// looks.
package e2e

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	// connectBudget is what "kute opens" has to mean on a 5k-pod cluster.
	// Generous on purpose: this is a regression guard against connect
	// growing an extra informer's worth of LIST, not a benchmark.
	connectBudget = 30 * time.Second

	// heapBudget is what the three eager caches may cost. Pods dominate:
	// managedFields are stripped on the way in (transform.go), which is most
	// of why this number is a few tens of MB rather than hundreds.
	heapBudget = 400 << 20 // 400 MiB

	postNavigationHeapBudget      = 64 << 20
	postNavigationGoroutineBudget = 32
	scaleInputBudget              = 750 * time.Millisecond
	scaleSettle                   = 6 * time.Second
)

// scaleKubeconfigPath is where scripts/e2e-scale-cluster.sh writes.
func scaleKubeconfigPath() string {
	if p := os.Getenv("KUTE_E2E_SCALE_KUBECONFIG"); p != "" {
		return p
	}
	return filepath.Join(repoRoot(), ".kube", "e2e-scale.config")
}

// requireScaleCluster skips the test unless the kwok scale kubeconfig exists.
// The nightly scale job deliberately provisions no ordinary e2e cluster.
func requireScaleCluster(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(scaleKubeconfigPath()); err != nil {
		t.Skipf("no scale cluster: %v\nrun: scripts/e2e-scale-cluster.sh up", err)
	}
}

// TestScaleConnectAndFirstFrame: on a cluster with 5,000 pods, kute reaches a
// rendered list inside the connect budget, and the frame it reaches is a real
// one — the pod count in the header, not a spinner that has given up.
func TestScaleConnectAndFirstFrame(t *testing.T) {
	requireScaleCluster(t)
	path := scaleKubeconfigPath()

	start := time.Now()
	a := Launch(t, WithKubeconfig(path), WithNamespace("scale-00"))

	// A row from the fixture workload, which only appears once the Pod cache
	// has actually filled.
	a.WaitFor("web-", connectBudget)
	elapsed := time.Since(start)
	t.Logf("connect to first populated frame: %s", elapsed)
	if elapsed > connectBudget {
		t.Errorf("connect took %s, budget %s", elapsed, connectBudget)
	}

	// The list is real, not a truncated placeholder: the header carries the
	// count and the table has paged rather than tried to render 5,000 rows.
	a.WaitLoaded(Settle)
	a.WaitFor("pods", Settle)

	// Heap after the eager caches have filled. Read after a GC so this is
	// live data rather than whatever the allocator has not yet swept.
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	t.Logf("heap in use after connect: %.1f MiB", float64(stats.HeapAlloc)/(1<<20))
	if stats.HeapAlloc > heapBudget {
		t.Errorf("heap after connect is %.1f MiB, budget %d MiB", float64(stats.HeapAlloc)/(1<<20), heapBudget>>20)
	}
}

// TestScaleNavigationStaysResponsive: the rule this exists for is that
// browsing a big cluster must not start an informer per kind. Walking the
// goto palette across a dozen kinds on a 5k-pod cluster is exactly the
// sequence that once hung the app for a minute.
func TestScaleNavigationStaysResponsive(t *testing.T) {
	requireScaleCluster(t)
	path := scaleKubeconfigPath()
	navigationIterations := scaleCount(t, "KUTE_E2E_SCALE_NAV_ITERATIONS", 8)
	burstPods := scaleCount(t, "KUTE_E2E_SCALE_BURST_PODS", 500)

	a := Launch(t, WithKubeconfig(path), WithNamespace("scale-00"))
	a.WaitFor("web-", connectBudget)

	// Opening the palette reads a count per kind, and those counts come from
	// CountLive rather than from informer caches. If that ever regressed,
	// this is where it would show: as a palette that takes a minute to open.
	start := time.Now()
	a.Press("g")
	a.WaitFor("jump anywhere", Settle)
	t.Logf("goto palette open: %s", time.Since(start))
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("the goto palette took %s to open — it is counting kinds through their informers", elapsed)
	}
	a.Esc()

	// And a screen that is not the resting one still lands promptly.
	start = time.Now()
	a.gotoKind(t, "deployments", "Deployments")
	a.WaitLoaded(Settle)
	t.Logf("switch to Deployments: %s", time.Since(start))

	// Warm the intended Pod/Deployment/palette paths before measuring. The
	// six-second settle exceeds the goto count fan-out's five-second timeout,
	// so no abandoned CountLive command is mistaken for navigation growth.
	a.gotoKind(t, "pods", "Pods")
	a.WaitFor("web-", Settle)
	baselineRuntime := scaleSettledSnapshot(a)
	baselineRequests := a.Proxy().Counts()

	for range navigationIterations {
		a.gotoKind(t, "deployments", "Deployments")
		a.WaitLoaded(Settle)
		a.gotoKind(t, "pods", "Pods")
		a.WaitFor("web-", Settle)
	}
	afterNavigation := scaleSettledSnapshot(a)
	assertScaleRuntimeBudget(t, baselineRuntime, afterNavigation)
	assertNoNewWatchResources(t, baselineRequests, a.Proxy().Counts())

	// Keep the large Pod list open and filtered while a second scale-sized
	// object burst lands. The final object fuzzy-ranks first but is created
	// last, so seeing it proves the trailing watch state won rather than merely
	// that an early event made the screen responsive once.
	client := scaleClientset(t, path)
	run := fmt.Sprintf("sb-%x", time.Now().UnixNano()&0xffffff)
	prefix := run + "-"
	selector := "kute.dev/e2e-scale-run=" + run
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cleanupCancel()
		if err := client.CoreV1().Pods("scale-00").DeleteCollection(cleanupCtx, metav1.DeleteOptions{}, metav1.ListOptions{LabelSelector: selector}); err != nil {
			t.Errorf("cleaning up scale burst Pods: %v", err)
		}
	})
	createScaleBurstPod(t, t.Context(), client, "scale-00", prefix+"anchor", run)
	a.WaitFor(prefix+"anchor", Settle)
	a.filterTo(t, prefix)

	finalMarker := prefix + "000-final"
	requestFence := a.Proxy().Fence()
	runScaleBurstWithFences(t, a, func(ctx context.Context) error {
		for i := 0; i < burstPods; i++ {
			name := fmt.Sprintf("%sz%04d", prefix, i)
			if i == burstPods-1 {
				name = finalMarker
			}
			if err := createScaleBurstPodE(ctx, client, "scale-00", name, run); err != nil {
				return err
			}
		}
		return nil
	})
	// Fuzzy ranking deliberately does not promise where equally strong prefix
	// matches land, and selection stays on the anchor while rows arrive. Refine
	// the still-open filter to the exact trailing marker and require it as a
	// table row (not merely text in the filter strip).
	a.Press("/")
	for range len(prefix) {
		a.Press("backspace")
	}
	a.Type(finalMarker)
	frame, markerVisible := a.poll(func(frame string) bool {
		for _, line := range strings.Split(frame, "\n") {
			if selectedTableRow(line, finalMarker) {
				return true
			}
		}
		return false
	}, Settle)
	if !markerVisible {
		t.Fatalf("final scale marker never reached the filtered table:\n%s", frame)
	}
	a.Enter()
	for range 5 {
		if latency := a.InputFence(); latency > scaleInputBudget {
			t.Fatalf("post-burst input fence took %s, budget %s", latency, scaleInputBudget)
		}
		if !scaleFrameHasSelectedRow(a.Frame(), finalMarker) {
			t.Fatalf("scale burst final marker became unstable:\n%s", a.Frame())
		}
	}
	for _, request := range a.Proxy().History() {
		if request.ID <= requestFence || (request.Verb != "LIST" && request.Verb != "WATCH") {
			continue
		}
		if request.Resource != "pods" {
			t.Fatalf("scale burst started unrelated %s traffic for %q: %+v", request.Verb, request.Resource, request)
		}
	}
}

func scaleFrameHasSelectedRow(frame, name string) bool {
	for _, line := range strings.Split(frame, "\n") {
		if selectedTableRow(line, name) {
			return true
		}
	}
	return false
}

func scaleCount(t *testing.T, env string, fallback int) int {
	t.Helper()
	value := fallback
	if raw := os.Getenv(env); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			t.Fatalf("%s must be a positive integer, got %q", env, raw)
		}
		value = parsed
	}
	t.Logf("%s=%d", env, value)
	return value
}

func scaleSettledSnapshot(a *App) RuntimeSnapshot {
	a.t.Helper()
	_ = a.InputFence()
	time.Sleep(scaleSettle)
	_ = a.InputFence()
	return a.Snapshot()
}

func assertScaleRuntimeBudget(t *testing.T, baseline, got RuntimeSnapshot) {
	t.Helper()
	if got.HeapAlloc > baseline.HeapAlloc+postNavigationHeapBudget {
		t.Errorf("post-navigation heap grew from %.1f MiB to %.1f MiB, budget +%d MiB",
			float64(baseline.HeapAlloc)/(1<<20), float64(got.HeapAlloc)/(1<<20), postNavigationHeapBudget>>20)
	}
	if got.Goroutines > baseline.Goroutines+postNavigationGoroutineBudget {
		t.Errorf("post-navigation goroutines grew from %d to %d, budget +%d\nclasses before=%v after=%v",
			baseline.Goroutines, got.Goroutines, postNavigationGoroutineBudget, baseline.Classes, got.Classes)
	}
}

func assertNoNewWatchResources(t *testing.T, before, after RequestCounts) {
	t.Helper()
	for key, count := range after.ByResourceVerb {
		if !strings.HasSuffix(key, "/WATCH") || count == 0 || before.ByResourceVerb[key] > 0 {
			continue
		}
		t.Errorf("navigation started an unwarmed informer watch: %s=%d", key, count)
	}
}

func scaleClientset(t *testing.T, path string) kubernetes.Interface {
	t.Helper()
	cfg, err := clientcmd.BuildConfigFromFlags("", path)
	if err != nil {
		t.Fatalf("building scale REST config: %v", err)
	}
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("building scale clientset: %v", err)
	}
	return client
}

func createScaleBurstPod(t *testing.T, ctx context.Context, client kubernetes.Interface, namespace, name, run string) {
	t.Helper()
	if err := createScaleBurstPodE(ctx, client, namespace, name, run); err != nil {
		t.Fatalf("creating scale burst Pod %s: %v", name, err)
	}
}

func createScaleBurstPodE(ctx context.Context, client kubernetes.Interface, namespace, name, run string) error {
	_, err := client.CoreV1().Pods(namespace).Create(ctx, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: namespace,
			Labels: map[string]string{"kute.dev/e2e-scale-run": run},
		},
		Spec: corev1.PodSpec{
			SchedulerName: "kute-e2e-never-schedule",
			Containers:    []corev1.Container{{Name: "burst", Image: "registry.k8s.io/pause:3.10"}},
		},
	}, metav1.CreateOptions{})
	return err
}

func runScaleBurstWithFences(t *testing.T, a *App, worker func(context.Context) error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- worker(ctx) }()

	for {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("scale Pod burst: %v", err)
			}
			if latency := a.InputFence(); latency > scaleInputBudget {
				t.Fatalf("scale input fence took %s, budget %s", latency, scaleInputBudget)
			}
			return
		default:
		}
		if latency := a.InputFence(); latency > scaleInputBudget {
			cancel()
			t.Fatalf("scale input fence took %s, budget %s", latency, scaleInputBudget)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
