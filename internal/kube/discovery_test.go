package kube

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func crdFixture(mutate func(obj map[string]any)) *unstructured.Unstructured {
	obj := map[string]any{
		"apiVersion": "apiextensions.k8s.io/v1",
		"kind":       "CustomResourceDefinition",
		"metadata":   map[string]any{"name": "certificates.cert-manager.io"},
		"spec": map[string]any{
			"group": "cert-manager.io",
			"names": map[string]any{"kind": "Certificate", "plural": "certificates"},
			"scope": "Namespaced",
			"versions": []any{
				map[string]any{
					"name": "v1", "served": true, "storage": true,
					"additionalPrinterColumns": []any{
						map[string]any{"name": "Ready", "type": "string", "jsonPath": `.status.conditions[?(@.type=="Ready")].status`},
						map[string]any{"name": "Secret", "type": "string", "jsonPath": ".spec.secretName"},
					},
				},
				map[string]any{"name": "v1beta1", "served": false, "storage": false, "deprecated": true},
			},
		},
		"status": map[string]any{
			"conditions": []any{
				map[string]any{"type": "Established", "status": "True"},
			},
		},
	}
	if mutate != nil {
		mutate(obj)
	}
	return &unstructured.Unstructured{Object: obj}
}

func TestParseDiscoveredKind(t *testing.T) {
	dk, ok := ParseDiscoveredKind(crdFixture(nil))
	if !ok {
		t.Fatalf("expected ok=true")
	}
	if dk.Kind != "Certificate" || dk.Plural != "certificates" || dk.Group != "cert-manager.io" {
		t.Fatalf("unexpected identity: %+v", dk)
	}
	if dk.ClusterScoped {
		t.Fatalf("expected Namespaced scope to parse as ClusterScoped=false")
	}
	if !dk.Established {
		t.Fatalf("expected Established=true")
	}
	if dk.GVR.Group != "cert-manager.io" || dk.GVR.Version != "v1" || dk.GVR.Resource != "certificates" {
		t.Fatalf("unexpected GVR: %+v", dk.GVR)
	}
	if len(dk.Versions) != 2 {
		t.Fatalf("expected 2 versions, got %d", len(dk.Versions))
	}
	if !dk.Versions[0].Served || !dk.Versions[0].Storage {
		t.Fatalf("v1 should be served+storage: %+v", dk.Versions[0])
	}
	if dk.Versions[1].Served || !dk.Versions[1].Deprecated {
		t.Fatalf("v1beta1 should be unserved+deprecated: %+v", dk.Versions[1])
	}
	if len(dk.PrinterColumns) != 2 || dk.PrinterColumns[0].Name != "Ready" || dk.PrinterColumns[1].Name != "Secret" {
		t.Fatalf("unexpected printer columns: %+v", dk.PrinterColumns)
	}
	if dk.CRDName != "certificates.cert-manager.io" {
		t.Fatalf("unexpected CRDName: %s", dk.CRDName)
	}
}

func TestParseDiscoveredKindNotEstablished(t *testing.T) {
	u := crdFixture(func(obj map[string]any) {
		obj["status"] = map[string]any{"conditions": []any{
			map[string]any{"type": "Established", "status": "False"},
		}}
	})
	dk, ok := ParseDiscoveredKind(u)
	if !ok || dk.Established {
		t.Fatalf("expected Established=false, got %+v (ok=%v)", dk, ok)
	}
}

func TestParseDiscoveredKindClusterScoped(t *testing.T) {
	u := crdFixture(func(obj map[string]any) {
		obj["spec"].(map[string]any)["scope"] = "Cluster"
	})
	dk, ok := ParseDiscoveredKind(u)
	if !ok || !dk.ClusterScoped {
		t.Fatalf("expected ClusterScoped=true, got %+v (ok=%v)", dk, ok)
	}
}

func TestParseDiscoveredKindFiltersRedundantNameAgeColumns(t *testing.T) {
	u := crdFixture(func(obj map[string]any) {
		versions := obj["spec"].(map[string]any)["versions"].([]any)
		v0 := versions[0].(map[string]any)
		cols := v0["additionalPrinterColumns"].([]any)
		cols = append(cols,
			map[string]any{"name": "Name", "type": "string", "jsonPath": ".metadata.name"},
			map[string]any{"name": "Age", "type": "date", "jsonPath": ".metadata.creationTimestamp"},
		)
		v0["additionalPrinterColumns"] = cols
	})
	dk, ok := ParseDiscoveredKind(u)
	if !ok {
		t.Fatalf("expected ok=true")
	}
	for _, c := range dk.PrinterColumns {
		if c.Name == "Name" || c.Name == "Age" {
			t.Fatalf("expected Name/Age to be filtered out, got %+v", dk.PrinterColumns)
		}
	}
}

func TestParseDiscoveredKindMissingRequiredFields(t *testing.T) {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apiextensions.k8s.io/v1",
		"kind":       "CustomResourceDefinition",
		"spec":       map[string]any{},
	}}
	if _, ok := ParseDiscoveredKind(u); ok {
		t.Fatalf("expected ok=false for a CRD missing group/kind/plural")
	}
}
