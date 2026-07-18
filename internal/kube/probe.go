package kube

import (
	"context"
	"sync"
	"time"
)

// ProbeResult is one kubeconfig context's reachability check.
type ProbeResult struct {
	Name    string
	Latency time.Duration
	Err     error
}

const probeTimeout = 3 * time.Second

// ProbeContexts probes every named kubeconfig context concurrently — for
// each, build a rest.Config (no caching; this is a one-shot check, not a
// long-lived client) and hit /livez with a 3s timeout — and streams results
// as they complete. Used by the context palette (7a) and the
// unreachable-at-launch screen (4c) to show reachability + latency in the
// background while the user browses. The channel closes once every context
// has reported.
func ProbeContexts(ctx context.Context, names []string) <-chan ProbeResult {
	return probeContextsWith(ctx, names, defaultProbe)
}

// probeFunc pings one named context. Factored out so probe fan-out
// (concurrency, result delivery, channel close) can be unit-tested with a
// fake instead of a live cluster (mvp-plan.md Phase 0 verification).
type probeFunc func(ctx context.Context, name string) (time.Duration, error)

func probeContextsWith(ctx context.Context, names []string, probe probeFunc) <-chan ProbeResult {
	out := make(chan ProbeResult, len(names))
	var wg sync.WaitGroup
	for _, name := range names {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			latency, err := probe(ctx, name)
			out <- ProbeResult{Name: name, Latency: latency, Err: err}
		}(name)
	}
	go func() {
		wg.Wait()
		close(out)
	}()
	return out
}

func defaultProbe(ctx context.Context, name string) (time.Duration, error) {
	client, err := NewClientForContext(name)
	if err != nil {
		return 0, err
	}
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	start := time.Now()
	err = client.Interface.Discovery().RESTClient().Get().AbsPath("/livez").Do(probeCtx).Error()
	return time.Since(start), err
}
