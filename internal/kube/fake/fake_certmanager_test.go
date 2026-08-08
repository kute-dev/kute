package fake

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/kute-dev/kute/internal/kube"
)

// ownedBy mirrors certchain's own ownerRef match — Kind+Name, never UID —
// kept as a small local copy so this test doesn't import a tui/tasks
// package into internal/kube/fake.
func ownedBy(t *testing.T, objs []*unstructured.Unstructured, kind, name string) []*unstructured.Unstructured {
	t.Helper()
	var out []*unstructured.Unstructured
	for _, u := range objs {
		for _, ref := range u.GetOwnerReferences() {
			if ref.Kind == kind && ref.Name == name {
				out = append(out, u)
			}
		}
	}
	return out
}

func unstructuredList(t *testing.T, c *Cluster, kind kube.ResourceKind, ns string) []*unstructured.Unstructured {
	t.Helper()
	objs, err := c.ListRaw(context.Background(), kind, ns)
	if err != nil {
		t.Fatalf("ListRaw(%s): %v", kind, err)
	}
	out := make([]*unstructured.Unstructured, 0, len(objs))
	for _, o := range objs {
		u, ok := o.(*unstructured.Unstructured)
		if !ok {
			t.Fatalf("%s: expected an unstructured object, got %T", kind, o)
		}
		out = append(out, u)
	}
	return out
}

// TestDemoCertManagerChainOwnerRefsResolve pins §35a's own demo fixture: the
// Certificate → CertificateRequest → Order → Challenge ownerRef chain
// resolves exactly the way certchain's load.go walks it — one hop at a
// time, matched by Kind+Name.
func TestDemoCertManagerChainOwnerRefsResolve(t *testing.T) {
	t.Parallel()
	c := NewDemo()

	crs := ownedBy(t, unstructuredList(t, c, kube.KindCertificateRequest, "default"), "Certificate", "web-tls")
	if len(crs) != 4 {
		t.Fatalf("expected 4 CertificateRequests owned by web-tls (the honest 'attempt 4' count), got %d", len(crs))
	}

	orders := ownedBy(t, unstructuredList(t, c, kube.KindOrder, "default"), "CertificateRequest", "web-tls-1")
	if len(orders) != 1 {
		t.Fatalf("expected 1 Order owned by web-tls-1, got %d", len(orders))
	}
	orderName := orders[0].GetName()
	if orderName != "web-tls-1-2847563921" {
		t.Fatalf("unexpected Order name %q", orderName)
	}
	state, _, _ := unstructured.NestedString(orders[0].Object, "status", "state")
	if state != "errored" {
		t.Fatalf("expected the Order's status.state = errored, got %q", state)
	}

	challenges := ownedBy(t, unstructuredList(t, c, kube.KindChallenge, "default"), "Order", orderName)
	if len(challenges) != 1 {
		t.Fatalf("expected 1 Challenge owned by %s, got %d", orderName, len(challenges))
	}
	reason, _, _ := unstructured.NestedString(challenges[0].Object, "status", "reason")
	if reason == "" {
		t.Fatal("expected the Challenge's status.reason to carry the propagation failure, verbatim")
	}
}

// TestRenewCertificateFlipsReadyFalse pins §35c's fake-cluster behavior:
// RenewCertificate has no real cert-manager controller to react to a real
// Issuing condition in demo mode, so it flips Ready to False/reason
// "Issuing" directly — visibly "renewal requested", never a stuck failure
// (no lastFailureTime, so the row reads Warn not Fail).
func TestRenewCertificateFlipsReadyFalse(t *testing.T) {
	t.Parallel()
	c := NewDemo()

	if err := c.RenewCertificate(context.Background(), "default", "admin-tls"); err != nil {
		t.Fatalf("RenewCertificate: %v", err)
	}

	certs := unstructuredList(t, c, kube.KindCertificate, "default")
	var admin *unstructured.Unstructured
	for _, u := range certs {
		if u.GetName() == "admin-tls" {
			admin = u
		}
	}
	if admin == nil {
		t.Fatal("admin-tls not found after RenewCertificate")
	}
	conds, _, _ := unstructured.NestedSlice(admin.Object, "status", "conditions")
	if len(conds) != 1 {
		t.Fatalf("expected exactly one condition after renew, got %d", len(conds))
	}
	cond, _ := conds[0].(map[string]any)
	if status, _, _ := unstructured.NestedString(cond, "status"); status != "False" {
		t.Fatalf("expected Ready status False after renew, got %q", status)
	}
	if reason, _, _ := unstructured.NestedString(cond, "reason"); reason != "Issuing" {
		t.Fatalf("expected reason Issuing after renew, got %q", reason)
	}
	if _, found, _ := unstructured.NestedString(admin.Object, "status", "lastFailureTime"); found {
		t.Fatal("renew must not set lastFailureTime — this is a manual trigger in progress, not a stuck failure")
	}
}

// TestRenewCertificateUnknownName reports the same "not found" error every
// other fake mutator method gives for a name it never seeded.
func TestRenewCertificateUnknownName(t *testing.T) {
	t.Parallel()
	c := NewDemo()
	if err := c.RenewCertificate(context.Background(), "default", "does-not-exist"); err == nil {
		t.Fatal("expected an error renewing an unseeded Certificate")
	}
}
