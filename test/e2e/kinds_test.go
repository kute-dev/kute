//go:build e2e

package e2e

import (
	"strings"
	"testing"
)

// TestKindScreens opens each fixture group's screen the way a user reaches it
// — through the goto palette, then ↵ on the row — and asserts the fixture's
// own data reaches the frame.
//
// One Launch for all of them, because the point is partly that the kinds
// behind these screens are lazy: each subtest is the first read of its kind,
// starting that kind's informer from inside ListRaw, and every screen after
// the first is opened on a program that has already been running.
func TestKindScreens(t *testing.T) {
	a := Launch(t)
	a.WaitFor("api-", Connect)

	t.Run("configmap", func(t *testing.T) {
		a.openFrom(t, "configmaps", "ConfigMaps", "app-config")
		a.WaitLoaded(Settle)

		// §27a's grid, with both shapes of value the fixture exists to
		// provide: a short one shown inline, and a folded one that only the
		// buffer editor can reach.
		a.WaitForAll(Settle,
			"cm/app-config",
			"mode", "production",
			"log-level", "info",
			"nginx.conf",
		)
		if frame := a.Frame(); !strings.Contains(frame, "8 lines") {
			t.Errorf("the multi-line value is not folded — §27a shows a line count, not the body:\n%s", frame)
		}
		a.back(t, "ConfigMaps")
	})

	t.Run("secret", func(t *testing.T) {
		a.openFrom(t, "secrets", "Secrets", "app-secret")
		a.WaitLoaded(Settle)

		// §27b: keys visible, values masked, sizes real. The value must not
		// be on screen — a masked grid that leaks the secret is the one bug
		// this screen exists to not have.
		a.WaitForAll(Settle, "secret/app-secret", "api-token", "database-url", "Opaque")
		frame := a.Frame()
		if !strings.Contains(frame, "•") {
			t.Errorf("no masked values in the Secret grid:\n%s", frame)
		}
		if strings.Contains(frame, "KUTE-E2E-SECRET-VALUE") {
			t.Fatalf("the Secret's value rendered unmasked:\n%s", frame)
		}
		a.back(t, "Secrets")
	})

	t.Run("ingress", func(t *testing.T) {
		a.openFrom(t, "ingresses", "Ingresses", "shop")
		a.WaitLoaded(Settle)

		// §23a: one row per host+path across both hosts, with backends
		// resolved live against the cluster — which is why the fixture has a
		// backend Service that exists and one that does not.
		a.WaitForAll(Settle,
			"shop.kute-e2e.test",
			"admin.kute-e2e.test",
			"web:80", "api:8080", "checkout:8080",
			"2 ready",
		)
		a.back(t, "Ingress")
	})

	// The load-bearing one. kute has never heard of a Widget: everything on
	// these two screens comes from API discovery turning the CRD into a
	// kind-registry entry — its printer columns become the list's columns,
	// its Ready condition becomes the status derivation — with no per-CRD
	// layout code anywhere.
	t.Run("crd", func(t *testing.T) {
		a.gotoKind(t, "widgets", "Widgets")

		// The list's columns are the CRD's own additionalPrinterColumns, and
		// the health tally is derived from each Widget's Ready condition.
		a.WaitForAll(Settle,
			"SIZE", "COLOUR", "PHASE",
			"sprocket", "large", "cerulean",
			"flange", "small",
		)

		// ↵ on the not-Ready one opens §14d, which reads the same columns
		// back as a meta grid and shows the condition that made it not Ready.
		a.WaitFor("flange", Settle)
		a.Enter()
		a.WaitLoaded(Settle)
		a.WaitForAll(Settle,
			"flange",
			"kute.dev/v1",
			"CONDITIONS", "Ready", "False",
			"flange will not disengage",
		)
		a.back(t, "Widgets")
	})

	t.Run("helm", func(t *testing.T) {
		a.gotoKind(t, "helm", "Helm Releases")

		// Decoded straight out of the release Secret: chart, app version,
		// revision and status, with no helm binary involved.
		a.WaitForAll(Settle, "shop", "shop 1.3.0", "2.5.0", "deployed")

		// h opens §18a's revision rail, which is the only screen that reads
		// past the newest revision — both fixture revisions have to be there,
		// the superseded one included.
		a.Press("h")
		a.WaitLoaded(Settle)
		a.WaitForAll(Settle, "shop", "1.3.0", "1.2.0", "superseded")
		a.Esc()
		a.WaitFor("Helm Releases", Settle)
	})
}

// TestJumpPaletteGainsKindsWhileOpen holds the palette open across discovery.
//
// Its corpus is snapshotted when it opens, so a kind discovery finds a moment
// later used to stay invisible until the palette was closed and reopened —
// and a query for it does not come back empty, it matches the kind's own CRD
// row and jumps to CustomResourceDefinitions, which is a wrong answer rather
// than a missing one.
//
// 'g' is the very first key here, before even the connect wait, which is what
// puts the open palette ahead of discovery. On a cluster whose discovery beats
// it the assertion is merely true on arrival — it can be vacuous, never wrong.
func TestJumpPaletteGainsKindsWhileOpen(t *testing.T) {
	a := Launch(t)
	a.Press("g")
	a.Type("kustomiz")
	a.WaitFor("Kustomizations", Settle)
}

// openFrom jumps to a kind's list, puts the cursor on the named row and
// opens it.
//
// list is the destination list's own title, and passing row for it instead
// is a race, not a shortcut: the palette's enter dispatches the kind switch
// as a command, so the frame gotoKind fences on can still be the *previous*
// kind's list. A row name that also appears there matches it immediately —
// "shop" is an Ingress here and also the middle of the Secrets list's
// sh.helm.release.v1.shop.v2 — and ↵ then fires at a list whose rows haven't
// arrived, which browse correctly ignores, leaving the test waiting forever
// for a screen it never asked to open. Waiting for the title first is what
// proves the switch landed; only then does the row wait mean anything,
// because switching kinds clears the rows.
func (a *App) openFrom(t *testing.T, query, list, row string) {
	t.Helper()
	a.gotoKind(t, query, list)
	a.WaitFor(row, Settle)
	a.selectRow(t, row)
	a.Enter()
}

// back leaves a pushed screen and waits for the list it came from — esc walks
// back exactly one level, so one press is always the right number.
func (a *App) back(t *testing.T, want string) {
	t.Helper()
	a.Esc()
	a.WaitFor(want, Settle)
}
