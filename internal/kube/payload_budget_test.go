package kube

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/dynamicinformer"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// The payload numbers docs/lazy-informers.md is built on live in a markdown
// table: 2.76 MB of CRD OpenAPI schema pulled at connect, 8.19 MB of release
// Secrets cluster-wide against 4 MB for one namespace, managedFields
// "routinely a third to a half of the stored bytes". Those figures are the
// entire justification for lazy informers, for the CRD carve-out and for the
// per-namespace Helm cache — and prose cannot fail CI.
//
// The tests here restate them as budgets. They are not a substitute for the
// shape assertions in lazy_test.go and helm_secrets_test.go (which resource
// got listed, with which selector, in which namespace); they are the axis
// those can't see. A read can keep its resource, its selector and its scope
// and still get much more expensive — a new eager informer over a fat kind,
// a transform that stops stripping, a filter that stops being applied to
// what the cache actually holds. What follows measures bytes, so it fails on
// that.
//
// The absolute numbers are a fake clientset's, not a cluster's. What carries
// over is the *proportion*: each fixture is built with the doc's own shape
// (schema-heavy CRDs, manifest-heavy release Secrets, managedFields-heavy
// pods), and each budget is stated against the mass sitting in the fixture
// rather than as a magic constant.

const (
	// Per the measured cluster: 48 CRDs, 2.76 MB, ~57 KB of OpenAPI schema
	// each, 98.2% of it never read.
	fixtureCRDCount      = 48
	fixtureCRDSchemaSize = 57 << 10

	// Release Secrets carry the release's whole gzipped manifest, which is
	// what makes a cluster-wide list of them expensive.
	fixtureReleaseSize = 40 << 10
)

// listKinds maps each resource this file's fixtures use to its object kind,
// so a recorded list action can be replayed against the tracker to find out
// what it would have sent back. (The tracker appends "List" itself.)
var listKinds = map[string]schema.GroupVersionKind{
	"pods":       {Version: "v1", Kind: "Pod"},
	"nodes":      {Version: "v1", Kind: "Node"},
	"namespaces": {Version: "v1", Kind: "Namespace"},
	"secrets":    {Version: "v1", Kind: "Secret"},
}

// transferred sums the serialized size of everything the recorded list
// actions would have put on the wire, replaying each against the tracker
// with the same namespace and field selector the real request carried.
//
// Replayed rather than measured directly because the fake clientset records
// the request, not the response — but the request plus the tracker is
// exactly what the response is, which is the point: it prices what a read
// *asked for*, so narrowing a request shows up as fewer bytes.
func transferred(t *testing.T, tracker k8stesting.ObjectTracker, actions []k8stesting.Action) int {
	t.Helper()
	total := 0
	for _, a := range actions {
		la, ok := a.(k8stesting.ListAction)
		if !ok || a.GetVerb() != "list" {
			continue
		}
		gvk, known := listKinds[a.GetResource().Resource]
		if !known {
			t.Fatalf("no list kind registered for %q — add it to listKinds", a.GetResource().Resource)
		}
		list, err := tracker.List(a.GetResource(), gvk, a.GetNamespace())
		if err != nil {
			t.Fatalf("replaying the %s list: %v", a.GetResource().Resource, err)
		}
		objs, err := extractItems(list)
		if err != nil {
			t.Fatalf("reading the %s list: %v", a.GetResource().Resource, err)
		}
		for _, obj := range objs {
			if !matchesFieldSelector(t, obj, la.GetListRestrictions().Fields.String()) {
				continue
			}
			total += serializedSize(t, obj)
		}
	}
	return total
}

