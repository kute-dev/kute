//go:build e2e

package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"
)

// TestFluxScreens drives §30a/§31a against real Flux CRDs served by a real
// API server, with no Flux controller running.
//
// That absence is deliberate and load-bearing. §30a's status precedence
// needs a suspended object, a failing one and a reconciling one visible at
// the same time; a real Flux install would reconcile all three into one
// state within seconds, and every assertion here would be racing it. With
// no controller, the hand-written status in 53-flux-objects.yaml is
// permanent — which is exactly the "never assert on transient state" rule
// the harness doc calls out.
func TestFluxScreens(t *testing.T) {
	a := Launch(t)
	a.WaitFor("api-", Connect)

	// The regression this whole design exists for. §18a's list is reached
	// by the same query on a cluster that is *also* serving
	// helmreleases.helm.toolkit.fluxcd.io — before the substitution, the
	// discovered descriptor replaced this one and the list rendered NAME +
	// AGE only, with no chart, version or status.
	t.Run("helm release list survives the flux crd", func(t *testing.T) {
		a.gotoKind(t, "helm rel", "Helm Releases")
		a.WaitForAll(Settle, "shop", "shop 1.3.0", "deployed")

		// And the Flux HelmRelease must not have leaked into it.
		a.Never("kute-podinfo", 2*time.Second)
	})

	t.Run("kustomizations", func(t *testing.T) {
		a.gotoKind(t, "kustomiz", "Kustomizations")
		a.WaitLoaded(Settle)

		// §30a's derived state for all three branches at once. "suspended"
		// is the READY cell, not a column Flux declares — kute derives it
		// from spec.suspend, and it has to outrank the object's own stale
		// Ready=True.
		a.WaitForAll(Settle,
			"kute-workers",
			"kute-infra",
			"suspended",
		)

		// The condition message verbatim, on the row — §30a's sub-line.
		a.WaitForWrapped("health check failed after 2m0s", Settle)

		// A reconciling object must not be reported as failed. Its row is
		// present; what must never appear is the generic read's verdict,
		// which would have shown it among the failures.
		frame := a.Frame()
		if strings.Contains(frame, "1 failed") && !strings.Contains(frame, "reconciling") {
			t.Errorf("a reconciling Kustomization was counted as a failure:\n%s", frame)
		}
	})

	t.Run("detail explains the failure", func(t *testing.T) {
		a.gotoKind(t, "kustomiz", "Kustomizations")
		a.WaitFor("kute-workers", Settle)
		// kute-workers sorts first: it is the only Fail-class row, and §30a
		// sorts unhealthy first.
		a.Enter()
		a.WaitFor("RECONCILE FAILED", Settle)

		a.WaitForWrapped("health check failed after 2m0s", Settle)
		// §31a's in-sync distinction: applied, but unhealthy — not drifted.
		a.WaitForWrapped("applied, failing health checks", Settle)
		// The inventory resolved against a real Deployment in the cluster.
		a.WaitForAll(Settle, "INVENTORY", "api")

		a.back(t, "Kustomizations")
	})

	t.Run("suspend and reconcile write to the cluster", func(t *testing.T) {
		a.gotoKind(t, "kustomiz", "Kustomizations")
		a.WaitFor("kute-apps", Settle)

		// Move to kute-apps (reconciling, so not the first row) rather than
		// acting on whatever happens to be under the cursor.
		a.selectListRow(t, "kute-apps")

		// The durable statement of the fact, read from the API rather than
		// from a frame: a suspend nothing reconciles away stays true.
		a.Press("s")
		waitForKustomization(t, "kute-apps", "became suspended", suspendIs(true))

		a.Press("s")
		waitForKustomization(t, "kute-apps", "resumed", suspendIs(false))

		a.Press("r")
		obj := waitForKustomization(t, "kute-apps", "gained a reconcile request",
			func(u *unstructured.Unstructured) bool {
				return u.GetAnnotations()["reconcile.fluxcd.io/requestedAt"] != ""
			})
		stamp := obj.GetAnnotations()["reconcile.fluxcd.io/requestedAt"]
		if _, err := time.Parse(time.RFC3339, stamp); err != nil {
			t.Errorf("reconcile annotation %q is not RFC3339 — Flux ignores a malformed stamp: %v", stamp, err)
		}
	})
}

// selectListRow moves the cursor down until want is the selected row in a
// browse list.
//
// Deliberately not editors_test.go's selectRow: that one matches on "›",
// which is §27b's secretdata marker. A browse list marks its selection with
// the accent bar "▎" (components.Table's SelBarStyle), so matching on the
// wrong glyph walks silently past every row.
func (a *App) selectListRow(t *testing.T, want string) {
	t.Helper()
	for range 20 {
		for _, line := range strings.Split(a.Frame(), "\n") {
			if strings.Contains(line, want) && strings.Contains(line, "▎") {
				return
			}
		}
		a.Down()
	}
	t.Fatalf("never reached row %q:\n%s", want, a.Frame())
}

// fluxDynamic builds a dynamic client against the e2e kubeconfig — the way
// to assert a write landed without reading it back through the UI that
// issued it.
func fluxDynamic(t *testing.T) dynamic.Interface {
	t.Helper()
	cfg, err := clientcmd.BuildConfigFromFlags("", KubeconfigPath())
	if err != nil {
		t.Fatalf("building a REST config from %s: %v", KubeconfigPath(), err)
	}
	client, err := dynamic.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("building a dynamic client: %v", err)
	}
	return client
}

var kustomizationGVR = schema.GroupVersionResource{
	Group: "kustomize.toolkit.fluxcd.io", Version: "v1", Resource: "kustomizations",
}

// waitForKustomization polls until pred is satisfied. The write goes out
// through a tea.Cmd, so it is not observable the instant the key is
// pressed — a one-shot read here would be a race by construction.
func waitForKustomization(t *testing.T, name, what string, pred func(*unstructured.Unstructured) bool) *unstructured.Unstructured {
	t.Helper()
	client := fluxDynamic(t).Resource(kustomizationGVR).Namespace("kute-e2e")
	deadline := time.Now().Add(Settle)
	var last *unstructured.Unstructured
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		obj, err := client.Get(ctx, name, metav1.GetOptions{})
		cancel()
		if err == nil {
			last = obj
			if pred(obj) {
				return obj
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("kustomization/%s never %s; last spec/metadata: %+v", name, what, last)
	return nil
}

func suspendIs(want bool) func(*unstructured.Unstructured) bool {
	return func(u *unstructured.Unstructured) bool {
		got, found, _ := unstructured.NestedBool(u.Object, "spec", "suspend")
		return found && got == want
	}
}
