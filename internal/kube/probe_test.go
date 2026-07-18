package kube

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestProbeContextsFansOutAllNames(t *testing.T) {
	t.Parallel()
	names := []string{"dev", "staging", "prod"}
	probe := func(_ context.Context, name string) (time.Duration, error) {
		return 5 * time.Millisecond, nil
	}

	seen := map[string]ProbeResult{}
	for r := range probeContextsWith(context.Background(), names, probe) {
		seen[r.Name] = r
	}
	if len(seen) != len(names) {
		t.Fatalf("got %d results, want %d", len(seen), len(names))
	}
	for _, n := range names {
		if _, ok := seen[n]; !ok {
			t.Fatalf("missing result for %q", n)
		}
	}
}

func TestProbeContextsRunsConcurrently(t *testing.T) {
	t.Parallel()
	names := []string{"a", "b", "c", "d"}
	const perProbe = 40 * time.Millisecond
	probe := func(_ context.Context, name string) (time.Duration, error) {
		time.Sleep(perProbe)
		return perProbe, nil
	}

	start := time.Now()
	for range probeContextsWith(context.Background(), names, probe) {
	}
	elapsed := time.Since(start)

	// Serial execution would take len(names)*perProbe (~160ms); concurrent
	// execution should finish well under that.
	if elapsed >= time.Duration(len(names))*perProbe {
		t.Fatalf("elapsed = %v, expected concurrent fan-out to finish faster than serial %v", elapsed, time.Duration(len(names))*perProbe)
	}
}

func TestProbeContextsPropagatesPerContextError(t *testing.T) {
	t.Parallel()
	names := []string{"reachable", "unreachable"}
	wantErr := errors.New("dial tcp: timeout")
	probe := func(_ context.Context, name string) (time.Duration, error) {
		if name == "unreachable" {
			return 0, wantErr
		}
		return time.Millisecond, nil
	}

	results := map[string]ProbeResult{}
	for r := range probeContextsWith(context.Background(), names, probe) {
		results[r.Name] = r
	}
	if results["reachable"].Err != nil {
		t.Fatalf("reachable context should have no error, got %v", results["reachable"].Err)
	}
	if !errors.Is(results["unreachable"].Err, wantErr) {
		t.Fatalf("unreachable context error = %v, want %v", results["unreachable"].Err, wantErr)
	}
}

func TestProbeContextsEmptyNamesClosesImmediately(t *testing.T) {
	t.Parallel()
	ch := probeContextsWith(context.Background(), nil, defaultProbe)
	count := 0
	for range ch {
		count++
	}
	if count != 0 {
		t.Fatalf("expected no results for empty names, got %d", count)
	}
}
