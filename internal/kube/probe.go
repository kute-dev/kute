package kube

import (
	"context"
	"time"

	"golang.org/x/sync/errgroup"
)

// ProbeResult is one kubeconfig context's reachability check.
type ProbeResult struct {
	Name    string
	Latency time.Duration
	Err     error
}

// probeTimeout bounds one context's reachability check. Generous rather than
// snappy: results stream in as they land, so a slow-but-reachable context
// (a bastion/port-forwarded cluster, or any context probed while the active
// cluster's informers are saturating the same link) costs a later verdict,
// whereas a tight deadline costs a *wrong* one — an "unreachable" label on a
// context that works fine.
const probeTimeout = 6 * time.Second

// probeConcurrency bounds how many contexts are probed at once. Each probe is
// a fresh client plus a TLS handshake, and a large corporate kubeconfig is the
// case this exists for.
const probeConcurrency = 8

// ProbeContexts probes every named kubeconfig context concurrently — for
// each, build a rest.Config (no caching; this is a one-shot check, not a
// long-lived client) and hit /livez with probeTimeout — and streams results
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
	// Bounded: every probe past the first builds its own rest.Config and does
	// its own TLS handshake, so an unbounded fan-out meant a 50-context
	// corporate kubeconfig opened 50 concurrent handshakes the instant the
	// context palette was opened. SetLimit blocks at g.Go, so the bound is on
	// goroutines, not just on requests in flight.
	g := new(errgroup.Group)
	g.SetLimit(probeConcurrency)
	for _, name := range names {
		g.Go(func() error {
			latency, err := probe(ctx, name)
			out <- ProbeResult{Name: name, Latency: latency, Err: err}
			return nil
		})
	}
	go func() {
		_ = g.Wait() // every probe reports through out, never through error
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
