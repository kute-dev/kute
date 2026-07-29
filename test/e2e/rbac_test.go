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
	// empty state used to appear. A one-shot check here passes by looking
	// too early, which is exactly how this went unnoticed.
	a.Never("you can read it", 20*time.Second)
	if frame := a.Frame(); strings.Contains(frame, falseEmpty) {
		t.Fatalf("every read this identity makes is Forbidden, and the screen reports an empty namespace:\n%s", frame)
	}

	// And the right answer is on screen, not merely the absence of the wrong
	// one: 4b's card, with the apiserver's own words and a way forward.
	a.WaitForAll(Settle,
		"403 Forbidden",
		"forbidden",
		"kute-restricted",
		"w who-can",
		"r retry",
	)
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

// TestForbiddenKindIsScopedToThatKind: Secrets are not granted to
// kute-partial. The denial has to stay the Secrets screen's problem — it must
// not hang, must not read as an empty cluster, and must not take the rest of
// the app down with it.
//
// The last of those is the one worth spelling out. A 403 arrives on a
// reflector's goroutine, and routing it into the connection state made one
// refused kind replace every working screen with "cluster is unreachable" and
// a backoff countdown that could not help. Often a kind the user never asked
// for, since the eager set and CRD discovery both run unprompted.
func TestForbiddenKindIsScopedToThatKind(t *testing.T) {
	a := Launch(t, WithKubeconfig(PartialKubeconfigPath()))
	a.WaitFor("api-", Connect)

	a.gotoKind(t, "secrets", "Secrets")
	a.WaitLoaded(Settle)

	// 4b's card, naming the kind that was refused.
	a.WaitForAll(Settle, "403 Forbidden", "secrets", "kute-partial")

	// The connection is not the thing that failed, and the header must not
	// say it was. Checked over a window, because the misreport arrived
	// asynchronously from the reflector rather than in the frame the
	// keypress produced.
	a.Never("unreachable", 8*time.Second)

	// And a readable kind still reads, after the denial rather than before
	// it: the app is usable, minus the one kind this identity cannot see.
	a.Esc()
	a.gotoKind(t, "pods", "Pods")
	a.WaitFor("api-", Settle)
}
