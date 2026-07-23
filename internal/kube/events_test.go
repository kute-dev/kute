package kube

import (
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func mkEvent(reason, object, message string, count int32, lastSeen time.Time, typ string) Event {
	return Event{Type: typ, Reason: reason, Object: object, Message: message, Count: count, LastSeen: lastSeen}
}

func TestDedupeEventsFoldsSameKey(t *testing.T) {
	t.Parallel()
	t0 := time.Now().Add(-10 * time.Minute)
	t1 := time.Now().Add(-1 * time.Minute)
	events := []Event{
		mkEvent("BackOff", "Pod/api", "back-off restarting failed container", 3, t0, "Warning"),
		mkEvent("BackOff", "Pod/api", "back-off restarting failed container", 2, t1, "Warning"),
		mkEvent("Scheduled", "Pod/api", "Successfully assigned default/api to node-1", 1, t0, "Normal"),
	}
	groups := DedupeEvents(events)
	if len(groups) != 2 {
		t.Fatalf("got %d groups, want 2, groups=%+v", len(groups), groups)
	}

	var backoff *EventGroup
	for i := range groups {
		if groups[i].Reason == "BackOff" {
			backoff = &groups[i]
		}
	}
	if backoff == nil {
		t.Fatalf("expected a BackOff group")
	}
	if backoff.Count != 5 {
		t.Fatalf("BackOff count = %d, want 5 (summed)", backoff.Count)
	}
	if !backoff.LastSeen.Equal(t1) {
		t.Fatalf("BackOff LastSeen = %v, want the most recent %v", backoff.LastSeen, t1)
	}
}

func TestDedupeEventsOrdersNewestFirst(t *testing.T) {
	t.Parallel()
	older := time.Now().Add(-time.Hour)
	newer := time.Now()
	events := []Event{
		mkEvent("Old", "Pod/a", "old message", 1, older, "Normal"),
		mkEvent("New", "Pod/b", "new message", 1, newer, "Warning"),
	}
	groups := DedupeEvents(events)
	if len(groups) != 2 || groups[0].Reason != "New" {
		t.Fatalf("expected New first (newest), got %+v", groups)
	}
}

// TestDedupeEventsFoldsDespiteDifferingMessage is the real-world case a live
// cluster surfaced: a ReplicaSet's FailedCreate quota rejection, retried a
// few minutes apart, each attempt naming a freshly-generated pod suffix in
// its message ("...pods \"checkout-api-bc9cbfdc-r5z84\" is forbidden..." vs
// "...-t7pr9..."). Same reason+object, different message — these must still
// collapse into one ×2 group (docs/design README.md's `Kute Spec.dc.html
// #9b` caption: "deduped by reason+object"), keeping the most recent
// message rather than the message becoming part of the dedupe key.
func TestDedupeEventsFoldsDespiteDifferingMessage(t *testing.T) {
	t.Parallel()
	older := time.Now().Add(-16 * time.Minute)
	newer := time.Now()
	events := []Event{
		mkEvent("FailedCreate", "ReplicaSet/checkout-api-bc9cbfdc",
			`Error creating: pods "checkout-api-bc9cbfdc-r5z84" is forbidden: exceeded quota: compute-quota, requested: requests.memory=21Gi, used: requests.memory=31488Mi, limited: requests.memory=48Gi`,
			1, older, "Warning"),
		mkEvent("FailedCreate", "ReplicaSet/checkout-api-bc9cbfdc",
			`Error creating: pods "checkout-api-bc9cbfdc-t7pr9" is forbidden: exceeded quota: compute-quota, requested: requests.memory=21Gi, used: requests.memory=31488Mi, limited: requests.memory=48Gi`,
			1, newer, "Warning"),
	}
	groups := DedupeEvents(events)
	if len(groups) != 1 {
		t.Fatalf("got %d groups, want 1 (same reason+object should fold despite differing message), groups=%+v", len(groups), groups)
	}
	if groups[0].Count != 2 {
		t.Fatalf("Count = %d, want 2 (summed)", groups[0].Count)
	}
	if !groups[0].LastSeen.Equal(newer) {
		t.Fatalf("LastSeen = %v, want the most recent %v", groups[0].LastSeen, newer)
	}
	if !strings.Contains(groups[0].Message, "t7pr9") {
		t.Fatalf("Message = %q, want the most recent occurrence's message (t7pr9)", groups[0].Message)
	}
}

// TestDedupeEventsSameReasonAndObjectDifferentNamespaceStaySeparate covers
// 9b's all-namespaces mode (browse's 'e' with namespace == "", mirroring
// 6b): two different namespaces can each have their own identically-named
// object (e.g. "Pod/cache-0" in both shop-checkout and shop-payments)
// hitting the same reason independently — Namespace must be part of the
// dedupe key, or these would wrongly fold into one row.
func TestDedupeEventsSameReasonAndObjectDifferentNamespaceStaySeparate(t *testing.T) {
	t.Parallel()
	now := time.Now()
	events := []Event{
		{Type: "Warning", Reason: "FailedScheduling", Object: "Pod/cache-0", Namespace: "shop-checkout", Message: "0/5 nodes available", Count: 1, LastSeen: now},
		{Type: "Warning", Reason: "FailedScheduling", Object: "Pod/cache-0", Namespace: "shop-payments", Message: "0/5 nodes available", Count: 1, LastSeen: now},
	}
	groups := DedupeEvents(events)
	if len(groups) != 2 {
		t.Fatalf("got %d groups, want 2 (same reason+object but different namespaces), groups=%+v", len(groups), groups)
	}
}

func TestDedupeEventsDifferentObjectsSameReasonStaySeparate(t *testing.T) {
	t.Parallel()
	now := time.Now()
	events := []Event{
		mkEvent("Pulled", "Pod/a", "Container image pulled", 1, now, "Normal"),
		mkEvent("Pulled", "Pod/b", "Container image pulled", 1, now, "Normal"),
	}
	groups := DedupeEvents(events)
	if len(groups) != 2 {
		t.Fatalf("got %d groups, want 2 (different objects)", len(groups))
	}
}

func TestDedupeEventsEmpty(t *testing.T) {
	t.Parallel()
	if got := DedupeEvents(nil); len(got) != 0 {
		t.Fatalf("expected no groups for no events, got %d", len(got))
	}
}

func TestEventFromObjectFilterByInvolvedObject(t *testing.T) {
	t.Parallel()
	now := time.Now()
	matching := &corev1.Event{
		ObjectMeta:     metav1.ObjectMeta{Name: "ev-1", Namespace: "default"},
		InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "api"},
		LastTimestamp:  metav1.NewTime(now),
		Type:           "Warning",
		Reason:         "BackOff",
	}
	other := &corev1.Event{
		ObjectMeta:     metav1.ObjectMeta{Name: "ev-2", Namespace: "default"},
		InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "worker"},
		LastTimestamp:  metav1.NewTime(now),
	}
	got := filterEventsByInvolvedObject([]*corev1.Event{matching, other}, "Pod", "api")
	if len(got) != 1 || got[0].Object != "Pod/api" {
		t.Fatalf("expected only the matching event, got %+v", got)
	}
}

