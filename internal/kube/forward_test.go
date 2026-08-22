package kube

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

func TestForwardablePortsPod(t *testing.T) {
	t.Parallel()
	pod := &corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{
		{Name: "web", Ports: []corev1.ContainerPort{{ContainerPort: 8080, Name: "http"}}},
	}}}
	ports := ForwardablePorts(pod)
	if len(ports) != 1 || ports[0].Port != 8080 || ports[0].Container != "web" {
		t.Fatalf("ForwardablePorts(pod) = %+v", ports)
	}
}

func TestForwardablePortsService(t *testing.T) {
	t.Parallel()
	svc := &corev1.Service{Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: 80, Name: "http"}}}}
	ports := ForwardablePorts(svc)
	if len(ports) != 1 || ports[0].Port != 80 || ports[0].Container != "" {
		t.Fatalf("ForwardablePorts(service) = %+v", ports)
	}
}

func TestForwardablePortsDeployment(t *testing.T) {
	t.Parallel()
	dep := &appsv1.Deployment{Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
		Containers: []corev1.Container{{Name: "web", Ports: []corev1.ContainerPort{{ContainerPort: 9090}}}},
	}}}}
	ports := ForwardablePorts(dep)
	if len(ports) != 1 || ports[0].Port != 9090 {
		t.Fatalf("ForwardablePorts(deployment) = %+v", ports)
	}
}

func TestForwardablePortsUnsupportedType(t *testing.T) {
	t.Parallel()
	if ports := ForwardablePorts(&corev1.ConfigMap{}); ports != nil {
		t.Fatalf("ForwardablePorts(configmap) = %+v, want nil", ports)
	}
}

func TestPortForwardCommandString(t *testing.T) {
	t.Parallel()
	got := PortForwardCommandString("nva-stage", "web-abc123", 8080, 80)
	want := "kubectl port-forward pod/web-abc123 8080:80 -n nva-stage"
	if got != want {
		t.Fatalf("PortForwardCommandString = %q, want %q", got, want)
	}
}

// stubTunnel is a controllable Tunnel for exercising ForwardManager's
// reconnect loop without a real network dial.
type stubTunnel struct {
	mu     sync.Mutex
	closed chan struct{}
	err    error // returned once Close fires or immediately if failFast
}

func newStubTunnel(err error) *stubTunnel {
	return &stubTunnel{closed: make(chan struct{}), err: err}
}

func (s *stubTunnel) Run(activity func()) error {
	if activity != nil {
		activity()
	}
	<-s.closed
	return s.err
}

func (s *stubTunnel) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case <-s.closed:
	default:
		close(s.closed)
	}
}

// stubDialer dials successfully exactly once (returning tunnel), and fails
// with dialErr on every subsequent attempt — enough to drive a session from
// Active into Reconnecting deterministically.
type stubDialer struct {
	mu      sync.Mutex
	tunnel  *stubTunnel
	dials   int
	dialErr error
}

func (d *stubDialer) Dial(_ string, _ string, _ int, _ int32) (Tunnel, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.dials++
	if d.dials == 1 {
		return d.tunnel, nil
	}
	return nil, d.dialErr
}

func (d *stubDialer) Dials() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.dials
}

type stubResolver struct{ pod string }

func (r stubResolver) ResolveForwardPod(_ context.Context, _ ForwardTarget) (string, error) {
	return r.pod, nil
}

