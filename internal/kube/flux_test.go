package kube

import "testing"

func TestParseFluxInventoryID(t *testing.T) {
	for _, tc := range []struct {
		id       string
		ns, name string
		kind     ResourceKind
		ok       bool
	}{
		{"gitlab_webservice_apps_Deployment", "gitlab", "webservice", "Deployment", true},
		{"_gitlab__Namespace", "", "gitlab", "Namespace", true},
		{"gitlab__apps_Deployment", "", "", "", false},
		{"not-an-id", "", "", "", false},
	} {
		ns, name, kind, ok := ParseFluxInventoryID(tc.id)
		if ok != tc.ok || (ok && (ns != tc.ns || name != tc.name || kind != tc.kind)) {
			t.Errorf("%q = (%q, %q, %q, %v), want (%q, %q, %q, %v)", tc.id, ns, name, kind, ok, tc.ns, tc.name, tc.kind, tc.ok)
		}
	}
}

func TestFluxManagerPrefersTheHelmRelease(t *testing.T) {
	kind, ns, name, ok := FluxManager(map[string]string{
		FluxGroupKustomize + "/name": "apps", FluxGroupKustomize + "/namespace": "flux-system",
		FluxGroupHelm + "/name": "gitlab", FluxGroupHelm + "/namespace": "gitlab",
	})
	if !ok || kind != KindFluxHelmRelease || ns != "gitlab" || name != "gitlab" {
		t.Errorf("FluxManager = (%q, %q, %q, %v), want the HelmRelease", kind, ns, name, ok)
	}
	if _, _, _, ok := FluxManager(map[string]string{"app": "x"}); ok {
		t.Error("an unlabelled object must not resolve to a reconciler")
	}
}
