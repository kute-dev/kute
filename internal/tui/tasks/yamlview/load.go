package yamlview

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
)

// load fetches the raw object once (for kube.ManagedFieldsYAML, computed on
// the pre-clear object per kube.GetYAML's doc comment) and the rendered
// YAML text separately via YAMLReader.GetYAML, which strips managedFields
// itself before marshaling. Unlike poddetail's tolerant "gone" state, a
// missing object here is a real error — there's no useful degraded view of
// an object yamlview can't show at all.
func (m Model) load() tea.Cmd {
	lister := m.lister
	yamlReader := m.yaml
	kind := m.kind
	namespace := m.namespace
	name := m.name
	timeout := m.timeout
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		objs, err := lister.ListRaw(ctx, kind, namespace)
		if err != nil {
			return loadedMsg{err: err}
		}
		obj := findByName(objs, name)
		if obj == nil {
			return loadedMsg{err: fmt.Errorf("%s %q not found", kind, name)}
		}
		// Best-effort: a marshal failure just means the managedFields fold
		// has nothing to show, not that the whole object failed to load.
		managedFieldsYAML, _ := kube.ManagedFieldsYAML(obj)

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
