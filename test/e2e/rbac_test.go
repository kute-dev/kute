//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"
)

// falseEmpty is browse's empty state. Reaching it is a positive claim about
// the cluster — "there is nothing of this kind here" — which is only ever
// true if the read that produced it succeeded.
const falseEmpty = "no pods in"

// TestForbiddenKindNeverRendersAsEmpty is the regression this file exists
// for. CLAUDE.md: "An empty state is a claim about the cluster" — a kind that
// is merely unreadable must never be presented as a kind the cluster has none
// of, and must not hang on a spinner either.
//
// kute's informers list every kind cluster-wide regardless of the selected
// namespace, so kute-restricted — which may read Pods only inside kute-e2e —
// is Forbidden on every read it makes, including Pods.
func TestForbiddenKindNeverRendersAsEmpty(t *testing.T) {
	a := Launch(t, WithKubeconfig(RestrictedKubeconfigPath()))

	// Settled: whatever the app decides to show, it has to stop loading.
	// This half holds today — KindSynced reports a forbidden cache settled
	// precisely so a spinner cannot hang.
	a.WaitLoaded(Connect)

	// Over a window, not at an instant. The first frame after connect shows
	// the watch error, but the /livez ping loop keeps succeeding — the API
	// server is perfectly reachable, it is this identity that is refused —
	// so the connection state recovers to "connected" while every informer
	// stays forbidden. It is at *that* point, some seconds in, that the
	// empty state appears. A one-shot check here passes by looking too early.
	a.Never("you can read it", 20*time.Second)
	if frame := a.Frame(); strings.Contains(frame, falseEmpty) {
		t.Fatalf("every read this identity makes is Forbidden, and the screen reports an empty namespace:\n%s", frame)
	}
}

// TestReadableKindsWorkUnderAPartialGrant is the other side: an identity with
// real but incomplete access is not a broken cluster. Everything it can read
// has to read normally, including a lazily-started kind, so that a failure on
// a kind it cannot read is visibly about that kind.
func TestReadableKindsWorkUnderAPartialGrant(t *testing.T) {
	a := Launch(t, WithKubeconfig(PartialKubeconfigPath()))

	a.WaitForAll(Connect, "api-", "worker-", "kute-e2e")
	a.WaitFor("CrashLoopBackOff", Settle)

	// ConfigMaps are granted, and are not eager — so this is a lazily
	// started informer filling under a restricted identity.
	a.gotoKind(t, "configmaps", "ConfigMaps")
	a.WaitFor("app-config", Settle)
	a.WaitLoaded(Settle)
}

// TestForbiddenKindDoesNotHang: Secrets are not granted to kute-partial. The
// screen must settle rather than spin, whatever else it decides to show.
func TestForbiddenKindDoesNotHang(t *testing.T) {
	a := Launch(t, WithKubeconfig(PartialKubeconfigPath()))
	a.WaitFor("api-", Connect)

	a.gotoKind(t, "secrets", "Secrets")
	a.WaitLoaded(Settle)
}
