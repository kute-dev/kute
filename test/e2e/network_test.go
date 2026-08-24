//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestConnectivityOutageKeepsCacheResponsiveAndRecovers(t *testing.T) {
	RequireCluster(t)
	a := Launch(t)
	a.WaitForAll(Connect, "api-", "worker-")
	proxy := a.Proxy()
	fence := proxy.Fence()
	outageAt := time.Now()
	proxy.SetAvailable(false)

	a.WaitFor("OFFLINE", Settle)
	a.WaitForAll(Settle, "api-", "worker-", "mutating actions disabled")

	// Input still traverses the real event loop while every API request is
	// failing, and a mutating shortcut cannot open its confirmation gate.
	a.Press("/")
	a.Type("api")
	a.WaitFor("/ api", Settle)
	a.Esc()
	a.Press("D")
	a.Never("CONFIRM", 750*time.Millisecond)

	marker := fmt.Sprintf("network-recovery-%d", time.Now().UnixNano())
	createDisposablePod(t, marker, map[string]string{"phase": "disconnected"})
	a.Never(marker, 500*time.Millisecond)

	livez := waitForProxyRequests(t, proxy, fence, RequestMatcher{Path: "/livez"}, 4, 25*time.Second)
	if livez[0].Started.Before(outageAt) {
		t.Fatalf("first fenced livez request predates outage: %s < %s", livez[0].Started, outageAt)
	}
	assertRetrySpacing(t, livez)

	proxy.SetAvailable(true)
	a.WaitFor(marker, Settle)
	a.WaitGone("OFFLINE", Settle)
	a.WaitFor("connected", Settle)
}

func waitForProxyRequests(t *testing.T, proxy *APIProxy, after uint64, matcher RequestMatcher, count int, timeout time.Duration) []RequestRecord {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var matches []RequestRecord
		for _, rec := range proxy.History() {
			if rec.ID > after && matcher.matches(rec) {
				matches = append(matches, rec)
			}
		}
		if len(matches) >= count {
			return matches[:count]
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("proxy saw fewer than %d requests after fence %d matching %+v", count, after, matcher)
	return nil
}

func assertRetrySpacing(t *testing.T, records []RequestRecord) {
	t.Helper()
	if len(records) < 4 {
		t.Fatalf("need four retry records, got %d", len(records))
	}
	// Depending on whether the first ping or the closed-watch callback wins
	// the outage race, the observed sequence begins 1s→2s→4s or 2s→4s→8s.
	// Either is exponential; both reject the old fixed 2s ticker.
	if delta := records[1].Started.Sub(records[0].Started); delta < 750*time.Millisecond {
		t.Errorf("first livez retry spacing = %s, want at least about 1s", delta)
	}
	if delta := records[2].Started.Sub(records[1].Started); delta < 1500*time.Millisecond {
		t.Errorf("second livez retry spacing = %s, want at least about 2s", delta)
	}
	if delta := records[3].Started.Sub(records[2].Started); delta < 3500*time.Millisecond {
		t.Errorf("third livez retry spacing = %s, want at least about 4s (not a fixed-frequency loop)", delta)
	}
}

func createDisposablePod(t *testing.T, name string, labels map[string]string) {
	createDisposablePodInNamespace(t, Namespace, name, labels)
}

func createDisposablePodInNamespace(t *testing.T, namespace, name string, labels map[string]string) {
	t.Helper()
	client := e2eClientset(t)
	ctx, cancel := context.WithTimeout(context.Background(), Settle)
	defer cancel()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: labels},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{{
				Name: "sleeper", Image: "busybox:1.37@sha256:9532d8c39891ca2ecde4d30d7710e01fb739c87a8b9299685c63704296b16028",
				Command: []string{"/bin/sh", "-c", "while true; do sleep 30; done"},
			}},
		},
	}
	if _, err := client.CoreV1().Pods(namespace).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		t.Fatalf("creating disposable pod %s: %v", name, err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		zero := int64(0)
		if err := client.CoreV1().Pods(namespace).Delete(cleanupCtx, name, metav1.DeleteOptions{GracePeriodSeconds: &zero}); err != nil && !strings.Contains(err.Error(), "not found") {
			t.Logf("cleanup pod %s: %v", name, err)
		}
	})
}