// transferredDynamic is transferred for the dynamic client the CRD informer
// runs on: unstructured objects, one GVR, no field selectors in play.
func transferredDynamic(t *testing.T, tracker k8stesting.ObjectTracker, actions []k8stesting.Action) int {
	t.Helper()
	total := 0
	for _, a := range actions {
		if a.GetVerb() != "list" {
			continue
		}
		list, err := tracker.List(a.GetResource(), schema.GroupVersionKind{
			Group: crdGVR.Group, Version: crdGVR.Version, Kind: "CustomResourceDefinition",
		}, a.GetNamespace())
		if err != nil {
			t.Fatalf("replaying the CRD list: %v", err)
		}
		objs, err := extractItems(list)
		if err != nil {
			t.Fatalf("reading the CRD list: %v", err)
		}
		for _, obj := range objs {
			total += serializedSize(t, obj)
		}
	}
	return total
}

func extractItems(list runtime.Object) ([]runtime.Object, error) {
	return apimeta.ExtractList(list)
}

func serializedSize(t *testing.T, obj runtime.Object) int {
	t.Helper()
	b, err := json.Marshal(obj)
	if err != nil {
		t.Fatalf("sizing %T: %v", obj, err)
	}
	return len(b)
}

// matchesFieldSelector applies the one field selector this codebase sends —
// the release Secrets' `type=helm.sh/release.v1`. Anything else is treated
// as "no narrowing", so a selector that silently stopped being applied would
// price as the full unfiltered list.
func matchesFieldSelector(t *testing.T, obj runtime.Object, selector string) bool {
	t.Helper()
	if selector == "" {
		return true
	}
	typ, found := strings.CutPrefix(selector, "type=")
	if !found {
		t.Fatalf("unrecognized field selector %q — teach matchesFieldSelector about it", selector)
	}
	secret, ok := obj.(*corev1.Secret)
	if !ok {
		return true
	}
	return string(secret.Type) == typ
}

// fatCRD is one CRD carrying a realistic OpenAPI validation schema: the
// thing that made listing CRD objects cost 2.76 MB, and that kute reads none
// of (it wants the name, the versions and the printer columns).
func fatCRD(i int) *unstructured.Unstructured {
	plural := fmt.Sprintf("widget%ds", i)
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apiextensions.k8s.io/v1",
		"kind":       "CustomResourceDefinition",
		"metadata":   map[string]any{"name": plural + ".example.test"},
		"spec": map[string]any{
			"group": "example.test",
			"scope": "Namespaced",
			"names": map[string]any{"kind": fmt.Sprintf("Widget%d", i), "plural": plural},
			"versions": []any{map[string]any{
				"name": "v1", "served": true, "storage": true,
				"schema": map[string]any{"openAPIV3Schema": map[string]any{
					"type": "object",
					// Stands in for the real thing's thousands of property
					// definitions — same bytes, same "kute reads none of it".
					"description": strings.Repeat("x", fixtureCRDSchemaSize),
				}},
			}},
		},
	}}
}

// releaseSecret is one helm revision Secret sized like a real one: the
// gzipped manifest dominates, which is why scope matters more here than for
// any other kind.
func releaseSecret(release, namespace string, revision int) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("sh.helm.release.v1.%s.v%d", release, revision),
			Namespace: namespace,
		},
		Type: HelmReleaseSecretType,
		Data: map[string][]byte{"release": []byte(strings.Repeat("r", fixtureReleaseSize))},
	}
}

// newBudgetCluster is newLazyTestCluster with the dynamic client seeded, so
// the CRD informer has fat CRDs to pull if anything asks it to.
func newBudgetCluster(crds []runtime.Object, objs ...runtime.Object) (*Cluster, *fake.Clientset, *dynamicfake.FakeDynamicClient) {
	cs := fake.NewSimpleClientset(objs...)
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		crdGVR: "CustomResourceDefinitionList",
	}, crds...)
	return &Cluster{
		clientset:  cs,
		factory:    newTypedFactory(cs),
		dynClient:  dyn,
		dynFactory: dynamicinformer.NewDynamicSharedInformerFactory(dyn, 0),
		stopCh:     make(chan struct{}),
		events:     make(chan ResourceChangedMsg, 256),
		health:     newHealth(),
	}, cs, dyn
}

