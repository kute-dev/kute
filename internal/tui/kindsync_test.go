package tui

import (
	"errors"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/kute-dev/kute/internal/kube"
)

// fakeReporter is a lister that answers whichever of the two "why is this
// empty" questions the test wires up, and neither by default — the shape of
// every fake in the task packages, which implement none of these seams.
type fakeReporter struct {
	forbidden map[kube.ResourceKind]error
	stalled   map[kube.ResourceKind]error
}

type forbiddenOnly struct{ fakeReporter }

func (f forbiddenOnly) KindForbidden(kind kube.ResourceKind) error { return f.forbidden[kind] }

type stalledOnly struct{ fakeReporter }

func (f stalledOnly) KindError(kind kube.ResourceKind) error { return f.stalled[kind] }

type bothReporters struct{ fakeReporter }

func (f bothReporters) KindForbidden(kind kube.ResourceKind) error { return f.forbidden[kind] }
func (f bothReporters) KindError(kind kube.ResourceKind) error     { return f.stalled[kind] }

func forbiddenErr(resource string) error {
	return apierrors.NewForbidden(schema.GroupResource{Resource: resource}, "", errors.New("nope"))
}

// TestKindsErrorReportsADenial is the gap that let a forbidden kind render as
// an empty cluster: KindSynced reports a denied cache settled, KindError
// deliberately stays nil for it, and with no third answer a screen had
// "settled, no reason, no rows" — which reads as "the cluster has none".
func TestKindsErrorReportsADenial(t *testing.T) {
	t.Parallel()
	lister := forbiddenOnly{fakeReporter{forbidden: map[kube.ResourceKind]error{
		kube.KindSecret: forbiddenErr("secrets"),
	}}}

	err := KindsError(lister, kube.KindSecret)
	if err == nil {
		t.Fatal("KindsError = nil for a forbidden kind; the empty state has nothing to stop it")
	}
	if !kube.IsPermissionError(err) {
		t.Fatalf("KindsError = %v, want the denial itself so the screen can show 4b's card", err)
	}
}

// TestKindsErrorIgnoresKindsThatAreFine: a denial on one kind says nothing
// about another, so asking about a readable kind must stay nil.
func TestKindsErrorIgnoresKindsThatAreFine(t *testing.T) {
	t.Parallel()
	lister := forbiddenOnly{fakeReporter{forbidden: map[kube.ResourceKind]error{
		kube.KindSecret: forbiddenErr("secrets"),
	}}}

	if err := KindsError(lister, kube.KindPod); err != nil {
		t.Fatalf("KindsError(Pod) = %v, want nil — only Secret was refused", err)
	}
}

// TestKindsErrorPrefersADenialOverAStall: with one kind refused and another
// merely stalling, the permanent reason is the one worth showing — and which
// one comes back must not depend on the order the caller listed them in.
func TestKindsErrorPrefersADenialOverAStall(t *testing.T) {
	t.Parallel()
	lister := bothReporters{fakeReporter{
		forbidden: map[kube.ResourceKind]error{kube.KindSecret: forbiddenErr("secrets")},
		stalled:   map[kube.ResourceKind]error{kube.KindEvent: errors.New("context deadline exceeded")},
	}}

	for _, order := range [][]kube.ResourceKind{
		{kube.KindSecret, kube.KindEvent},
		{kube.KindEvent, kube.KindSecret},
	} {
		err := KindsError(lister, order...)
		if !kube.IsPermissionError(err) {
			t.Errorf("KindsError(%v) = %v, want the denial regardless of argument order", order, err)
		}
	}
}

// TestKindsErrorStillReportsAStall: adding the denial channel must not have
// taken the recoverable one away — a stalled initial LIST is still a reason
// an empty screen owes the user.
func TestKindsErrorStillReportsAStall(t *testing.T) {
	t.Parallel()
	lister := stalledOnly{fakeReporter{
		stalled: map[kube.ResourceKind]error{kube.KindEvent: errors.New("context deadline exceeded")},
	}}

	err := KindsError(lister, kube.KindEvent)
	if err == nil {
		t.Fatal("KindsError = nil for a stalled initial LIST")
	}
	if kube.IsPermissionError(err) {
		t.Fatalf("KindsError = %v, classified as a denial; it is retryable and must stay so", err)
	}
}

// TestKindsErrorNilForAListerWithNeitherSeam: every task package's own fake
// implements neither, and they must go on reporting "no reason" rather than
// panicking on a failed type assertion.
func TestKindsErrorNilForAListerWithNeitherSeam(t *testing.T) {
	t.Parallel()
	if err := KindsError(fakeReporter{}, kube.KindPod); err != nil {
		t.Fatalf("KindsError = %v for a lister implementing neither reporter, want nil", err)
	}
	if err := KindsError(nil, kube.KindPod); err != nil {
		t.Fatalf("KindsError(nil) = %v, want nil", err)
	}
}
