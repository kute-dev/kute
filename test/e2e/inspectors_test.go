//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"
)

// TestWhoCanResolvesBindingsFromCache covers §22a, which had no e2e path at
// all — the RBAC suite exercises what a restricted identity can and cannot
// read, but never opened the screen that explains *why*.
//
// The claim is specific and cache-shaped: whocan resolves
// (Cluster)RoleBindings → (Cluster)Roles entirely from informer caches, with
// no SubjectAccessReview round-trip per query. Four RBAC informers back it,
// all lazy, so the first open of the screen is what fills them and the
// answer arrives on their change events — a path with no unit-level
// equivalent, since a fake lister is already full the moment it is built.
//
// The subjects come from fixtures/60-rbac.yaml, which grants pods/list in
// kute-e2e to kute-restricted (namespaced RoleBinding) and cluster-wide to
// kute-partial (ClusterRoleBinding). Both have to be found, and the SCOPE
// column has to tell them apart — a resolver that read only one of the two
// binding kinds still answers, just wrongly, which is exactly the failure a
// single-subject assertion would miss.
func TestWhoCanResolvesBindingsFromCache(t *testing.T) {
	RequireCluster(t)
	a := Launch(t)
	a.WaitFor("api-", Connect)

	// The synthetic goto target: "who-can" labels the palette row, "Who Can"
	// is the screen — the two differ, so this drives gotoPalette directly
	// rather than gotoKind.
	a.gotoPalette(t, "who", "who-can", "Who Can")
	a.WaitLoaded(Settle)

	// The default query, stated on screen rather than assumed: list + the
	// kind browse was on.
	a.WaitForAll(Settle, "who can", "list", "pods")

	// The grid, and both binding scopes in it. kute-restricted is bound by a
	// namespaced RoleBinding, kute-partial by a ClusterRoleBinding.
	a.WaitForAll(Settle,
		"SUBJECT", "KIND", "VIA", "SCOPE",
		"kute-restricted",
		"kute-partial",
	)

	// Each subject on its own row, with the binding that actually granted it
	// named in VIA and the matching SCOPE — the per-row claim, which "both
	// names are somewhere on screen" does not make. A resolver that found the
	// bindings but attributed them to the wrong subject passes the check
	// above and fails here.
	//
	// This pair is also where the two binding kinds are told apart, which is
	// the whole reason both fixtures are asserted: kute-restricted is reached
	// through a namespaced RoleBinding, kute-partial through a
	// ClusterRoleBinding. Note kute-restricted is matched on the *scope* word
	// and kute-partial on its clusterrole, deliberately — "cluster" is a
	// substring of the "clusterrole/…" already in kute-partial's own VIA
	// cell, so asserting that would prove nothing.
	waitForRowPair(t, a, "kute-restricted", "rolebinding/kute-restricted")
	waitForRowPair(t, a, "kute-restricted", "namespace")
	waitForRowPair(t, a, "kute-partial", "clusterrole/kute-partial")

	// No round-trip per query: §22a's whole design is that the answer is
	// already in cache. A SubjectAccessReview lives under the
	// authorization.k8s.io group, so the group path is the thing to look for.
	//
	// Anchored on the *group* path, never on a bare substring: the four
	// informers backing this screen watch rbac.authorization.k8s.io, whose
	// name contains "authorization.k8s.io" — so a substring match flags the
	// exact cache-fill this test exists to confirm, and does it on every
	// healthy run.
	for _, rec := range a.Proxy().History() {
		if strings.HasPrefix(rec.URL.Path, "/apis/authorization.k8s.io/") {
			t.Fatalf("who-can issued an authorization round-trip, and must resolve from cache: %s %s", rec.Method, rec.URL.String())
		}
	}

	a.Esc()
	a.WaitFor("Pods", Settle)
}

// TestNodeDebugPanelStagesWithoutLaunching covers §41d, the one debugpanel
// target reachable without a shell-less or crash-looping pod fixture: `x` on
// a Nodes row.
//
// It deliberately stops short of ↵. The panel's commit is a
// tea.ExecProcess — it would suspend the program and hand the terminal to a
// real `kubectl debug`, which this harness has no terminal for and which
// would leave a privileged pod on the node. What is worth testing is
// everything before that: that the key opens the panel for the selected
// node, that the fields are the node set rather than a pod's, and that the
// "will run" line documents the exact command — the whole point of the
// screen being a staging surface rather than a bare keystroke.
func TestNodeDebugPanelStagesWithoutLaunching(t *testing.T) {
	RequireCluster(t)
	a := Launch(t)
	a.WaitFor("api-", Connect)

	a.gotoKind(t, "nodes", "Nodes")
	a.WaitLoaded(Settle)
	node := NodeNamePrefix(t) + "-control-plane"
	a.selectRow(t, node)

	a.Press("x")

	// The panel, named for the node it was opened on. "node/" is the
	// discriminant: the pod targets render a bare pod name here, so this
	// also proves the node branch was taken rather than the pod one.
	a.WaitForAll(Settle, "debug", "node/"+node)

	// §41d's own header note and field set. host-namespaces/privileged is
	// what makes a node debug pod different from every other target, and
	// rootfs /host is the field a pod target does not have at all — so
	// together they pin the right branch of fieldLines.
	a.WaitForAll(Settle,
		"host namespaces · privileged",
		"rootfs",
		"/host",
		"profile",
	)

	// The staging contract: the command is shown before it is run.
	a.WaitFor("will run", Settle)

	// And nothing ran. The panel has to stay on screen and stay interactive:
	// a commit is a tea.ExecProcess handoff, which suspends the program and
	// stops it producing frames at all, so a panel that launched on open
	// would fail the fence inside this window rather than merely look
	// different. Over a window, not at an instant — the launch would be
	// dispatched as a command, landing a turn or more after the key.
	if latency := a.InputFence(); latency > 2*time.Second {
		t.Fatalf("the debug panel stopped servicing input after %s — it appears to have handed off the terminal without a confirm", latency)
	}

	// esc walks back exactly one level, to the list it was opened from.
	a.Esc()
	a.WaitFor("Nodes", Settle)
}

// waitForRowPair waits for one rendered row carrying both fields — the row
// shape every "this value belongs to that subject" assertion needs, and the
// thing a pair of WaitFor calls cannot say.
func waitForRowPair(t *testing.T, a *App, first, second string) {
	t.Helper()
	frame, ok := a.poll(func(f string) bool {
		for _, line := range strings.Split(f, "\n") {
			if strings.Contains(line, first) && strings.Contains(line, second) {
				return true
			}
		}
		return false
	}, Settle)
	if !ok {
		t.Fatalf("no single row carried both %q and %q; frame:\n%s", first, second, frame)
	}
}
