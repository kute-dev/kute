package kube

import (
	"context"
	"fmt"
	"strings"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	sigsyaml "sigs.k8s.io/yaml"
)

// GetYAML renders the named object of kind as YAML, read from the informer
// cache (ListRaw — no extra API call) rather than a fresh Get. Returns
// (yaml, resourceVersion, error). metadata.managedFields is cleared before
// marshaling — it's typically hundreds of noisy lines nobody reads raw;
// ManagedFieldsLineCount, called on the pre-clear object, is how a caller
// (yamlview, Phase 5) gets the count for its "▸ managedFields (N lines
// folded)" summary without paying to keep the stripped content around.
func (c *Cluster) GetYAML(ctx context.Context, kind ResourceKind, namespace, name string) (string, string, error) {
	objs, err := c.ListRaw(ctx, kind, namespace)
	if err != nil {
		return "", "", err
	}
	obj, err := findByName(objs, name)
	if err != nil {
		return "", "", err
	}

	copyObj := obj.DeepCopyObject()
	accessor, err := apimeta.Accessor(copyObj)
	if err != nil {
		return "", "", fmt.Errorf("get metadata accessor for %s/%s: %w", kind, name, err)
	}
	resourceVersion := accessor.GetResourceVersion()
	accessor.SetManagedFields(nil)

	data, err := sigsyaml.Marshal(copyObj)
	if err != nil {
		return "", "", fmt.Errorf("marshal %s/%s to yaml: %w", kind, name, err)
	}
	return string(data), resourceVersion, nil
}

// ManagedFieldsLineCount reports how many YAML lines obj's
// metadata.managedFields would occupy, for the fold summary. Call it before
// GetYAML clears the field.
func ManagedFieldsLineCount(obj runtime.Object) int {
	accessor, err := apimeta.Accessor(obj)
	if err != nil {
		return 0
	}
	fields := accessor.GetManagedFields()
	if len(fields) == 0 {
		return 0
	}
	data, err := sigsyaml.Marshal(fields)
	if err != nil {
		return 0
	}
	return strings.Count(strings.TrimRight(string(data), "\n"), "\n") + 1
}

// ManagedFieldsYAML renders obj's metadata.managedFields as its own YAML
// document — the field GetYAML clears before marshaling the rest of the
// object (so a typical object's YAML isn't dominated by hundreds of noisy
// lines nobody reads by default). A caller (yamlview's managedFields fold)
// uses this to show the real content once the user actually unfolds it,
// rather than only ever showing a placeholder count. Call it on the
// pre-clear object, same as ManagedFieldsLineCount.
func ManagedFieldsYAML(obj runtime.Object) (string, error) {
	accessor, err := apimeta.Accessor(obj)
	if err != nil {
		return "", err
	}
	fields := accessor.GetManagedFields()
	if len(fields) == 0 {
		return "", nil
	}
	data, err := sigsyaml.Marshal(fields)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func findByName(objs []runtime.Object, name string) (runtime.Object, error) {
	for _, obj := range objs {
		accessor, err := apimeta.Accessor(obj)
		if err != nil {
			continue
		}
		if accessor.GetName() == name {
			return obj, nil
		}
	}
	return nil, fmt.Errorf("%q not found in cache", name)
}
