package fake

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kute-dev/kute/internal/kube"
)

func TestPodResolverResolvesPodDirectly(t *testing.T) {
	t.Parallel()
	c := New("default", "test")
	r := NewPodResolver(c)
	pod, err := r.ResolveForwardPod(context.Background(), kube.ForwardTarget{Kind: kube.KindPod, Namespace: "default", Name: "web-1"})
	if err != nil || pod != "web-1" {
		t.Fatalf("ResolveForwardPod(pod) = %q, %v", pod, err)
	}
}

func TestPodResolverResolvesServiceSelector(t *testing.T) {
	t.Parallel()
	c := New("default", "test")
	c.Seed(kube.KindService, &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "default"},
		Spec:       corev1.ServiceSpec{Selector: map[string]string{"app": "web"}},
	})
	c.Seed(kube.KindPod,
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "web-1", Namespace: "default", Labels: map[string]string{"app": "other"}}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "web-2", Namespace: "default", Labels: map[string]string{"app": "web"}}},
	)

	r := NewPodResolver(c)
	pod, err := r.ResolveForwardPod(context.Background(), kube.ForwardTarget{Kind: kube.KindService, Namespace: "default", Name: "web"})
	if err != nil || pod != "web-2" {
		t.Fatalf("ResolveForwardPod(service) = %q, %v, want web-2", pod, err)
	}
}

func TestPodResolverResolvesDeploymentSelector(t *testing.T) {
	t.Parallel()
	c := New("default", "test")
	c.Seed(kube.KindDeployment, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default"},
		Spec:       appsv1.DeploymentSpec{Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "api"}}},
	})
	c.Seed(kube.KindPod,
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "api-1", Namespace: "default", Labels: map[string]string{"app": "api"}}},
	)

	r := NewPodResolver(c)
	pod, err := r.ResolveForwardPod(context.Background(), kube.ForwardTarget{Kind: kube.KindDeployment, Namespace: "default", Name: "api"})
	if err != nil || pod != "api-1" {
		t.Fatalf("ResolveForwardPod(deployment) = %q, %v, want api-1", pod, err)
	}
}

func TestPodResolverServiceNotFound(t *testing.T) {
	t.Parallel()
	c := New("default", "test")
	r := NewPodResolver(c)
	if _, err := r.ResolveForwardPod(context.Background(), kube.ForwardTarget{Kind: kube.KindService, Namespace: "default", Name: "missing"}); err == nil {
		t.Fatal("expected an error resolving a missing service")
	}
}

func TestForwardDialerBindsRealLocalPort(t *testing.T) {
	t.Parallel()
	// Occupy the port first so Dial's own bind fails deterministically.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	d := NewForwardDialer()
	if _, err := d.Dial("default", "web-1", port, 80); err == nil {
		t.Fatal("expected Dial to fail binding an already-occupied port")
	}
}

func TestForwardDialerTunnelAcceptsAndCloses(t *testing.T) {
	t.Parallel()
	// Grab a free ephemeral port number, then release it immediately so
	// the dialer can bind the same number itself.
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()

	d := NewForwardDialer()
	tunnel, err := d.Dial("default", "web-1", port, 80)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}

	activity := make(chan struct{}, 1)
	runErr := make(chan error, 1)
	go func() { runErr <- tunnel.Run(func() { activity <- struct{}{} }) }()

	conn, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatalf("net.Dial: %v", err)
	}
	conn.Close()

	select {
	case <-activity:
	case err := <-runErr:
		t.Fatalf("Run returned early: %v", err)
	}

	tunnel.Close()
	if err := <-runErr; err == nil {
		t.Fatal("expected Run to return an error once the listener closes")
	}
}

// TestForwardDialerFlagsFlakyPodDistinctly pins the 13c fix: Dial must
// return a *flakyTunnel only for flakyForwardPod, so every other demo pod's
// forward keeps behaving like a normal, stable tunnel.
func TestForwardDialerFlagsFlakyPodDistinctly(t *testing.T) {
	t.Parallel()
	d := NewForwardDialer()

	freePort := func(t *testing.T) int {
		t.Helper()
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("net.Listen: %v", err)
		}
		port := ln.Addr().(*net.TCPAddr).Port
		ln.Close()
		return port
	}

	stable, err := d.Dial("default", "web-1", freePort(t), 80)
	if err != nil {
		t.Fatalf("Dial(web-1): %v", err)
	}
	defer stable.Close()
	if _, ok := stable.(*flakyTunnel); ok {
		t.Fatal("expected a stable pod's tunnel not to be flaky")
	}

	flaky, err := d.Dial("default", flakyForwardPod, freePort(t), 80)
	if err != nil {
		t.Fatalf("Dial(%s): %v", flakyForwardPod, err)
	}
	defer flaky.Close()
	if _, ok := flaky.(*flakyTunnel); !ok {
		t.Fatalf("expected %s's tunnel to be flaky, got %T", flakyForwardPod, flaky)
	}
}

// TestFlakyTunnelRunFailsAfterTTL pins 13c/13d: a flaky tunnel's Run must
// eventually return an error on its own — the same signal ForwardManager.run
// reads from a real dropped connection to flip a session into Reconnecting
// with a backoff — rather than blocking forever like a stable tunnel.
func TestFlakyTunnelRunFailsAfterTTL(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	ft := &flakyTunnel{tunnel: &tunnel{ln: ln}, ttl: 20 * time.Millisecond}

	errCh := make(chan error, 1)
	go func() { errCh <- ft.Run(nil) }()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected Run to return a non-nil error once the TTL elapses")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected Run to return within the TTL, timed out waiting")
	}
}
