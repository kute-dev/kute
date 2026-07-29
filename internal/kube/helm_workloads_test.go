package kube

import (
	"reflect"
	"strings"
	"testing"
)

// TestHelmReleaseWorkloadsReadsTheManifest: the release→workload link 18a's
// rollout glyph rides on. The manifest is the authority because Helm stamps
// no labels of its own — a chart that doesn't template
// app.kubernetes.io/instance still has to resolve.
func TestHelmReleaseWorkloadsReadsTheManifest(t *testing.T) {
	manifest := `---
# Source: nva/templates/serviceaccount.yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: nva-api
---
# Source: nva/templates/service.yaml
apiVersion: v1
kind: Service
metadata:
  name: nva-api
spec:
  ports:
  - port: 80
---
# Source: nva/templates/deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: nva-api
  labels:
    helm.sh/chart: nva-1.4.2
spec:
  template:
    spec:
      containers:
      - name: api
        image: nva:2.1.0
---
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: nva-queue
  namespace: nva-data
---
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: "nva-agent"
`
	got := HelmReleaseWorkloads(HelmRelease{Name: "nva", Namespace: "nva", Manifest: manifest})
	want := []WorkloadRef{
		{Kind: KindDeployment, Namespace: "nva", Name: "nva-api"},
		// An explicit metadata.namespace wins over the release's.
		{Kind: KindStatefulSet, Namespace: "nva-data", Name: "nva-queue"},
		{Kind: KindDaemonSet, Namespace: "nva", Name: "nva-agent"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("HelmReleaseWorkloads =\n %+v\nwant\n %+v", got, want)
	}
}

// TestHelmReleaseWorkloadsIgnoresNestedKinds: `kind:` appears all over a
// rendered manifest — in ownerReferences, in RBAC rules, in a CRD's schema.
// Only the document's own, unindented kind names the object.
func TestHelmReleaseWorkloadsIgnoresNestedKinds(t *testing.T) {
	manifest := `apiVersion: v1
kind: ConfigMap
metadata:
  name: nva-config
  ownerReferences:
  - kind: Deployment
    name: not-a-workload
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: nva
rules:
- resources: ["deployments"]
`
	if got := HelmReleaseWorkloads(HelmRelease{Namespace: "nva", Manifest: manifest}); len(got) != 0 {
		t.Errorf("HelmReleaseWorkloads = %+v, want none", got)
	}
}

// TestHelmReleaseWorkloadsToleratesJunk: an unparseable manifest costs the
// signal, never the list — the same best-effort tolerance DecodeHelmReleases
// applies to a Secret it can't read.
func TestHelmReleaseWorkloadsToleratesJunk(t *testing.T) {
	for _, manifest := range []string{
		"",
		"not yaml at all: [unclosed",
		"kind: Deployment\nmetadata:\n  labels: {a: b\n", // broken doc, no name
		"kind: Deployment\n",                             // workload with no name
	} {
		if got := HelmReleaseWorkloads(HelmRelease{Namespace: "nva", Manifest: manifest}); len(got) != 0 {
			t.Errorf("HelmReleaseWorkloads(%q) = %+v, want none", manifest, got)
		}
	}
}

// TestSplitYAMLDocumentsOnlySplitsAtColumnZero: an indented `---` inside a
// block scalar (a NOTES.txt, an embedded manifest in a ConfigMap) is data,
// not a document boundary.
func TestSplitYAMLDocumentsOnlySplitsAtColumnZero(t *testing.T) {
	docs := splitYAMLDocuments("kind: ConfigMap\ndata:\n  extra: |\n    ---\n    nested\n---\nkind: Deployment\n")
	if len(docs) != 2 {
		t.Fatalf("got %d documents, want 2: %q", len(docs), docs)
	}
	if !strings.Contains(docs[0], "nested") {
		t.Errorf("indented --- split a document in half: %q", docs[0])
	}
}
