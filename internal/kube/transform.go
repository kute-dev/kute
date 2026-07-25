package kube

import (
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/tools/cache"
)

// stripManagedFields drops metadata.managedFields from every object before it
// enters an informer cache.
//
// managedFields is server-side-apply bookkeeping: one entry per controller
// that has ever touched the object, each listing the fields it owns. On a
// well-trodden object it is routinely a third to a half of the stored bytes,
// and kute reads none of it — every screen works from spec and status, and
// the one place it is displayed (8a's YAML view) already clears it before
// marshaling and fetches it separately.
//
// Safe with respect to writes because nothing here does read-modify-write
// through a cache: every mutation is a patch built from scratch and sent via
// the clientset (mutate.go), so a cached object is never a source for what
// gets sent back.
func stripManagedFields(obj any) (any, error) {
	// Tombstones arrive on delete when the watch missed the final state;
	// they wrap the real object and must be passed through unchanged.
	if _, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		return obj, nil
	}
	accessor, err := apimeta.Accessor(obj)
	if err != nil {
		// Not a Kubernetes object — leave it exactly as it came.
		//nolint:nilerr // a transform that can't understand an object must not drop it
		return obj, nil
	}
	if len(accessor.GetManagedFields()) == 0 {
		return obj, nil
	}
	accessor.SetManagedFields(nil)
	return obj, nil
}
