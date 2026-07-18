package kube

import (
	"context"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
)

// Event is the projected view of a corev1.Event this app renders (9a/9b).
type Event struct {
	Type      string // "Normal" or "Warning"
	Reason    string
	Message   string
	Object    string // "Kind/Name", e.g. "Pod/nva-worker-9k2ss"
	Namespace string
	Count     int32
	FirstSeen time.Time
	LastSeen  time.Time
}

// EventGroup is one deduped row for the events screen (9b): every
// underlying Event object sharing the same reason+object+message folded
// into one row, with the summed count and most recent timestamp.
type EventGroup struct {
	Type      string
	Reason    string
	Message   string
	Object    string
	Namespace string
	Count     int32
	LastSeen  time.Time
}

// ObjectEvents returns every cached Event whose involvedObject matches
// kind/name in namespace, newest first.
func (c *Cluster) ObjectEvents(ctx context.Context, namespace string, kind ResourceKind, name string) ([]Event, error) {
	objs, err := c.ListRaw(ctx, KindEvent, namespace)
	if err != nil {
		return nil, err
	}
	events := make([]*corev1.Event, 0, len(objs))
	for _, obj := range objs {
		if ev, ok := obj.(*corev1.Event); ok {
			events = append(events, ev)
		}
	}
	return filterEventsByInvolvedObject(events, string(kind), name), nil
}

// NamespaceEvents returns every cached Event in namespace ("" = every
// namespace, mirroring ListRaw), newest first — 9b's namespace-scoped view
// (browse's 'e'), unlike ObjectEvents unfiltered by involvedObject.
func (c *Cluster) NamespaceEvents(ctx context.Context, namespace string) ([]Event, error) {
	objs, err := c.ListRaw(ctx, KindEvent, namespace)
	if err != nil {
		return nil, err
	}
	out := make([]Event, 0, len(objs))
	for _, obj := range objs {
		if ev, ok := obj.(*corev1.Event); ok {
			out = append(out, eventFromObject(ev))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastSeen.After(out[j].LastSeen) })
	return out, nil
}

// filterEventsByInvolvedObject projects and filters raw Events by
// involvedObject kind/name, newest first. Factored out of ObjectEvents so
// the filter/projection logic is unit-testable without an informer.
func filterEventsByInvolvedObject(events []*corev1.Event, kind, name string) []Event {
	out := make([]Event, 0, len(events))
	for _, ev := range events {
		if ev.InvolvedObject.Kind != kind || ev.InvolvedObject.Name != name {
			continue
		}
		out = append(out, eventFromObject(ev))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastSeen.After(out[j].LastSeen) })
	return out
}

func eventFromObject(ev *corev1.Event) Event {
	last := ev.LastTimestamp.Time
	if last.IsZero() {
		last = ev.EventTime.Time
	}
	if last.IsZero() {
		last = ev.CreationTimestamp.Time
	}
	first := ev.FirstTimestamp.Time
	if first.IsZero() {
		first = last
	}
	count := ev.Count
	if count == 0 {
		count = 1
	}
	return Event{
		Type:      ev.Type,
		Reason:    ev.Reason,
		Message:   ev.Message,
		Object:    ev.InvolvedObject.Kind + "/" + ev.InvolvedObject.Name,
		Namespace: ev.Namespace,
		Count:     count,
		FirstSeen: first,
		LastSeen:  last,
	}
}

// DedupeEvents folds events sharing the same reason+object+message into one
// EventGroup per key, summing Count and keeping the latest LastSeen (and
// that occurrence's Type, in case it changed across occurrences). Results
// are ordered newest-first; screens partition warnings-first or otherwise
// re-sort on top of this base order.
func DedupeEvents(events []Event) []EventGroup {
	type key struct{ reason, object, message string }
	groups := make(map[key]*EventGroup, len(events))
	order := make([]key, 0, len(events))

	for _, e := range events {
		k := key{e.Reason, e.Object, e.Message}
		g, ok := groups[k]
		if !ok {
			g = &EventGroup{Reason: e.Reason, Message: e.Message, Object: e.Object, Namespace: e.Namespace}
			groups[k] = g
			order = append(order, k)
		}
		g.Count += e.Count
		if e.LastSeen.After(g.LastSeen) {
			g.LastSeen = e.LastSeen
			g.Type = e.Type
		}
	}

	out := make([]EventGroup, 0, len(order))
	for _, k := range order {
		out = append(out, *groups[k])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastSeen.After(out[j].LastSeen) })
	return out
}
