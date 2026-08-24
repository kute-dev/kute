//go:build e2e && e2e_soak

package e2e

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	soakInputBudget = 750 * time.Millisecond
	soakSettle      = 2 * time.Second
)

// soakCount makes every stress dimension bounded and prints the exact value
// in the test log. Environment overrides are useful for a short local
// reproduction without changing the nightly contract.
func soakCount(t *testing.T, env string, fallback int) int {
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

// runBurstWithFences runs bounded cluster churn while continuously measuring
// inert turns of kute's event loop. The worker returns errors instead of
// touching testing.T from its goroutine.
func runBurstWithFences(t *testing.T, a *App, name string, worker func(context.Context) error) time.Duration {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*Settle)
	defer cancel()
	done := make(chan error, 1)
	start := time.Now()
	go func() { done <- worker(ctx) }()

	maxLatency := time.Duration(0)
	fences := 0
	for {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("%s burst: %v", name, err)
			}
			if fences == 0 {
				latency := a.InputFence()
				maxLatency = latency
				fences = 1
			}
			if maxLatency > soakInputBudget {
				t.Fatalf("%s input fence reached %s, budget %s", name, maxLatency, soakInputBudget)
			}
			elapsed := time.Since(start)
			t.Logf("%s: %d input fences, max %s, burst %s", name, fences, maxLatency, elapsed)
			return elapsed
		default:
		}

		latency := a.InputFence()
		fences++
		if latency > maxLatency {
			maxLatency = latency
		}
		if latency > soakInputBudget {
			cancel()
			t.Fatalf("%s input fence took %s, budget %s", name, latency, soakInputBudget)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func settledSnapshot(a *App) RuntimeSnapshot {
	a.t.Helper()
	// Drain already-queued updates, then allow the one-second screen ticks and
	// short retry/debounce timers to fire before forcing GC.
	_ = a.InputFence()
	time.Sleep(soakSettle)
	_ = a.InputFence()
	return a.Snapshot()
}

func assertRuntimeBudget(t *testing.T, baseline, got RuntimeSnapshot, heapDelta uint64, goroutineDelta int) {
	t.Helper()
	if got.HeapAlloc > baseline.HeapAlloc+heapDelta {
		t.Errorf("heap grew from %.1f MiB to %.1f MiB, budget +%.1f MiB",
			mib(baseline.HeapAlloc), mib(got.HeapAlloc), mib(heapDelta))
	}
	if got.Goroutines > baseline.Goroutines+goroutineDelta {
		t.Errorf("goroutines grew from %d to %d, budget +%d\nclasses before=%v after=%v",
			baseline.Goroutines, got.Goroutines, goroutineDelta, baseline.Classes, got.Classes)
	}
}

func assertRequestGrowthBounded(t *testing.T, before RequestCounts, history []RequestRecord, totalDelta int) {
	t.Helper()
	if before.Total > len(history) {
		t.Fatalf("request baseline %d exceeds proxy history %d", before.Total, len(history))
	}
	unexpected := 0
	lists := map[string]int{}
	for _, request := range history[before.Total:] {
		// Healthy clusters deliberately probe /livez every two seconds, and
		// Pod detail deliberately polls metrics on its sync interval. A real
		// API burst can take several minutes on kind; neither periodic read is
		// screen-reload amplification, which is what this assertion guards.
		if request.URL.Path == "/livez" || strings.HasPrefix(request.URL.Path, "/apis/metrics.k8s.io/") {
			continue
		}
		unexpected++
		if request.Verb == "LIST" {
			lists[request.Resource+"/LIST"]++
		}
	}
	if unexpected > totalDelta {
		t.Errorf("unexpected proxied API requests grew by %d, budget %d", unexpected, totalDelta)
	}
	for key, delta := range lists {
		if delta > 1 {
			t.Errorf("%s grew by %d during a cache-local storm; want at most one recovery relist", key, delta)
		}
	}
}

func requireOnlyWatchesActive(t *testing.T, counts RequestCounts) {
	t.Helper()
	for key, count := range counts.ActiveByResourceVerb {
		// /livez has no Kubernetes resource, so the proxy records its brief
		// health probe as /GET. It is part of the active context's steady-state
		// health loop, unlike an object GET, stream, exec, or forward.
		if count > 0 && key != "/GET" && (len(key) < 6 || key[len(key)-6:] != "/WATCH") {
			t.Errorf("active non-WATCH request after settling: %s=%d", key, count)
		}
	}
}

func mib(n uint64) float64 { return float64(n) / (1 << 20) }

func soakName(prefix string) string {
	return fmt.Sprintf("%s-%x", prefix, time.Now().UnixNano()&0xffffff)
}
