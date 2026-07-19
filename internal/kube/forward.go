// Package kube's port-forward support (13a/13c/13d, docs/design/README.md):
// ForwardManager runs forwarding sessions in-process (no kubectl
// subprocess) via a pluggable ForwardDialer/PodResolver pair, so the real
// cluster dials through client-go's SPDY upgrade (spdyDialer below) while
// kube/fake substitutes an in-memory stand-in for tests/--demo. Sessions are
// keyed by an opaque ID, independent of any *Cluster — a ForwardManager is
// constructed once at the composition root and never rebuilt on context
// switch, which is what makes forwards survive one (docs/design README.md
// §13d: "global across context switches").
package kube

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	// client-go's portforward.New/spdy.NewDialer (the pair this file uses) still
	// take apimachinery's httpstream.Dialer, not the k8s.io/streaming replacement
	// — that's only wired through the parallel NewForStreaming/NewDialerForStreaming
	// pair, which isn't interface-compatible.
	"k8s.io/apimachinery/pkg/util/httpstream" //nolint:staticcheck
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

// ForwardState is one session's current tunnel state.
type ForwardState string

const (
	ForwardActive       ForwardState = "active"
	ForwardReconnecting ForwardState = "reconnecting"
)

// ForwardTarget names what a forward points at: a specific Pod, or a
// Service/Deployment resolved to one of its backing pods at dial time
// (docs/design README.md §13c: "svc/deploy targets show the resolved
// backing pod").
type ForwardTarget struct {
	Kind      ResourceKind
	Namespace string
	Name      string
}

// PortOption is one candidate port offered by 13a's picker, discovered from
// the target object itself.
type PortOption struct {
	Port      int32
	Name      string
	Protocol  string
	Container string // empty for Service ports, which aren't container-scoped
}

// ForwardablePorts lists obj's candidate ports for the 13a picker: a Pod or
// Deployment's container ports, or a Service's declared ports. Any other
// type (or a nil/unexpected object) yields no ports.
func ForwardablePorts(obj runtime.Object) []PortOption {
	switch o := obj.(type) {
	case *corev1.Pod:
		return containerPortOptions(o.Spec.Containers)
	case *corev1.Service:
		out := make([]PortOption, 0, len(o.Spec.Ports))
		for _, p := range o.Spec.Ports {
			out = append(out, PortOption{Port: p.Port, Name: p.Name, Protocol: string(p.Protocol)})
		}
		return out
	case *appsv1.Deployment:
		return containerPortOptions(o.Spec.Template.Spec.Containers)
	default:
		return nil
	}
}

func containerPortOptions(containers []corev1.Container) []PortOption {
	var out []PortOption
	for _, c := range containers {
		for _, p := range c.Ports {
			out = append(out, PortOption{Port: p.ContainerPort, Name: p.Name, Protocol: string(p.Protocol), Container: c.Name})
		}
	}
	return out
}

// ForwardSession is one live/attempted port-forward, the row 13c's Forwards
// list renders.
type ForwardSession struct {
	ID             string
	Target         ForwardTarget
	ResolvedPod    string
	LocalPort      int
	RemotePort     int32
	RemotePortName string
	State          ForwardState
	Err            string
	Attempt        int
	NextRetryAt    time.Time
	StartedAt      time.Time
	LastActivityAt time.Time
}

// Tunnel is one dialed port-forward connection, returned by ForwardDialer.
type Tunnel interface {
	// Run blocks until the tunnel ends (lost connection or Close), calling
	// activity() for every new local connection it proxies — the only
	// per-connection signal client-go's portforward package exposes without
	// forking it, so ForwardManager treats it as a recency-of-use signal
	// (13c's TRAFFIC column) rather than a byte rate.
	Run(activity func()) error
	Close()
}

// ForwardDialer opens tunnels against namespace/pod. Bound to a fixed
// clientset/rest.Config snapshot at construction time (see
// NewSpdyForwardDialer) — never a live *Cluster reference — so an existing
// forward's reconnect attempts keep hitting the cluster it was started
// against even if the browsed context switches later.
type ForwardDialer interface {
	Dial(namespace, pod string, localPort int, remotePort int32) (Tunnel, error)
}

// PodResolver resolves a ForwardTarget to the pod name a dial should use:
// itself for a Pod target, or a matching backing pod for Service/Deployment.
type PodResolver interface {
	ResolveForwardPod(ctx context.Context, target ForwardTarget) (string, error)
}

// --- real (SPDY) dialer ---

