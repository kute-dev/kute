//go:build e2e

package e2e

import (
	"testing"
	"time"
)

// TestScopedLaunchLoadsRealPodRows is the sharpest regression test in this
// file: same kute-restricted kubeconfig, same Role, same cluster as
// TestForbiddenKindNeverRendersAsEmpty (rbac_test.go) — but launched with
// --namespace-scoped instead of a plain launch. That test proves this
// identity never gets past the Pod 403 card unscoped; this one proves the
// scoped launch reaches Connect and shows real pod rows. One flag is the
// entire difference.
func TestScopedLaunchLoadsRealPodRows(t *testing.T) {
	RequireCluster(t)
	a := Launch(t, WithKubeconfig(RestrictedKubeconfigPath()), WithScopeNamespace(Namespace))
	a.WaitForAll(Connect, "api-", "worker-")
}

// TestScopedModeConnectSettlesWithClusterScopedKindsForbidden: kute-restricted
// grants nothing cluster-scoped, so Namespace and Node stay denied even under
// scoped mode — only Pod's own eager cache is scoped to kute-e2e. Connect
// still has to complete and never hang, the Nodes screen has to show 4b's
// card rather than an empty list, and — the separate half of the claim — the
// rest of the session has to stay usable afterward, not merely "connect
// itself worked."
func TestScopedModeConnectSettlesWithClusterScopedKindsForbidden(t *testing.T) {
	RequireCluster(t)
	a := Launch(t, WithKubeconfig(RestrictedKubeconfigPath()), WithScopeNamespace(Namespace))
	a.WaitLoaded(Connect)
	a.Never("loading", 8*time.Second)

	a.gotoKind(t, "nodes", "Nodes")
	a.WaitLoaded(Settle)
	a.WaitForAll(Settle, "403 Forbidden", "nodes")

	// A Node 403 that quietly wedged the rest of the session would pass
	// everything above and fail only here.
	a.gotoKind(t, "pods", "Pods")
	a.WaitForAll(Settle, "api-", "worker-")
}

// TestScopedModeLazyGrantedKindLoads is the scoped-mode counterpart of
// TestReadableKindsWorkUnderAPartialGrant: a kind whose informer starts
// lazily, scoped to one namespace, under a Role that has never had
// cluster-wide list access at all.
func TestScopedModeLazyGrantedKindLoads(t *testing.T) {
	RequireCluster(t)
	a := Launch(t, WithKubeconfig(TeamKubeconfigPath()), WithScopeNamespace(Namespace))
	a.WaitForAll(Connect, "api-", "worker-")

	a.gotoKind(t, "configmaps", "ConfigMaps")
	a.WaitLoaded(Settle)
	a.WaitFor("app-config", Settle)
}

// TestScopedModeUngrantedKindStillShowsTheCard mirrors
// TestForbiddenKindIsScopedToThatKind's "one 403 does not take the app down"
// shape, under scoped mode instead of kute-partial. kute-team has no Secrets
// grant.
func TestScopedModeUngrantedKindStillShowsTheCard(t *testing.T) {
	RequireCluster(t)
	a := Launch(t, WithKubeconfig(TeamKubeconfigPath()), WithScopeNamespace(Namespace))
	a.WaitForAll(Connect, "api-", "worker-")

	a.gotoKind(t, "secrets", "Secrets")
	a.WaitLoaded(Settle)
	a.WaitForAll(Settle, "403 Forbidden", "secrets")
	a.WaitForWrapped(`kute-e2e:kute-team`, Settle)

	// This jump started from browse's own resting state (empty task stack),
	// so routeGoto mutated browse in place rather than pushing it — esc has
	// nothing to pop back to. Go to Pods the same way we got to Secrets.
	a.gotoKind(t, "pods", "Pods")
	a.WaitForAll(Settle, "api-", "worker-")
}

