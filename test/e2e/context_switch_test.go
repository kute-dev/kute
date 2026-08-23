//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"
)

func TestContextSwitchCancelsOldStreamAndReturnsPushedTaskToBrowse(t *testing.T) {
	first := NewAPIProxy(t, KubeconfigPath())
	second := NewAPIProxy(t, KubeconfigPath())
	merged := BuildMergedKubeconfig(t,
		KubeconfigContext{Name: "ctx-one", Kubeconfig: first.KubeconfigPath()},
		KubeconfigContext{Name: "ctx-two", Kubeconfig: second.KubeconfigPath()},
	)
	a := Launch(t, WithKubeconfig(merged), WithContext("ctx-one"), WithoutAPIProxy())
	a.WaitFor("api-", Connect)
	a.filterTo(t, "api-")
	pod := firstMatch(a.Frame(), "api-")
	a.Enter()
	a.WaitFor("CONTAINERS", Settle)

	gate := first.Hold(RequestMatcher{Resource: "pods", Verb: "STREAM"})
	fence := first.Fence()
	a.Press("l")
	stream := first.WaitForRequest(fence, RequestMatcher{Resource: "pods", Verb: "STREAM"}, Settle)
	if stream.Completed {
		t.Fatalf("delayed old-context log stream completed too early: %+v", stream)
	}

	switchContextThroughPalette(t, a, "ctx-two")
	a.WaitFor("ctx-two", Settle)
	a.WaitGone(pod, Settle)
	completed := first.WaitForCompletion(stream.ID, Settle)
	if !completed.Cancelled {
		t.Fatalf("old-context follow request was not cancelled: %+v", completed)
	}
	gate.Release()

	// The destination contract is browse, never an attempted reconstruction
	// of detail/YAML/log/editor state from the old object identity.
	a.WaitFor("Pods", Settle)
	a.Never("KUTE-E2E-LOG-MARKER", time.Second)

	oldFence := first.Fence()
	a.Never("old-context-error-marker", 2500*time.Millisecond)
	for _, rec := range first.History() {
		if rec.ID > oldFence && (rec.Verb == "LIST" || rec.Verb == "WATCH") {
			t.Fatalf("old endpoint received new %s after switch: %+v", rec.Verb, rec)
		}
	}

	// Switching back restores the first context's remembered namespace, kind
	// and filter; the api rows are the observable filter result.
	switchContextThroughPalette(t, a, "ctx-one")
	a.WaitForAll(Settle, "ctx-one", Namespace, "api-")
	if frame := a.Frame(); strings.Contains(frame, "worker-") {
		t.Fatalf("remembered api filter was not restored on ctx-one:\n%s", frame)
	}

	// Pin the same return-to-browse contract from the other pushed task
	// shapes. The delayed follow stream above proves cancellation at the wire;
	// these prevent detail/YAML/in-app editor state from being rebuilt with an
	// old-context object after the switch.
	a.Enter()
	a.WaitFor("CONTAINERS", Settle)
	switchContextThroughPalette(t, a, "ctx-two")
	a.WaitFor("ctx-two", Settle)
	switchContextThroughPalette(t, a, "ctx-one")
	a.WaitFor("api-", Settle)

	a.Press("y")
	a.WaitForAll(Settle, "YAML", pod)
	switchContextThroughPalette(t, a, "ctx-two")
	a.WaitFor("ctx-two", Settle)
	switchContextThroughPalette(t, a, "ctx-one")
	a.WaitFor("api-", Settle)

	a.openFrom(t, "configmaps", "ConfigMaps", "app-config")
	a.WaitFor("nginx.conf", Settle)
	a.selectRow(t, "nginx.conf")
	a.Press("e")
	a.WaitFor("BUFFER EDITOR", Settle)
	// Free-text buffers deliberately own plain 'c'; discard the editor, then
	// switch from its still-pushed ConfigMap Data parent task.
	a.Esc()
	a.WaitGone("BUFFER EDITOR", Settle)
	switchContextThroughPalette(t, a, "ctx-two")
	a.WaitForAll(Settle, "ctx-two", "Pods")
	a.Never("BUFFER EDITOR", time.Second)
}

func TestFailedContextSwitchKeepsOriginalSession(t *testing.T) {
	first := NewAPIProxy(t, KubeconfigPath())
	second := NewAPIProxy(t, KubeconfigPath())
	merged := BuildMergedKubeconfig(t,
		KubeconfigContext{Name: "ctx-good", Kubeconfig: first.KubeconfigPath()},
		KubeconfigContext{Name: "ctx-down", Kubeconfig: second.KubeconfigPath()},
	)
	a := Launch(t, WithKubeconfig(merged), WithContext("ctx-good"), WithoutAPIProxy())
	a.WaitForAll(Connect, "ctx-good", "api-")
	second.SetAvailable(false)

	a.Press("c")
	a.Type("ctx-down")
	a.WaitFor("ctx-down", Settle)
	rollbackFence := first.Fence()
	a.Enter()
	// SwitchContext is bounded at 15s and then rebuilds the original context.
	first.WaitForRequest(rollbackFence, RequestMatcher{Resource: "pods", Verb: "LIST"}, 2*Settle)
	a.WaitForAll(2*Settle, "ctx-good", Namespace, "api-")
	a.Never("ctx-down ›", time.Second)
}

func switchContextThroughPalette(t *testing.T, a *App, name string) {
	t.Helper()
	a.Press("c")
	a.Type(name)
	a.WaitFor(name, Settle)
	a.Enter()
}
