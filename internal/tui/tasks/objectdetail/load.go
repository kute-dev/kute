package objectdetail

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
)

// load fetches the object itself (found=false, not an error, when it's no
// longer in the cache — a watch delete), projects it through the kind's own
// Descriptor for the meta grid, extracts its conditions generically, and
// best-effort fetches its events — mirrors poddetail's load() shape.
func (m Model) load() tea.Cmd {
	lister := m.lister
	events := m.events
	kind := m.kind
	namespace := m.namespace
	name := m.name
	desc := m.desc
	timeout := m.timeout
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		objs, err := lister.ListRaw(ctx, kind, namespace)
		if err != nil {
			return loadedMsg{err: err}
		}
		obj := findObject(objs, name)
		if obj == nil {
			return loadedMsg{found: false}
		}
		row := desc.Project(obj)
		conditions := extractConditions(obj)

		var eventRows []kube.Event
		var eventsErr error
		if events != nil {
			eventRows, eventsErr = events.ObjectEvents(ctx, namespace, kind, name)
		}

		return loadedMsg{obj: obj, row: row, conditions: conditions, found: true, events: eventRows, eventsErr: eventsErr}
	}
}

func findObject(objs []runtime.Object, name string) *unstructured.Unstructured {
	for _, obj := range objs {
		u, ok := obj.(*unstructured.Unstructured)
		if ok && u.GetName() == name {
			return u
		}
	}
	return nil
}

// extractConditions reads status.conditions off any object's unstructured
// form — the one generic read every discovered kind's detail view shares,
// no per-CRD parsing.
func extractConditions(u *unstructured.Unstructured) []condition {
	raw, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
	out := make([]condition, 0, len(raw))
	for _, c := range raw {
		cm, ok := c.(map[string]any)
		if !ok {
			continue
		}
		typ, _, _ := unstructured.NestedString(cm, "type")
		status, _, _ := unstructured.NestedString(cm, "status")
		message, _, _ := unstructured.NestedString(cm, "message")
		reason, _, _ := unstructured.NestedString(cm, "reason")
		ltStr, _, _ := unstructured.NestedString(cm, "lastTransitionTime")
		lt, _ := time.Parse(time.RFC3339, ltStr)
		out = append(out, condition{Type: typ, Status: status, Message: message, Reason: reason, LastTransition: lt})
	}
	return out
}

// primaryCondition returns the first Ready/Available-style condition — the
// title row's status word (14d) and the same signal
// resources.projectCustomResource already used to color the row.
func primaryCondition(conds []condition) (condition, bool) {
	for _, c := range conds {
		if c.Type == "Ready" || c.Type == "Available" {
			return c, true
		}
	}
	return condition{}, false
}

func statusWord(c condition) string {
	switch c.Status {
	case "True":
		return c.Type
	case "False":
		return "Not " + c.Type
	default:
		return c.Type + " unknown"
	}
}