// TestConnectPayloadStaysWithinBudget prices a connect against a cluster
// whose expensive kinds are all present: docs/lazy-informers.md §3's "≈ 21.8
// MB before, ≈ 1.3 MB after", as a number this repo can fail on.
//
// The fixture deliberately holds several megabytes that a connect must not
// touch — 48 schema-heavy CRDs and a pile of release Secrets — so the budget
// is a statement about laziness, not about how big the fixture is. Reaching
// for any of it (a new eager informer, a CRD object list at connect) blows
// the budget by an order of magnitude rather than by a few percent.
func TestConnectPayloadStaysWithinBudget(t *testing.T) {
	crds := make([]runtime.Object, 0, fixtureCRDCount)
	for i := range fixtureCRDCount {
		crds = append(crds, fatCRD(i))
	}
	var objs []runtime.Object
	for i := range 91 {
		objs = append(objs, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("api-%d", i), Namespace: "default",
		}})
	}
	for i := range 12 {
		objs = append(objs, releaseSecret(fmt.Sprintf("chart-%d", i), "default", 1))
	}

	c, cs, dyn := newBudgetCluster(crds, objs...)
	defer c.Stop()
	if err := c.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, "the eager caches to fill", func() bool { return c.Synced() })

	typed := transferred(t, cs.Tracker(), cs.Actions())
	crdBytes := transferredDynamic(t, dyn.Tracker(), dyn.Actions())

	// Everything a connect must leave on the server, for the failure message
	// to be able to say what the alternative costs.
	unread := fixtureCRDCount*fixtureCRDSchemaSize + 12*fixtureReleaseSize

	if crdBytes != 0 {
		t.Errorf("connect pulled %d bytes of CRD objects; the CRD *names* come from the metadata client and discovery instead, which is what took this from 2.76 MB to ~15 KB (docs/lazy-informers.md §5.1)", crdBytes)
	}
	// 64 KB is roughly 40× the eager fixture's own weight and ~2% of what's
	// sitting unread beside it — loose enough that adding a pod or a
	// namespace to the fixture never trips it, tight enough that enlisting
	// one more kind does.
	const budget = 64 << 10
	if typed > budget {
		t.Errorf("connect transferred %d bytes, over the %d budget (%d bytes of CRD schema and release manifests sat unread beside it)", typed, budget, unread)
	}
}

// TestHelmReleaseReadPaysForOneNamespace is docs/lazy-informers.md §5.5's
// "8.19 MB cluster-wide against 4 MB for one namespace" — the carve-out that
// gives release Secrets one informer *per namespace read* rather than a
// single cluster-wide one, the only kind in the codebase treated that way.
//
// helm_secrets_test.go already asserts the list is scoped. This asserts what
// the scoping is *for*: the read has to get materially cheaper, which is the
// claim the carve-out is justified by and the reason it survives a "why
// isn't this like every other kind" reading of watch.go.
func TestHelmReleaseReadPaysForOneNamespace(t *testing.T) {
	var objs []runtime.Object
	for _, ns := range []string{"prod", "staging", "dev"} {
		for i := range 8 {
			objs = append(objs, releaseSecret(fmt.Sprintf("chart-%d", i), ns, 1))
		}
	}

	scoped, csScoped, _ := newBudgetCluster(nil, objs...)
	defer scoped.Stop()
	if err := scoped.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := scoped.ListHelmReleaseSecrets(t.Context(), "prod"); err != nil {
		t.Fatalf("ListHelmReleaseSecrets: %v", err)
	}
	waitFor(t, "the prod release cache to fill", func() bool { return scoped.KindSynced(KindHelmRelease, "prod") })
	oneNamespace := transferred(t, csScoped.Tracker(), csScoped.Actions())

	// The same read with no namespace — 19a's overview genuinely wants every
	// namespace, and is the price the scoped read is being compared against.
	wide, csWide, _ := newBudgetCluster(nil, objs...)
	defer wide.Stop()
	if err := wide.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := wide.ListHelmReleaseSecrets(t.Context(), ""); err != nil {
		t.Fatalf("ListHelmReleaseSecrets: %v", err)
	}
	waitFor(t, "the cluster-wide release cache to fill", func() bool { return wide.KindSynced(KindHelmRelease, "") })
	clusterWide := transferred(t, csWide.Tracker(), csWide.Actions())

	if clusterWide == 0 {
		t.Fatal("the cluster-wide read priced at zero bytes — the accounting is broken, not the code")
	}
	// Three namespaces hold the releases, so one namespace's read should be
	// near a third. Half leaves room for the fixture to grow unevenly while
	// still failing outright if the scoping is lost.
	if oneNamespace*2 > clusterWide {
		t.Errorf("reading one namespace's releases cost %d bytes against %d cluster-wide — a namespace-scoped read must not pull every namespace's release manifests (docs/lazy-informers.md §5.5)",
			oneNamespace, clusterWide)
	}
}

