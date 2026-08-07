package kube

import (
	"testing"
	"time"
)

// TestTimelineFromArgoEventsFiltersByReason pins §34a's core mechanism: only
// an OperationCompleted event becomes a sync row, and statusOf resolves the
// fields the Event itself doesn't carry (unlike Flux's own reconcile
// events, which stamp the revision on the Event annotation directly).
func TestTimelineFromArgoEventsFiltersByReason(t *testing.T) {
	t.Parallel()
	now := time.Now()
	events := []Event{
		{Object: "Application/kute-billing", Namespace: "argocd", Reason: "OperationCompleted", LastSeen: now},
		// Not a sync-completed event: an ordinary event, not a sync row.
		{Object: "Application/kute-billing", Namespace: "argocd", Reason: "ResourceUpdated", LastSeen: now},
	}
	statusOf := func(object string) (ArgoSyncStatus, bool) {
		if object == "Application/kute-billing" {
			return ArgoSyncStatus{Ref: "main", Revision: "e41b90cdeadbeef0123456789abcdef012345678", By: "ci-bot"}, true
		}
		return ArgoSyncStatus{}, false
	}

	got := TimelineFromArgoEvents(events, statusOf)
	if len(got) != 1 {
		t.Fatalf("expected exactly one sync entry, got %d", len(got))
	}
	e := got[0]
	if e.Kind != TimelineSync {
		t.Errorf("kind = %v, want TimelineSync", e.Kind)
	}
	if e.GitRevision != "main@e41b90c" {
		t.Errorf("GitRevision = %q, want %q", e.GitRevision, "main@e41b90c")
	}
	if e.By != "ci-bot" {
		t.Errorf("By = %q, want %q", e.By, "ci-bot")
	}
	if e.CommitSubject != "" {
		t.Errorf("CommitSubject should never be set on a TimelineSync entry, got %q", e.CommitSubject)
	}
}

// TestTimelineFromArgoEventsSkipsUnresolvedStatus is the honest-degradation
// path: an OperationCompleted event whose Application statusOf can't
// resolve (already gone, or the informer isn't wired) is skipped rather
// than rendered with blank fields.
func TestTimelineFromArgoEventsSkipsUnresolvedStatus(t *testing.T) {
	t.Parallel()
	events := []Event{
		{Object: "Application/kute-billing", Reason: "OperationCompleted", LastSeen: time.Now()},
	}
	got := TimelineFromArgoEvents(events, func(string) (ArgoSyncStatus, bool) { return ArgoSyncStatus{}, false })
	if len(got) != 0 {
		t.Fatalf("expected no entries when statusOf can't resolve, got %d", len(got))
	}
}

// TestShortArgoRevision pins the "<ref>@<7-char-sha>" composition — Argo's
// own SHA carries no "sha1:"-style algorithm prefix, unlike Flux's digests.
func TestShortArgoRevision(t *testing.T) {
	t.Parallel()
	tests := []struct {
		ref, sha, want string
	}{
		{"main", "e41b90cdeadbeef0123456789abcdef012345678", "main@e41b90c"},
		{"", "e41b90cdeadbeef0123456789abcdef012345678", "HEAD@e41b90c"},
		{"main", "", "main"},
		{"", "", "HEAD"},
	}
	for _, tc := range tests {
		if got := ShortArgoRevision(tc.ref, tc.sha); got != tc.want {
			t.Errorf("ShortArgoRevision(%q, %q) = %q, want %q", tc.ref, tc.sha, got, tc.want)
		}
	}
}