func TestEventFromObjectFallsBackThroughTimestamps(t *testing.T) {
	t.Parallel()
	created := time.Now().Add(-5 * time.Minute)
	ev := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{CreationTimestamp: metav1.NewTime(created)},
		InvolvedObject: corev1.ObjectReference{
			Kind: "Pod", Name: "api",
		},
		Reason:  "Scheduled",
		Type:    "Normal",
		Message: "assigned to node-1",
	}
	got := eventFromObject(ev)
	if got.Count != 1 {
		t.Fatalf("Count = %d, want 1 default", got.Count)
	}
	if !got.LastSeen.Equal(created) {
		t.Fatalf("LastSeen = %v, want fallback to CreationTimestamp %v", got.LastSeen, created)
	}
	if got.Object != "Pod/api" {
		t.Fatalf("Object = %q, want Pod/api", got.Object)
	}
}

// TestEventFromObjectPrefersSeriesOverEventTime is a live-cluster
// regression: events.k8s.io/v1-native events (kube-scheduler and most
// controllers on modern Kubernetes) never populate the legacy
// LastTimestamp/Count fields — repeats update Series instead, so
// LastTimestamp/Count stay at their zero value for the object's whole
// lifetime. EventTime only ever records the *first* occurrence. A pod that
// fails scheduling for hours has EventTime hours in the past while
// Series.LastObservedTime keeps advancing — without reading Series, 9b's
// "last hour" default window (and any other time-windowed view) would
// silently drop an actively-recurring warning as if it were stale.
func TestEventFromObjectPrefersSeriesOverEventTime(t *testing.T) {
	t.Parallel()
	firstOccurrence := time.Now().Add(-4 * time.Hour)
	lastObserved := time.Now().Add(-2 * time.Minute)
	ev := &corev1.Event{
		ObjectMeta:     metav1.ObjectMeta{CreationTimestamp: metav1.NewTime(firstOccurrence)},
		InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "llm-inference-74ccb94c66-msk8n"},
		Reason:         "FailedScheduling",
		Type:           "Warning",
		Message:        "0/9 nodes are available",
		EventTime:      metav1.NewMicroTime(firstOccurrence),
		Series:         &corev1.EventSeries{Count: 42, LastObservedTime: metav1.NewMicroTime(lastObserved)},
	}
	got := eventFromObject(ev)
	if !got.LastSeen.Equal(lastObserved) {
		t.Fatalf("LastSeen = %v, want Series.LastObservedTime %v (not EventTime's first-occurrence %v)", got.LastSeen, lastObserved, firstOccurrence)
	}
	if got.Count != 42 {
		t.Fatalf("Count = %d, want Series.Count 42", got.Count)
	}
}

// TestEventFromObjectSeriesNilKeepsLegacyFields covers a singleton event
// (no Series at all) still working exactly as before this fix.
func TestEventFromObjectSeriesNilKeepsLegacyFields(t *testing.T) {
	t.Parallel()
	last := time.Now().Add(-time.Minute)
	ev := &corev1.Event{
		InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "api"},
		Reason:         "Pulled",
		Type:           "Normal",
		LastTimestamp:  metav1.NewTime(last),
		Count:          3,
	}
	got := eventFromObject(ev)
	if !got.LastSeen.Equal(last) {
		t.Fatalf("LastSeen = %v, want LastTimestamp %v", got.LastSeen, last)
	}
	if got.Count != 3 {
		t.Fatalf("Count = %d, want the legacy Count field 3", got.Count)
	}
}
