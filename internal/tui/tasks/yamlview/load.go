package yamlview

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
)

// load fetches the rendered YAML text via YAMLReader.GetYAML (which strips
// managedFields itself before marshaling) plus metadata.managedFields
// separately, for the fold this screen splices back in.
//
// managedFields comes from a live Get when the seam is wired, because
// informer caches strip it — see ManagedFieldsReader. Falling back to the
// cached object covers --demo and tests, where nothing strips anything.
// Either way it's best-effort: no managedFields means the fold has nothing
// to show, not that the object failed to load.
//
// Unlike poddetail's tolerant "gone" state, a missing object here is a real
// error — there's no useful degraded view of an object yamlview can't show
// at all.
func (m Model) load() tea.Cmd {
	lister := m.lister
	yamlReader := m.yaml
	managedFieldsReader := m.managedFields
	kind := m.kind
	namespace := m.namespace
	name := m.name
	timeout := m.timeout
	parent := m.session.ClusterContext()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, timeout)
		defer cancel()

		var managedFieldsYAML string
		if managedFieldsReader != nil {
			managedFieldsYAML, _ = managedFieldsReader.GetManagedFields(ctx, kind, namespace, name)
		} else {
			objs, err := lister.ListRaw(ctx, kind, namespace)
			if err != nil {
				return loadedMsg{err: err}
			}
			obj := findByName(objs, name)
			if obj == nil {
				return loadedMsg{err: fmt.Errorf("%s %q not found", kind, name)}
			}
			managedFieldsYAML, _ = kube.ManagedFieldsYAML(obj)
		}

		text, resourceVersion, err := yamlReader.GetYAML(ctx, kind, namespace, name)
		if err != nil {
			return loadedMsg{err: err}
		}

		return loadedMsg{text: text, resourceVersion: resourceVersion, managedFieldsYAML: managedFieldsYAML}
	}
}

func findByName(objs []runtime.Object, name string) runtime.Object {
	for _, obj := range objs {
		if accessor, err := apimeta.Accessor(obj); err == nil && accessor.GetName() == name {
			return obj
		}
	}
	return nil
}
