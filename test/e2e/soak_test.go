//go:build e2e && e2e_soak

package e2e

import (
	"net"
	"strings"
	"testing"
	"time"
)

type soakKind struct{ query, title string }

var soakKinds = []soakKind{
	{"pods", "Pods"},
	{"deployments", "Deployments"},
	{"services", "Services"},
	{"configmaps", "ConfigMaps"},
	{"secrets", "Secrets"},
	{"jobs", "Jobs"},
	{"cronjobs", "CronJobs"},
	{"ingresses", "Ingresses"},
	{"nodes", "Nodes"},
	{"widgets", "Widgets"},
}

func TestRepeatedWorkflowSoakReleasesTransientSessions(t *testing.T) {
	iterations := soakCount(t, "KUTE_E2E_SOAK_ITERATIONS", 8)
	first := NewAPIProxy(t, KubeconfigPath())
	second := NewAPIProxy(t, KubeconfigPath())
	merged := BuildMergedKubeconfig(t,
		KubeconfigContext{Name: "soak-one", Kubeconfig: first.KubeconfigPath()},
		KubeconfigContext{Name: "soak-two", Kubeconfig: second.KubeconfigPath()},
	)
	a := Launch(t, WithKubeconfig(merged), WithContext("soak-one"), WithScopeNamespace(Namespace), WithoutAPIProxy())
	a.WaitFor("api-", Connect)

	// One complete warm-up makes the baseline include every intentional
	// retained cache and palette probe used by an iteration.
	current := runWorkflowIteration(t, a, first, second, "soak-one")
	if current != "soak-two" {
		t.Fatalf("warm-up ended on %s", current)
	}
	baseline := settledSnapshot(a)
	requireOnlyWatchesActive(t, second.Counts())

	start := time.Now()
	for i := 0; i < iterations; i++ {
		current = runWorkflowIteration(t, a, first, second, current)
		if latency := a.InputFence(); latency > soakInputBudget {
			t.Fatalf("iteration %d input fence took %s, budget %s", i, latency, soakInputBudget)
		}
		t.Logf("completed workflow iteration %d/%d on %s", i+1, iterations, current)
	}
	t.Logf("%d measured workflow iterations completed in %s", iterations, time.Since(start))

	got := settledSnapshot(a)
	assertRuntimeBudget(t, baseline, got, 96<<20, 50)
	if got.Classes["streams"] > baseline.Classes["streams"] || got.Classes["forwards"] > baseline.Classes["forwards"] {
		t.Errorf("transient session goroutines grew: before=%v after=%v", baseline.Classes, got.Classes)
	}

	// Derive the active/old endpoints from the actual final context rather
	// than relying on the default iteration count's parity.
	currentProxy, oldProxy := first, second
	if current == "soak-two" {
		currentProxy, oldProxy = second, first
	}
	if active := oldProxy.Counts().Active; active != 0 {
		t.Errorf("old context retains %d active requests", active)
	}
	requireOnlyWatchesActive(t, currentProxy.Counts())
}

func runWorkflowIteration(t *testing.T, a *App, first, second *APIProxy, current string) string {
	t.Helper()
	a.gotoKind(t, "pods", "Pods")
	a.WaitFor("api-", Settle)
	pod := firstMatch(a.Frame(), "api-")
	if pod == "" {
		t.Fatalf("no api pod for workflow iteration:\n%s", a.Frame())
	}
	a.selectRow(t, pod)
	a.Enter()
	a.WaitFor("CONTAINERS", Settle)

	a.Press("l")
	a.WaitFor("KUTE-E2E-LOG-MARKER", Settle)
	a.backToPodDetail()
	a.Press("e")
	a.WaitLoaded(Settle)
	a.WaitFor("Events", Settle)
	a.backToPodDetail()
	a.Press("t")
	a.WaitLoaded(Settle)
	a.WaitFor("Timeline", Settle)
	a.backToPodDetail()
	a.Press("y")
	a.WaitForAll(Settle, "YAML", pod)
	a.Esc()
	a.WaitFor("CONTAINERS", Settle)
	a.Esc()
	a.WaitFor("Pods", Settle)

	for _, kind := range soakKinds {
		a.gotoKind(t, kind.query, kind.title)
		a.WaitLoaded(Settle)
	}

	// Exercise all three scopes of the one palette shell.
	for _, key := range []string{"g", "n", "c"} {
		a.Press(key)
		a.WaitFor("esc", Settle)
		a.Esc()
	}

	// A complete forward lifecycle in every iteration, including a forced
	// reconnect through r before stop.
	a.gotoKind(t, "pods", "Pods")
	a.WaitFor("api-", Settle)
	pod = firstMatch(a.Frame(), "api-")
	a.selectRow(t, pod)
	a.Press("f")
	frame := a.WaitForAll(Settle, "forward", pod, "8080", "kubectl port-forward")
	match := localPortRe.FindStringSubmatch(frame)
	if match == nil {
		t.Fatalf("no local port in soak forward picker:\n%s", frame)
	}
	local := match[1]
	a.Enter()
	a.WaitFor("⇄", Settle)
	if body := fetchThrough(t, local); !strings.Contains(body, "KUTE-E2E-FORWARD-OK") {
		t.Fatalf("forward returned %q", body)
	}
	a.gotoKind(t, "forwards", "Forwards")
	a.WaitFor("localhost:"+local, Settle)
	a.Press("r")
	_ = fetchThrough(t, local)
	a.Press("x")
	a.WaitGone("localhost:"+local, Settle)
	WaitForTCPRefused(t, net.JoinHostPort("127.0.0.1", local), 5*time.Second)
	a.gotoKind(t, "pods", "Pods")
	a.WaitFor("api-", Settle)

	next := "soak-two"
	old := first
	if current == "soak-two" {
		next, old = "soak-one", second
	}
	switchContextThroughPalette(t, a, next)
	a.WaitForAll(Settle, next, "Pods")
	deadline := time.Now().Add(Settle)
	for old.Counts().Active != 0 && time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
	}
	if active := old.Counts().Active; active != 0 {
		t.Fatalf("context %s retains %d active requests after switch", current, active)
	}

	// The two real namespace caches are revisited after every rebuild. This
	// covers both per-namespace creation and reuse within one context.
	switchNamespaceThroughPalette(t, a, "kute-e2e-b")
	a.WaitFor("namespace-b-pod", Settle)
	switchNamespaceThroughPalette(t, a, Namespace)
	a.WaitFor("api-", Settle)
	return next
}
