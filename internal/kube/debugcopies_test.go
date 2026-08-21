package kube

import (
	"sync"
	"testing"
)

func TestDebugCopyRegistryAddContainsRemove(t *testing.T) {
	t.Parallel()
	r := NewDebugCopyRegistry()
	if r.Contains("default", "worker-debug") {
		t.Fatalf("empty registry must not contain anything")
	}
	r.Add("default", "worker-debug")
	if !r.Contains("default", "worker-debug") {
		t.Fatalf("expected Contains true after Add")
	}
	if r.Contains("default", "other-debug") {
		t.Fatalf("Contains must not match an unrelated name")
	}
	if r.Contains("prod", "worker-debug") {
		t.Fatalf("Contains must be namespace-scoped")
	}
	r.Remove("default", "worker-debug")
	if r.Contains("default", "worker-debug") {
		t.Fatalf("expected Contains false after Remove")
	}
}

func TestDebugCopyRegistryZeroValueUsable(t *testing.T) {
	t.Parallel()
	var r DebugCopyRegistry
	r.Add("default", "x")
	if !r.Contains("default", "x") {
		t.Fatalf("expected a zero-value DebugCopyRegistry to work after Add")
	}
}

func TestDebugCopyRegistryConcurrentAccess(t *testing.T) {
	t.Parallel()
	r := NewDebugCopyRegistry()
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r.Add("default", "copy")
			r.Contains("default", "copy")
			r.Remove("default", "copy")
		}(i)
	}
	wg.Wait()
}
