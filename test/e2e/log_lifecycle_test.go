//go:build e2e

package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestLogStreamEndsWhenPodIsDeleted(t *testing.T) {
	RequireCluster(t)
	a := Launch(t)
	a.WaitFor("api-", Connect)
	pod, stream := openAPILogStream(t, a)
	a.WaitFor("KUTE-E2E-LOG-MARKER", Settle)

	ctx, cancel := context.WithTimeout(context.Background(), Settle)
	defer cancel()
	zero := int64(0)
	if err := e2eClientset(t).CoreV1().Pods(Namespace).Delete(ctx, pod, metav1.DeleteOptions{GracePeriodSeconds: &zero}); err != nil {
		t.Fatalf("deleting streamed pod: %v", err)
	}
	a.Proxy().WaitForCompletion(stream.ID, Settle)
	frame, ok := a.poll(func(f string) bool {
		lower := strings.ToLower(f)
		return strings.Contains(lower, "pod deleted") || strings.Contains(lower, "log stream closed") || strings.Contains(lower, "stream logs for")
	}, Settle)
	if !ok {
		t.Fatalf("deleted pod left logs in an indefinite restart wait:\n%s", frame)
	}
}

func TestEscAndQuitCancelFollowRequests(t *testing.T) {
	RequireCluster(t)
	a := Launch(t)
	a.WaitFor("api-", Connect)
	_, stream := openAPILogStream(t, a)
	a.Esc()
	if completed := a.Proxy().WaitForCompletion(stream.ID, 5*time.Second); !completed.Cancelled {
		t.Fatalf("esc did not cancel follow request: %+v", completed)
	}
	a.Esc()
	a.WaitFor("api-", Settle)
	a.Press("/")
	a.Esc() // clear the previous api- filter before the helper applies it again

	_, stream = openAPILogStream(t, a)
	a.Quit()
	if completed := a.Proxy().WaitForCompletion(stream.ID, 5*time.Second); !completed.Cancelled {
		t.Fatalf("quit did not cancel follow request: %+v", completed)
	}
}

func TestLogsRemainNavigableAcrossAPIDisconnect(t *testing.T) {
	RequireCluster(t)
	a := Launch(t)
	a.WaitFor("api-", Connect)
	_, _ = openAPILogStream(t, a)
	a.WaitFor("KUTE-E2E-LOG-MARKER", Settle)
	a.Proxy().SetAvailable(false)

	// A stream error is terminal, but the task must continue accepting input.
	a.Press("/")
	a.Type("KUTE-E2E")
	a.WaitFor("/ KUTE-E2E", Settle)
	a.Esc()
	a.Esc()
	a.WaitFor("api-", Settle)
	a.Proxy().SetAvailable(true)
	a.WaitGone("OFFLINE", Settle)
}

func openAPILogStream(t *testing.T, a *App) (string, RequestRecord) {
	t.Helper()
	pod := a.selectAPIPod(t)
	a.Enter()
	a.WaitFor("CONTAINERS", Settle)
	fence := a.Proxy().Fence()
	a.Press("l")
	stream := a.Proxy().WaitForRequest(fence, RequestMatcher{Resource: "pods", Verb: "STREAM"}, Settle)
	return pod, stream
}
