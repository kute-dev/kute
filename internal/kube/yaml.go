package kube

import (
	"context"
	"fmt"
	"strings"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
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

// GetManagedFields fetches an object's metadata.managedFields directly from
// the API server and renders it as YAML.
//
// It cannot come from the informer cache: managedFields is stripped on the
// way in (transform.go), because it is routinely a third of an object's
// bytes and 8a's YAML view is the only thing in the app that ever displays
// it. So the one screen that wants it pays one Get for the one object it is
// showing, rather than every cache carrying it for every object forever.
//
// Goes through the dynamic client so it works for any kind, discovered CRDs
// included, without a typed switch.
func (c *Cluster) GetManagedFields(ctx context.Context, kind ResourceKind, namespace, name string) (string, error) {
	gvr, clusterScoped, ok := c.resourceFor(kind)
	if !ok {
		return "", fmt.Errorf("no API resource known for kind %s", kind)
	}
	c.mu.Lock()
	dyn := c.dynClient
	c.mu.Unlock()
	if dyn == nil {
		return "", fmt.Errorf("no dynamic client")
	}

	var ri dynamic.ResourceInterface = dyn.Resource(gvr)
	if !clusterScoped && namespace != "" {
		ri = dyn.Resource(gvr).Namespace(namespace)
	}
	obj, err := ri.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	return ManagedFieldsYAML(obj)
}