// TestScopedModeAllNamespacesFailsHonestly: kute-team's Role is namespaced,
// so a cluster-wide Pod LIST is Forbidden even though the scoped one just
// worked. This is docs/lazy-informers.md §5.6's
// namespaceScopedHint proven the other direction — a scoped session's
// explicit global read must still degrade to a permission card, never a
// false empty.
func TestScopedModeAllNamespacesFailsHonestly(t *testing.T) {
	RequireCluster(t)
	a := Launch(t, WithKubeconfig(TeamKubeconfigPath()), WithScopeNamespace(Namespace))
	a.WaitForAll(Connect, "api-", "worker-")

	a.Press("a")
	a.WaitLoaded(Settle)
	a.WaitForAll(Settle, "403 Forbidden", "pods")
}

// TestScopedModeReturningToGrantedNamespaceRestoresRows proves, through the
// real key-by-key palette flow, two things the final plan's unit tests could
// only assert separately: the denied namespace palette dispatches a typed
// name, and a namespace switch reloads.
func TestScopedModeReturningToGrantedNamespaceRestoresRows(t *testing.T) {
	RequireCluster(t)
	a := Launch(t, WithKubeconfig(TeamKubeconfigPath()), WithScopeNamespace(Namespace))
	a.WaitForAll(Connect, "api-", "worker-")

	a.Press("a")
	a.WaitLoaded(Settle)
	a.WaitForAll(Settle, "403 Forbidden", "pods")

	// kute-team has no cluster-wide Namespace access either, so the
	// namespace palette is in its denied state.
	a.Press("n")
	a.WaitForAll(Settle, "cannot list namespaces", "switch to")
	a.Type("kute-e2e")
	a.Enter()

	a.WaitForAll(Settle, "api-", "worker-")
}

// TestScopedModeNamespacePaletteShowsDeniedNotice is the real-cluster proof
// that a genuinely absent Namespace ClusterRole (not a hand-built fake
// lister) reaches the palette's denied state end to end.
func TestScopedModeNamespacePaletteShowsDeniedNotice(t *testing.T) {
	RequireCluster(t)
	a := Launch(t, WithKubeconfig(TeamKubeconfigPath()), WithScopeNamespace(Namespace))
	a.WaitForAll(Connect, "api-", "worker-")

	a.Press("n")
	a.WaitForWrapped("cannot list namespaces", Settle)
	a.WaitForWrapped("switch to", Settle)
}

func TestScopedModeSwitchesBetweenReadableNamespaceCaches(t *testing.T) {
	RequireCluster(t)
	a := Launch(t, WithKubeconfig(TeamKubeconfigPath()), WithScopeNamespace(Namespace))
	a.WaitForAll(Connect, "api-", "worker-")

	switchNamespaceThroughPalette(t, a, "kute-e2e-b")
	a.WaitForAll(Settle, "kute-e2e-b", "namespace-b-pod")
	a.Never("api-", 750*time.Millisecond)

	marker := "namespace-a-current-on-return"
	createDisposablePod(t, marker, map[string]string{"namespace-cache": "a"})
	a.Never(marker, 500*time.Millisecond)

	switchNamespaceThroughPalette(t, a, Namespace)
	a.WaitForAll(Settle, Namespace, "api-", marker)
	a.Never("namespace-b-pod", 750*time.Millisecond)

	// A second B→A round trip must reuse the same scoped caches; the wire-level
	// companion asserts the informer keys directly, while this pins the rows.
	switchNamespaceThroughPalette(t, a, "kute-e2e-b")
	a.WaitFor("namespace-b-pod", Settle)
	switchNamespaceThroughPalette(t, a, Namespace)
	a.WaitFor(marker, Settle)
}

func switchNamespaceThroughPalette(t *testing.T, a *App, namespace string) {
	t.Helper()
	a.Press("n")
	a.Type(namespace)
	a.WaitFor(namespace, Settle)
	a.Enter()
}
