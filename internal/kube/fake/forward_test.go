package fake

import (
	"context"
	"net"
	"strconv"
	"testing"

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
