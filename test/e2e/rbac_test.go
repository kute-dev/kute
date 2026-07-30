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
		"w who-can",
		"r retry",
	)
	a.WaitForWrapped(`kute-e2e:kute-restricted`, Settle)
}

// TestReadableKindsWorkUnderAPartialGrant is the other side: an identity with
// real but incomplete access is not a broken cluster. Everything it can read
// has to read normally, including a lazily-started kind, so that a failure on
// a kind it cannot read is visibly about that kind.
func TestReadableKindsWorkUnderAPartialGrant(t *testing.T) {
	a := Launch(t, WithKubeconfig(PartialKubeconfigPath()))

	a.WaitForAll(Connect, "api-", "worker-", "kute-e2e")
	a.waitForWorkerUnready(t)

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

	// 4b's card, naming the kind that was refused and the identity refused.
	a.WaitForAll(Settle, "403 Forbidden", "secrets")
	a.WaitForWrapped(`kute-e2e:kute-partial`, Settle)

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

// TestPartialGrantAcrossScreens walks the screens a partially-granted
// identity can reach and the ones it cannot, in one session, asserting the
// boundary holds in both directions.
//
// The point is the pairing. A suite that only checked the granted screens
// would pass with 403 handling entirely absent; one that only checked the
// refused screens would pass with the app broken for everyone. Every screen
// here has to either show real data or say plainly that it may not — and
// none may hang, because a permission failure settles a cache without ever
// filling it.
func TestPartialGrantAcrossScreens(t *testing.T) {
	a := Launch(t, WithKubeconfig(PartialKubeconfigPath()))
	a.WaitFor("api-", Connect)

	t.Run("granted screens render real data", func(t *testing.T) {
		// Pod detail, and the log stream — pods/log is granted separately
		// from pods, so this is its own permission.
		pod := a.selectAPIPod(t)
		a.Enter()
		a.WaitLoaded(Settle)
		a.WaitForAll(Settle, pod, "server", "sidecar")

		a.Press("l")
		a.WaitFor("KUTE-E2E-LOG-MARKER", Settle)
		a.Esc()
		a.WaitFor(pod, Settle)

		// Events and the timeline, both granted.
		a.Press("e")
		a.WaitLoaded(Settle)
		a.WaitFor("Started", Settle)
		a.Esc()
		a.WaitFor(pod, Settle)

		a.Press("t")
		a.WaitLoaded(Settle)
		a.WaitFor("Timeline", Settle)
		a.Esc()
		a.WaitFor(pod, Settle)
		a.Esc()
		a.WaitFor("api-", Settle)
	})

	t.Run("nodes are granted and cluster-scoped", func(t *testing.T) {
		a.gotoKind(t, "nodes", "Nodes")
		a.WaitLoaded(Settle)
		a.WaitFor(NodeNamePrefix(t), Settle)
		a.Enter()
		a.WaitLoaded(Settle)
		a.WaitFor("ALLOCATED / ALLOCATABLE", Settle)
		a.Esc()
		a.WaitFor("Nodes", Settle)
	})

	t.Run("refused kinds say so and do not hang", func(t *testing.T) {
		// Deployments, Secrets and Ingresses are all outside the grant, and
		// each has to land on 4b's card naming its own resource — not a
		// spinner, and not an empty list.
		for _, kind := range []struct{ query, resource string }{
			{"deployments", "deployments"},
			{"secrets", "secrets"},
			{"ingresses", "ingresses"},
		} {
			a.Press("g")
			a.Type(kind.query)
			a.Enter()
			a.WaitLoaded(Settle)
			a.WaitForAll(Settle, "403 Forbidden", kind.resource)
			// The identity goes through the wrap-tolerant matcher: the card
			// breaks "kute-partial" across two lines at some widths and not
			// others, purely on where the resource name pushes the wrap.
			a.WaitForWrapped(`kute-e2e:kute-partial`, Settle)
			if frame := a.Frame(); strings.Contains(frame, "No resources found") {
				t.Fatalf("%s rendered as an empty cluster rather than a refusal:\n%s", kind.query, frame)
			}
			a.Esc()
		}
	})

	// Still usable at the end of all that: the boundary is a boundary, not a
	// cumulative degradation.
	a.gotoKind(t, "pods", "Pods")
	a.WaitFor("api-", Settle)
}