// TestPodCacheDropsManagedFields prices docs/lazy-informers.md §4's
// managedFields claim: server-side-apply bookkeeping is "routinely a third
// to a half of the stored bytes" and kute reads none of it.
//
// The transform runs after the reflector decodes, so this is cache footprint
// rather than wire bytes — which is exactly why no action-recording test can
// see it. Without a size assertion, stripManagedFields could quietly stop
// stripping (a transform that isn't registered on a newly added informer, a
// tombstone path that returns early) and every screen would still render
// identically.
func TestPodCacheDropsManagedFields(t *testing.T) {
	const pods = 40
	var objs []runtime.Object
	for i := range pods {
		p := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("api-%d", i), Namespace: "default",
			ManagedFields: fatManagedFields(),
		}}
		objs = append(objs, p)
	}

	c, cs, _ := newBudgetCluster(nil, objs...)
	defer c.Stop()
	if err := c.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, "the pod cache to fill", func() bool { return c.KindSynced(KindPod, "") })

	cached, err := c.ListRaw(t.Context(), KindPod, "default")
	if err != nil {
		t.Fatalf("ListRaw: %v", err)
	}
	if len(cached) != pods {
		t.Fatalf("cache holds %d pods, want %d", len(cached), pods)
	}

	cachedBytes := 0
	for _, obj := range cached {
		if p, ok := obj.(*corev1.Pod); ok && len(p.ManagedFields) != 0 {
			t.Fatalf("%s kept %d managedFields entries in cache", p.Name, len(p.ManagedFields))
		}
		cachedBytes += serializedSize(t, obj)
	}
	wireBytes := transferred(t, cs.Tracker(), cs.Actions())

	// A third is the doc's low end; asserting the cache is under two thirds
	// of the wire form states that without pinning the exact ratio, which
	// depends on how much else the object carries.
	if cachedBytes*3 > wireBytes*2 {
		t.Errorf("cached pods are %d bytes against %d on the wire — managedFields are not being stripped (internal/kube/transform.go)", cachedBytes, wireBytes)
	}
}

// fatManagedFields is one object's worth of server-side-apply bookkeeping:
// several controllers, each listing the fields it owns.
func fatManagedFields() []metav1.ManagedFieldsEntry {
	var out []metav1.ManagedFieldsEntry
	for _, manager := range []string{"kubectl-client-side-apply", "kube-controller-manager", "kubelet", "cluster-autoscaler"} {
		out = append(out, metav1.ManagedFieldsEntry{
			Manager:    manager,
			Operation:  metav1.ManagedFieldsOperationUpdate,
			APIVersion: "v1",
			FieldsType: "FieldsV1",
			FieldsV1:   &metav1.FieldsV1{Raw: []byte(`{"f:metadata":{"f:annotations":{".":{},"f:` + strings.Repeat("a", 400) + `":{}}}}`)},
		})
	}
	return out
}