type spdyDialer struct {
	clientset kubernetes.Interface
	restCfg   *rest.Config
}

// NewSpdyForwardDialer builds the real ForwardDialer, snapshotting clientset
// and restCfg at call time (the caller must build this fresh from the
// active *Cluster right before Start, not cache it across context
// switches).
func NewSpdyForwardDialer(clientset kubernetes.Interface, restCfg *rest.Config) ForwardDialer {
	return spdyDialer{clientset: clientset, restCfg: restCfg}
}

func (d spdyDialer) Dial(namespace, pod string, localPort int, remotePort int32) (Tunnel, error) {
	req := d.clientset.CoreV1().RESTClient().Post().
		Resource("pods").Namespace(namespace).Name(pod).SubResource("portforward")
	transport, upgrader, err := spdy.RoundTripperFor(d.restCfg)
	if err != nil {
		return nil, err
	}
	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, "POST", req.URL())
	return &spdyTunnel{dialer: dialer, localPort: localPort, remotePort: remotePort}, nil
}

type spdyTunnel struct {
	dialer     httpstreamDialer
	localPort  int
	remotePort int32

	mu     sync.Mutex
	stopCh chan struct{}
}

// httpstreamDialer is the subset of httpstream.Dialer portforward.New needs
// — declared locally so this file only imports client-go/transport/spdy for
// the concrete constructor, not apimachinery's httpstream package too.
type httpstreamDialer = httpstream.Dialer

func (t *spdyTunnel) Run(activity func()) error {
	t.mu.Lock()
	if t.stopCh != nil {
		t.mu.Unlock()
		return fmt.Errorf("tunnel already running")
	}
	stopCh := make(chan struct{})
	t.stopCh = stopCh
	t.mu.Unlock()

	readyCh := make(chan struct{})
	out := &activityWriter{activity: activity}
	pf, err := portforward.New(t.dialer, []string{fmt.Sprintf("%d:%d", t.localPort, t.remotePort)}, stopCh, readyCh, out, out)
	if err != nil {
		return err
	}
	return pf.ForwardPorts()
}

func (t *spdyTunnel) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopCh == nil {
		return
	}
	select {
	case <-t.stopCh:
	default:
		close(t.stopCh)
	}
}

// activityWriter scans portforward's out/errOut stream for "Handling
// connection" lines (see spdyTunnel.Run) and reports each as one activity
// tick.
type activityWriter struct{ activity func() }

func (w *activityWriter) Write(p []byte) (int, error) {
	if w.activity != nil && bytes.Contains(p, []byte("Handling connection")) {
		w.activity()
	}
	return len(p), nil
}

// --- real pod resolver ---

type clientsetPodResolver struct{ clientset kubernetes.Interface }

// NewClientsetPodResolver builds the real PodResolver, snapshotting
// clientset the same way NewSpdyForwardDialer does.
func NewClientsetPodResolver(clientset kubernetes.Interface) PodResolver {
	return clientsetPodResolver{clientset: clientset}
}

func (r clientsetPodResolver) ResolveForwardPod(ctx context.Context, target ForwardTarget) (string, error) {
	switch target.Kind {
	case KindPod:
		pod, err := r.clientset.CoreV1().Pods(target.Namespace).Get(ctx, target.Name, metav1.GetOptions{})
		if err != nil {
			return "", err
		}
		return pod.Name, nil
	case KindService:
		svc, err := r.clientset.CoreV1().Services(target.Namespace).Get(ctx, target.Name, metav1.GetOptions{})
		if err != nil {
			return "", err
		}
		if len(svc.Spec.Selector) == 0 {
			return "", fmt.Errorf("service %q has no selector to resolve a backing pod", target.Name)
		}
		return r.firstRunningPod(ctx, target.Namespace, labels.SelectorFromSet(svc.Spec.Selector))
	case KindDeployment:
		dep, err := r.clientset.AppsV1().Deployments(target.Namespace).Get(ctx, target.Name, metav1.GetOptions{})
		if err != nil {
			return "", err
		}
		sel, err := metav1.LabelSelectorAsSelector(dep.Spec.Selector)
		if err != nil {
			return "", err
		}
		return r.firstRunningPod(ctx, target.Namespace, sel)
	default:
		return "", fmt.Errorf("port-forward is not supported for kind %s", target.Kind)
	}
}

