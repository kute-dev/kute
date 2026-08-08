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
