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
	// events.k8s.io/v1-native events (emitted by kube-scheduler and most
	// controllers on modern Kubernetes) never populate the legacy
	// LastTimestamp/Count fields at all — repeats instead update Series,
	// leaving LastTimestamp/Count at their zero value for the object's
	// entire lifetime. Without this, "last" above falls back to EventTime
	// (the *first* occurrence), which can be hours old for a long-running
	// series — silently expelling an actively-recurring warning from every
	// time-windowed view (9b's own "last hour" default, poddetail/
	// nodedetail's EVENTS grid, the 16a/16b timeline) even though it's
	// still happening right now. Series.LastObservedTime/Count are the
	// authoritative "last seen"/"how many times" for these events, so they
	// take priority over whatever the legacy-field fallback above computed.
	if ev.Series != nil {
		if !ev.Series.LastObservedTime.IsZero() {
			last = ev.Series.LastObservedTime.Time
		}
		if ev.Series.Count > 0 {
			count = ev.Series.Count
		}
	}
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

// DedupeEvents folds events sharing the same reason+object into one
// EventGroup per key (docs/design README.md's `Kute Spec.dc.html#9b`
// caption: "deduped by reason+object" — message is deliberately left out of
// the key, since retries of the same failure routinely generate a fresh
// message each attempt, e.g. a ReplicaSet's FailedCreate quota rejection
// naming a newly-generated pod suffix every time; keying on message too
// would silently defeat the dedupe for exactly the bursty-retry case 9b
// exists to collapse), summing Count and keeping the latest LastSeen (and
// that occurrence's Type/Message, in case either changed across
// occurrences). Namespace is part of the key even though Object ("Kind/Name")
// doesn't carry it — 9b's all-namespaces mode (browse's 'e' with
// namespace == "", mirroring 6b) would otherwise fold together two
// same-named, same-kind objects with the same reason in different
// namespaces (e.g. "FailedScheduling" on "Pod/cache-0" in both
// shop-checkout and shop-payments) into one wrongly-merged row. Results are
// ordered newest-first; screens partition warnings-first or otherwise
// re-sort on top of this base order.
func DedupeEvents(events []Event) []EventGroup {
	type key struct{ reason, namespace, object string }
	groups := make(map[key]*EventGroup, len(events))
	order := make([]key, 0, len(events))

	for _, e := range events {
		k := key{e.Reason, e.Namespace, e.Object}
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
			g.Message = e.Message
		}
	}

	out := make([]EventGroup, 0, len(order))
	for _, k := range order {
		out = append(out, *groups[k])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastSeen.After(out[j].LastSeen) })
	return out
}
