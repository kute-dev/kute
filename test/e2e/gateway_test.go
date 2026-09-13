//go:build e2e

package e2e

import (
	"testing"
)

// TestGatewayAPIScreens covers §23b, which had no fixture and no test — §23a
// (Ingress) was the only routing path this suite exercised.
//
// Everything here arrives through discovery: kute ships no Gateway API
// descriptors, so HTTPRoute's bespoke ATTACHED column, Gateway's listener
// table and the ↵ carve-out into tasks/routetable all depend on the CRDs
// being found on a live server and turned into registry entries. That is the
// "CRD support is data, not code" invariant in its least forgiving form —
// one discovered kind gets a hand-written Descriptor, addressed by Kind
// name, and a miss renders it as a generic custom resource instead.
func TestGatewayAPIScreens(t *testing.T) {
	RequireCluster(t)
	a := Launch(t)
	a.WaitFor("api-", Connect)

	// The list display name comes from the CRD's own plural, capitalized —
	// so the palette row and list title are "Httproutes", not "HTTPRoutes".
	t.Run("route list", func(t *testing.T) {
		a.gotoKind(t, "httproutes", "Httproutes")
		a.WaitLoaded(Settle)

		// §23b's ATTACHED column, inserted ahead of the CRD's own declared
		// printer columns — and both of the states it exists to tell apart:
		// the accepted route names the Gateway it attached to, the refused
		// one says plainly that it attached to nothing.
		//
		// Matched on the prefix of each cell, not the whole phrase: the
		// ATTACHED column is narrow and the table truncates it ("✓ gw/publ…"),
		// so the assertion has to stop where the column does. The condition's
		// verbatim message is asserted on the routing table below, which has
		// the room for it.
		a.WaitFor("ATTACHED", Settle)
		waitForRowPair(t, a, "shop-route", "✓ gw/")
		waitForRowPair(t, a, "stale-route", "✕ not acc")

		// The CRD's own printer column is still there, after the inserted
		// one rather than instead of it.
		a.WaitFor("HOSTNAMES", Settle)
	})

	// ↵ on a route opens the routing table rather than 14d's generic object
	// detail — the same carve-out Ingress takes.
	t.Run("route table", func(t *testing.T) {
		a.selectRow(t, "shop-route")
		a.Enter()
		a.WaitLoaded(Settle)

		// The breadcrumb segment no other screen renders.
		a.WaitFor("HTTPRoute/shop-route", Settle)
		a.WaitForAll(Settle, "MATCH", "WEIGHT", "BACKEND", "ENDPOINTS")

		// One row per rule-match × backendRef, with the weighted split
		// stacked under its match: 90/10 across two Services that exist.
		waitForRowPair(t, a, "web:80", "90%")
		waitForRowPair(t, a, "api:8080", "10%")

		// The second rule's match text — path, match type and header
		// clause — and its backend, which has no Service at all. A screen
		// that rendered spec.backendRefs without resolving them would show
		// this one exactly like the two above.
		a.WaitForWrapped("/internal exact", Settle)
		a.WaitForWrapped("header x-env=stage", Settle)
		a.WaitFor("checkout:8080", Settle)

		// The parent line: the Gateway this route attached to, resolved to
		// the listener named by the route's own sectionName.
		a.WaitForWrapped("gw/public", Settle)
		a.WaitForWrapped("listener HTTP:80", Settle)

		a.Esc()
		a.WaitFor("Httproutes", Settle)
	})

	// A Gateway's own routing table is the other flavor of the same screen:
	// one row per listener, with the attached-route count read from
	// status.listeners rather than from the spec.
	t.Run("gateway listeners", func(t *testing.T) {
		a.gotoKind(t, "gateways", "Gateways")
		a.WaitLoaded(Settle)
		a.selectRow(t, "public")
		a.Enter()
		a.WaitLoaded(Settle)

		a.WaitFor("Gateway/public", Settle)
		a.WaitForAll(Settle, "PROTO:PORT", "HOSTNAME", "ATTACHED")
		waitForRowPair(t, a, "http", "HTTP:80")
		// Truncated in the HOSTNAME column, same as the list above.
		waitForRowPair(t, a, "https", "shop.kute-e2e")
		// The class the Gateway declares, which the listener rows do not
		// carry themselves.
		a.WaitForWrapped("kute-e2e-class", Settle)

		a.Esc()
		a.WaitFor("Gateways", Settle)
	})
}