func (r clientsetPodResolver) firstRunningPod(ctx context.Context, namespace string, sel labels.Selector) (string, error) {
	pods, err := r.clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: sel.String()})
	if err != nil {
		return "", err
	}
	for _, p := range pods.Items {
		if p.Status.Phase == corev1.PodRunning {
			return p.Name, nil
		}
	}
	if len(pods.Items) > 0 {
		return pods.Items[0].Name, nil
	}
	return "", fmt.Errorf("no pods match the target's selector")
}

// PortForwardCommandString renders the kubectl invocation dialing pod
// directly would run — 13a's "will run" documentation line. The real dial
// path goes through client-go, not this subprocess, but the equivalent
// command is exact (same pod, same ports).
func PortForwardCommandString(namespace, pod string, localPort int, remotePort int32) string {
	return fmt.Sprintf("kubectl port-forward pod/%s %d:%d -n %s", pod, localPort, remotePort, namespace)
}

// --- ForwardManager ---

// ForwardManager owns every live forwarding session for the app's lifetime —
// constructed once at the composition root, shared by every screen that
// needs it, and never rebuilt on context switch (see the package doc).
type ForwardManager struct {
	mu       sync.Mutex
	sessions map[string]*forwardEntry
	events   chan struct{}
	nextID   int
}

type forwardEntry struct {
	session  ForwardSession
	dialer   ForwardDialer
	resolver PodResolver
	cancel   context.CancelFunc
	tunnel   Tunnel
}

// NewForwardManager builds an empty manager.
func NewForwardManager() *ForwardManager {
	return &ForwardManager{sessions: map[string]*forwardEntry{}, events: make(chan struct{}, 1)}
}

// Events fires (best-effort, coalesced) whenever any session's state
// changes — the composition root forwards this into the Bubble Tea program
// as a kube.ResourceChangedMsg{Kind: KindForward} (see internal/app), so
// Forwards reloads through the exact same path every other kind's watch
// events already use.
func (m *ForwardManager) Events() <-chan struct{} { return m.events }

func (m *ForwardManager) notify() {
	select {
	case m.events <- struct{}{}:
	default:
	}
}

// List returns every session, sorted by ID for a stable render order.
func (m *ForwardManager) List() []ForwardSession {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ForwardSession, 0, len(m.sessions))
	for _, e := range m.sessions {
		out = append(out, e.session)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ForwardObject adapts a ForwardSession to runtime.Object so Forwards can
// flow through the same resources.List/Project pipeline as real API
// objects (docs/design README.md §13c: "a registry kind, not a bespoke
// screen").
type ForwardObject struct {
	metav1.TypeMeta
	Session ForwardSession
}

// DeepCopyObject satisfies runtime.Object. ForwardSession has no pointer
// fields, so a shallow copy is a full deep copy.
func (o *ForwardObject) DeepCopyObject() runtime.Object {
	cp := *o
	return &cp
}

// ListRaw wraps List for resources.RawLister's shape (namespace is ignored
// — forwards are never namespace-filtered, docs/design README.md §13c).
func (m *ForwardManager) ListRaw() []runtime.Object {
	sessions := m.List()
	out := make([]runtime.Object, len(sessions))
	for i, s := range sessions {
		out[i] = &ForwardObject{Session: s}
	}
	return out
}

// Start opens a new forwarding session and returns its initial snapshot
// immediately; the dial itself (and any reconnect loop) runs in the
// background. dialer/resolver are the caller's already-snapshotted pair
// (NewSpdyForwardDialer/NewClientsetPodResolver for a real cluster, or
// kube/fake's equivalents in --demo).
func (m *ForwardManager) Start(dialer ForwardDialer, resolver PodResolver, target ForwardTarget, resolvedPod string, localPort int, remotePort int32, remotePortName string) ForwardSession {
	m.mu.Lock()
	m.nextID++
	id := fmt.Sprintf("fwd-%d", m.nextID)
	now := time.Now()
	session := ForwardSession{
		ID: id, Target: target, ResolvedPod: resolvedPod,
		LocalPort: localPort, RemotePort: remotePort, RemotePortName: remotePortName,
		State: ForwardActive, StartedAt: now, LastActivityAt: now,
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.sessions[id] = &forwardEntry{session: session, dialer: dialer, resolver: resolver, cancel: cancel}
	m.mu.Unlock()

	m.notify()
	go m.run(ctx, id, resolvedPod)
	return session
}

// run is one session's dial/reconnect loop: dial, block in Run() until the
// tunnel ends, mark Reconnecting with a backoff, re-resolve the target pod
// (Service/Deployment only — a Pod target that's gone stays gone), and
// retry. Returns as soon as ctx is cancelled (Stop/Restart).
func (m *ForwardManager) run(ctx context.Context, id, initialPod string) {
	pod := initialPod
	attempt := 0
	for {
		if ctx.Err() != nil {
			return
		}
		m.mu.Lock()
		entry, ok := m.sessions[id]
		if !ok {
			m.mu.Unlock()
			return
		}
		dialer, target, localPort, remotePort := entry.dialer, entry.session.Target, entry.session.LocalPort, entry.session.RemotePort
		m.mu.Unlock()

		var tunnel Tunnel
		var err error
		if dialer == nil {
			err = fmt.Errorf("no forward dialer configured")
		} else {
			tunnel, err = dialer.Dial(target.Namespace, pod, localPort, remotePort)
		}
		if err == nil {
			m.mu.Lock()
			if e, ok := m.sessions[id]; ok {
				e.tunnel = tunnel
				e.session.State = ForwardActive
				e.session.ResolvedPod = pod
				e.session.Err = ""
				e.session.Attempt = 0
				e.session.LastActivityAt = time.Now()
			}
			m.mu.Unlock()
			m.notify()
			attempt = 0
			err = tunnel.Run(func() { m.touchActivity(id) })
		}
		if ctx.Err() != nil {
			return
		}

		attempt++
		nextRetry := time.Now().Add(forwardBackoff(attempt))
		m.mu.Lock()
		if e, ok := m.sessions[id]; ok {
			e.session.State = ForwardReconnecting
			e.session.Attempt = attempt
			e.session.NextRetryAt = nextRetry
			if err != nil {
				e.session.Err = err.Error()
			}
		}
		m.mu.Unlock()
		m.notify()

		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(nextRetry)):
		}

		m.mu.Lock()
		entry, ok = m.sessions[id]
		if !ok {
			m.mu.Unlock()
			return
		}
		resolver, target := entry.resolver, entry.session.Target
		m.mu.Unlock()
		if target.Kind != KindPod && resolver != nil {
			rctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			if newPod, rerr := resolver.ResolveForwardPod(rctx, target); rerr == nil {
				pod = newPod
			}
			cancel()
		}
	}
}