func awaitState(t *testing.T, mgr *ForwardManager, id string, want ForwardState) ForwardSession {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, s := range mgr.List() {
			if s.ID == id && s.State == want {
				return s
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("session %s never reached state %s; sessions=%+v", id, want, mgr.List())
	return ForwardSession{}
}

func TestForwardManagerStartListStop(t *testing.T) {
	t.Parallel()
	mgr := NewForwardManager()
	tunnel := newStubTunnel(nil)
	dialer := &stubDialer{tunnel: tunnel}
	target := ForwardTarget{Kind: KindPod, Namespace: "default", Name: "web"}

	session := mgr.Start(dialer, stubResolver{pod: "web"}, target, "web", 8080, 80, "http")
	if session.State != ForwardActive {
		t.Fatalf("Start() initial state = %s, want Active", session.State)
	}
	got := awaitState(t, mgr, session.ID, ForwardActive)
	if got.ResolvedPod != "web" {
		t.Fatalf("ResolvedPod = %q, want %q", got.ResolvedPod, "web")
	}

	mgr.Stop(session.ID)
	if list := mgr.List(); len(list) != 0 {
		t.Fatalf("List() after Stop = %+v, want empty", list)
	}
}

func TestForwardManagerReconnectsOnLostTunnel(t *testing.T) {
	t.Parallel()
	mgr := NewForwardManager()
	tunnel := newStubTunnel(errors.New("lost connection to pod"))
	dialer := &stubDialer{tunnel: tunnel, dialErr: errors.New("dial refused")}
	target := ForwardTarget{Kind: KindPod, Namespace: "default", Name: "web"}

	session := mgr.Start(dialer, stubResolver{pod: "web"}, target, "web", 8080, 80, "")
	awaitState(t, mgr, session.ID, ForwardActive)

	tunnel.Close() // simulate the tunnel dropping
	got := awaitState(t, mgr, session.ID, ForwardReconnecting)
	if got.Attempt < 1 {
		t.Fatalf("Attempt = %d, want >= 1 once reconnecting", got.Attempt)
	}
	if !strings.Contains(got.Err, "lost connection") {
		t.Fatalf("Err = %q, want it to mention the lost tunnel", got.Err)
	}

	mgr.Stop(session.ID)
}

func TestForwardManagerStopAll(t *testing.T) {
	t.Parallel()
	mgr := NewForwardManager()
	for i := range 3 {
		dialer := &stubDialer{tunnel: newStubTunnel(nil)}
		target := ForwardTarget{Kind: KindPod, Namespace: "default", Name: fmt.Sprintf("web-%d", i)}
		s := mgr.Start(dialer, stubResolver{pod: target.Name}, target, target.Name, 8080+i, 80, "")
		awaitState(t, mgr, s.ID, ForwardActive)
	}
	mgr.StopAll()
	if list := mgr.List(); len(list) != 0 {
		t.Fatalf("List() after StopAll = %+v, want empty", list)
	}
}

func TestForwardManagerListRawWrapsSessions(t *testing.T) {
	t.Parallel()
	mgr := NewForwardManager()
	dialer := &stubDialer{tunnel: newStubTunnel(nil)}
	target := ForwardTarget{Kind: KindPod, Namespace: "default", Name: "web"}
	s := mgr.Start(dialer, stubResolver{pod: "web"}, target, "web", 8080, 80, "")
	awaitState(t, mgr, s.ID, ForwardActive)

	objs := mgr.ListRaw()
	if len(objs) != 1 {
		t.Fatalf("ListRaw() = %d objects, want 1", len(objs))
	}
	fo, ok := objs[0].(*ForwardObject)
	if !ok {
		t.Fatalf("ListRaw()[0] type = %T, want *ForwardObject", objs[0])
	}
	if fo.Session.ID != s.ID {
		t.Fatalf("wrapped session ID = %q, want %q", fo.Session.ID, s.ID)
	}
	if cp, ok := fo.DeepCopyObject().(*ForwardObject); !ok || cp.Session.ID != s.ID {
		t.Fatalf("DeepCopyObject() = %+v", cp)
	}
	mgr.Stop(s.ID)
}

func TestForwardBackoffCapped(t *testing.T) {
	t.Parallel()
	if d := forwardBackoff(1); d != 2*time.Second {
		t.Errorf("forwardBackoff(1) = %s, want 2s", d)
	}
	if d := forwardBackoff(100); d != 30*time.Second {
		t.Errorf("forwardBackoff(100) = %s, want capped at 30s", d)
	}
}

// gatedDialer blocks its first Dial on gate, then serves later dials from
// tunnels in order. It exists to hold one run() goroutine inside Dial while a
// Restart replaces it.
type gatedDialer struct {
	gate    chan struct{}
	entered chan struct{} // closed once the first Dial is actually parked
	mu      sync.Mutex
	dials   int
	tunnels []*stubTunnel
}

func (d *gatedDialer) Dial(_ string, _ string, _ int, _ int32) (Tunnel, error) {
	d.mu.Lock()
	n := d.dials
	d.dials++
	d.mu.Unlock()
	if n == 0 {
		close(d.entered)
		<-d.gate
	}
	if n < len(d.tunnels) {
		return d.tunnels[n], nil
	}
	return nil, errors.New("no tunnel left")
}

// tunnelFor reads the live tunnel handle a session is holding.
func (m *ForwardManager) tunnelFor(id string) Tunnel {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.sessions[id]; ok {
		return e.tunnel
	}
	return nil
}

func (s *stubTunnel) isClosed() bool {
	select {
	case <-s.closed:
		return true
	default:
		return false
	}
}

// TestForwardManagerRestartDoesNotOrphanTheReplacedTunnel pins the window
// between Dial returning and the tunnel handle being published.
//
// run() checks ctx.Err() before dialing and after Run() returns, but not
// between Dial and the write. A Restart landing inside that gap left the
// doomed goroutine to publish its stale tunnel over the replacement's, so the
// live tunnel became unreachable and was never closed — a leaked SPDY
// connection and a leaked local listening port per restart.
func TestForwardManagerRestartDoesNotOrphanTheReplacedTunnel(t *testing.T) {
	t.Parallel()
	stale, live := newStubTunnel(nil), newStubTunnel(nil)
	dialer := &gatedDialer{
		gate:    make(chan struct{}),
		entered: make(chan struct{}),
		tunnels: []*stubTunnel{stale, live},
	}
	mgr := NewForwardManager()
	target := ForwardTarget{Kind: KindPod, Namespace: "default", Name: "web"}

	session := mgr.Start(dialer, stubResolver{pod: "web"}, target, "web", 8080, 80, "")

	// Wait until the first goroutine is genuinely parked inside Dial before
	// replacing it — otherwise the replacement can be the one that gets the
	// gated dial and the roles swap.
	<-dialer.entered
	mgr.Restart(session.ID)

	// Wait for the replacement to publish its own tunnel before letting the
	// doomed one out of Dial — that ordering is the whole point of the test.
	deadline := time.Now().Add(2 * time.Second)
	for mgr.tunnelFor(session.ID) != Tunnel(live) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := mgr.tunnelFor(session.ID); got != Tunnel(live) {
		t.Fatalf("replacement never published its tunnel; holding %v", got)
	}

	close(dialer.gate)

	// The doomed goroutine must close what it dialled and leave the live
	// handle alone.
	for !stale.isClosed() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !stale.isClosed() {
		t.Error("the tunnel dialled by the replaced goroutine was never closed")
	}
	if got := mgr.tunnelFor(session.ID); got != Tunnel(live) {
		t.Errorf("session is holding %v, want the live tunnel — the stale one stomped it", got)
	}
	if live.isClosed() {
		t.Error("the live tunnel was closed")
	}

	mgr.Stop(session.ID)
}
