//go:build e2e

package e2e

import (
	"testing"
	"time"
)

// TestCertificateChainScreen covers §35a end to end, which nothing did
// before: tasks/certchain was reachable only through browse's ↵ on a
// Certificate row, and no e2e test ever pressed it.
//
// The screen's whole job is a walk no fake exercises honestly — four hops
// across *two* API groups (cert-manager.io → acme.cert-manager.io), matched
// by ownerRef Kind+Name, off kinds that exist only because discovery turned
// their CRDs into registry entries. The two chains in
// fixtures/57-certmanager-objects.yaml are shaped to pin the two things that
// walk can get wrong.
//
// Rows are reached with selectRow rather than filterTo: both certificates
// have to be visited in turn, and browse keeps a filter applied across the
// esc that leaves a pushed screen — so a second filterTo would wait forever
// for a row the first filter is still hiding.
func TestCertificateChainScreen(t *testing.T) {
	RequireCluster(t)
	a := Launch(t)
	a.WaitFor("api-", Connect)

	// §35b's own list first. Certificate is a discovered kind with a curated
	// descriptor, so EXPIRES/RENEWAL/ISSUER have to be its columns — a
	// regression to the generic CRD path renders the CRD's own declared
	// printer columns (Ready/Secret) instead, which is a silent downgrade
	// rather than an error.
	a.gotoKind(t, "certificates", "Certificates")
	a.WaitLoaded(Settle)
	a.WaitForAll(Settle, "EXPIRES", "RENEWAL", "ISSUER", "web-tls", "internal-tls")

	t.Run("failing acme chain", func(t *testing.T) {
		a.selectRow(t, "web-tls")
		a.Enter()
		a.WaitLoaded(Settle)

		// All four hops, each labelled with its own kind, in one frame —
		// WaitForAll rather than four waits, so a chain that only ever
		// showed them separately cannot pass.
		a.WaitForAll(Settle,
			"certificate/web-tls",
			"certificaterequest/web-tls-1",
			"order/web-tls-1-2748",
			"challenge/web-tls-1-2748-0",
		)

		// The promotion rule, which is the actual claim §35a makes. Three of
		// the four hops are non-green: Ready=False·Issuing on the
		// Certificate, Approved-but-not-Ready on the request, pending on the
		// order. None of those is a failure, and the banner must name the
		// one hop that genuinely is — the deepest, not the first.
		a.WaitForAll(Settle,
			"challenge failed",
			"Ready=False · Issuing",
			"Approved · not Ready",
			"http-01 · invalid",
		)
		// Wrapped: the ACME reason is a long sentence hard-wrapped to the
		// banner width, so it is broken mid-token in the frame.
		a.WaitForWrapped("NXDOMAIN looking up A record for web.kute-e2e.test", Settle)

		// The refs strip's two branches. This Certificate has never issued,
		// so its target Secret does not exist; its issuerRef is a
		// ClusterIssuer, which certchain resolves against the cluster-wide
		// ("") cache rather than against the object's own namespace.
		a.WaitForAll(Settle,
			"secret/web-tls", "missing",
			"clusterissuer/letsencrypt-e2e",
		)
		a.back(t, "Certificates")
	})

	t.Run("healthy ca chain", func(t *testing.T) {
		a.selectRow(t, "internal-tls")
		a.Enter()
		a.WaitLoaded(Settle)

		// Two hops and no ACME stage at all — a CA issuer has none, so the
		// screen has to render the short chain rather than inventing the
		// Order/Challenge rows the other certificate has. The issuerRef here
		// is a namespaced Issuer, the other side of the branch above.
		a.WaitForAll(Settle,
			"certificate/internal-tls",
			"certificaterequest/internal-tls-1",
			"secret/internal-tls", "exists",
			"issuer/internal-ca",
		)
		a.Never("order/", 2*time.Second)
		a.Never("challenge/", 2*time.Second)

		// "zero chrome until earned": nothing in this chain failed, so no
		// failure band may appear. Over a window rather than at an instant —
		// a walk that mis-promoted a still-in-flight hop would raise the
		// band on a later frame, which a one-shot check looks straight past.
		// Both titles this two-hop chain could possibly produce are named;
		// a bare "failed" would also match unrelated chrome.
		a.Never("certificate failed", 2*time.Second)
		a.Never("certificaterequest failed", 2*time.Second)
		a.back(t, "Certificates")
	})
}
