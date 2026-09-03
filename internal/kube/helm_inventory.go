package kube

import (
	"slices"
	"strings"

	sigsyaml "sigs.k8s.io/yaml"
)

// HelmObjectRef identifies one object declared by a release manifest and
// retains its saved YAML for diagnosis even when Helm never created it.
type HelmObjectRef struct {
	APIVersion string
	Kind       string
	Namespace  string
	Name       string
	YAML       string
}

// HelmReleaseObjects returns every named object in the ordinary release
// manifest. Hooks are deliberately separate in HelmRelease.Hooks.
func HelmReleaseObjects(r HelmRelease) []HelmObjectRef {
	return slices.Collect(helmObjectRefs(r.Manifest, r.Namespace))
}

// ObjectRef returns the top-level object declared by this hook's manifest.
func (h HelmHook) ObjectRef(defaultNamespace string) (HelmObjectRef, bool) {
	for ref := range helmObjectRefs(h.Manifest, defaultNamespace) {
		return ref, true
	}
	return HelmObjectRef{}, false
}

func helmObjectRefs(manifest, defaultNamespace string) func(func(HelmObjectRef) bool) {
	return func(yield func(HelmObjectRef) bool) {
		for doc := range splitYAMLDocuments(manifest) {
			if strings.TrimSpace(doc) == "" {
				continue
			}
			var meta struct {
				APIVersion string `json:"apiVersion"`
				Kind       string `json:"kind"`
				Metadata   struct {
					Name      string `json:"name"`
					Namespace string `json:"namespace"`
				} `json:"metadata"`
			}
			if err := sigsyaml.Unmarshal([]byte(doc), &meta); err != nil || meta.Kind == "" || meta.Metadata.Name == "" {
				continue
			}
			namespace := meta.Metadata.Namespace
			if namespace == "" {
				namespace = defaultNamespace
			}
			if !yield(HelmObjectRef{
				APIVersion: meta.APIVersion,
				Kind:       meta.Kind,
				Namespace:  namespace,
				Name:       meta.Metadata.Name,
				YAML:       strings.TrimSpace(doc) + "\n",
			}) {
				return
			}
		}
	}
}