func (m *ForwardManager) touchActivity(id string) {
	m.mu.Lock()
	if e, ok := m.sessions[id]; ok {
		e.session.LastActivityAt = time.Now()
	}
	m.mu.Unlock()
}

// Stop ends and forgets id — a stopped forward is removed outright (13c's
// 'x', "executes immediately (reversible)": reversible via starting a new
// one, not by reviving the old row).
func (m *ForwardManager) Stop(id string) {
	m.mu.Lock()
	entry, ok := m.sessions[id]
	if ok {
		delete(m.sessions, id)
	}
	m.mu.Unlock()
	if !ok {
		return
	}
	entry.cancel()
	if entry.tunnel != nil {
		entry.tunnel.Close()
	}
	m.notify()
}

// StopAll ends every session (13c's 'X', the one forward verb with an
// inline y/N — see verbs.StopAllForwards).
func (m *ForwardManager) StopAll() {
	m.mu.Lock()
	entries := make([]*forwardEntry, 0, len(m.sessions))
	for _, e := range m.sessions {
		entries = append(entries, e)
	}
	m.sessions = map[string]*forwardEntry{}
	m.mu.Unlock()
	for _, e := range entries {
		e.cancel()
		if e.tunnel != nil {
			e.tunnel.Close()
		}
	}
	if len(entries) > 0 {
		m.notify()
	}
}

// Restart force-reconnects id immediately, bypassing any pending backoff.
func (m *ForwardManager) Restart(id string) {
	m.mu.Lock()
	entry, ok := m.sessions[id]
	if !ok {
		m.mu.Unlock()
		return
	}
	pod := entry.session.ResolvedPod
	entry.cancel()
	if entry.tunnel != nil {
		entry.tunnel.Close()
	}
	ctx, cancel := context.WithCancel(context.Background())
	entry.cancel = cancel
	entry.tunnel = nil
	entry.session.State = ForwardActive
	entry.session.Attempt = 0
	entry.session.Err = ""
	m.mu.Unlock()
	m.notify()
	go m.run(ctx, id, pod)
}

// forwardBackoff is the reconnect delay schedule: 2s per attempt, capped at
// 30s — the same order of magnitude as the 4a connection-loss backoff.
func forwardBackoff(attempt int) time.Duration {
	d := time.Duration(attempt) * 2 * time.Second
	if d > 30*time.Second {
		return 30 * time.Second
	}
	if d <= 0 {
		return 2 * time.Second
	}
	return d
}
