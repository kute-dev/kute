//go:build e2e

package e2e

import (
	"bufio"
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// RequestMatcher selects traffic using HTTP and Kubernetes request metadata.
// Empty fields are wildcards. Path is a prefix, which keeps matching stable
// across namespace and object-name suffixes.
type RequestMatcher struct {
	Method   string
	Path     string
	Resource string
	Verb     string
}

func (m RequestMatcher) matches(r RequestRecord) bool {
	return (m.Method == "" || strings.EqualFold(m.Method, r.Method)) &&
		(m.Path == "" || strings.HasPrefix(r.URL.Path, m.Path)) &&
		(m.Resource == "" || strings.EqualFold(m.Resource, r.Resource)) &&
		(m.Verb == "" || strings.EqualFold(m.Verb, r.Verb))
}

// RequestRecord is the wire-observable lifecycle of one proxied request.
// Completed is false while a request is active, making a fence useful for
// synchronizing a fault with a request that is deliberately held open.
type RequestRecord struct {
	ID          uint64
	Method      string
	URL         url.URL
	Resource    string
	Verb        string
	Started     time.Time
	CompletedAt time.Time
	StatusCode  int
	Error       string
	Completed   bool
	Cancelled   bool
}

// RequestCounts is a point-in-time view of proxy traffic.
type RequestCounts struct {
	Active int
	Total  int
	// ByResourceVerb keys are "resource/VERB", for example "pods/WATCH".
	ByResourceVerb       map[string]int
	ActiveByResourceVerb map[string]int
}

type proxyFault struct {
	matcher RequestMatcher
	status  int
	left    int
}

type proxyDelay struct {
	matcher RequestMatcher
	delay   time.Duration
}

type proxyGate struct {
	matcher RequestMatcher
	release chan struct{}
	once    sync.Once
}

// RequestGate holds matching requests until Release. Tests should first use
// WaitForRequest with a fence, then mutate state, then release the request.
type RequestGate struct{ gate *proxyGate }

func (g *RequestGate) Release() {
	if g != nil && g.gate != nil {
		g.gate.once.Do(func() { close(g.gate.release) })
	}
}

// APIProxy is a controllable TLS reverse proxy for Kubernetes E2E traffic.
type APIProxy struct {
	t          *testing.T
	server     *httptest.Server
	upstream   *url.URL
	transport  http.RoundTripper
	kubeconfig string

	mu        sync.Mutex
	nextID    uint64
	records   map[uint64]*RequestRecord
	active    map[uint64]context.CancelFunc
	faults    []*proxyFault
	delays    []proxyDelay
	gates     []*proxyGate
	available bool
	changed   chan struct{}
}

// NewAPIProxy starts a TLS proxy for the current context in kubeconfig and
// writes a temporary kubeconfig whose selected cluster points at the proxy.
func NewAPIProxy(t *testing.T, kubeconfig string) *APIProxy {
	t.Helper()
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	rules.ExplicitPath = kubeconfig
	raw, err := rules.Load()
	if err != nil {
		t.Fatalf("loading proxy source kubeconfig %s: %v", kubeconfig, err)
	}
	restCfg, err := clientcmd.NewNonInteractiveClientConfig(*raw, raw.CurrentContext, &clientcmd.ConfigOverrides{}, rules).ClientConfig()
	if err != nil {
		t.Fatalf("building proxy upstream config: %v", err)
	}
	upstream, err := url.Parse(restCfg.Host)
	if err != nil {
		t.Fatalf("parsing proxy upstream %q: %v", restCfg.Host, err)
	}
	// A reverse proxy must speak HTTP/1.1 upstream for Kubernetes' SPDY
	// port-forward/exec upgrade requests. Leaving ALPN at client-go's default
	// lets the shared transport negotiate h2 on an earlier LIST, after which an
	// Upgrade: SPDY/3.1 request fails locally with "http2: invalid Upgrade
	// request header" and never reaches the apiserver.
	upstreamCfg := rest.CopyConfig(restCfg)
	upstreamCfg.TLSClientConfig.NextProtos = []string{"http/1.1"}
	transport, err := rest.TransportFor(upstreamCfg)
	if err != nil {
		t.Fatalf("building proxy upstream transport: %v", err)
	}

	p := &APIProxy{
		t: t, upstream: upstream, transport: transport, available: true,
		records: make(map[uint64]*RequestRecord), active: make(map[uint64]context.CancelFunc),
		changed: make(chan struct{}),
	}
	p.server = httptest.NewUnstartedServer(http.HandlerFunc(p.serveHTTP))
	p.server.EnableHTTP2 = true
	p.server.StartTLS()
	p.kubeconfig = p.writeKubeconfig(raw)
	t.Cleanup(p.Close)
	return p
}

// KubeconfigPath is the temporary kubeconfig clients use to reach the proxy.
func (p *APIProxy) KubeconfigPath() string { return p.kubeconfig }

// Close stops active requests and the listener. It is safe to call repeatedly.
func (p *APIProxy) Close() {
	p.SetAvailable(false)
	if p.server != nil {
		p.server.Close()
	}
	if closer, ok := p.transport.(io.Closer); ok {
		_ = closer.Close()
	}
}

// Fence returns the latest assigned request sequence. Pass it to
// WaitForRequest to ignore all earlier traffic.
func (p *APIProxy) Fence() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.nextID
}

// WaitForRequest waits for a matching request with ID greater than after.
// It observes active as well as completed requests and uses notifications,
// never polling sleeps.
func (p *APIProxy) WaitForRequest(after uint64, matcher RequestMatcher, timeout time.Duration) RequestRecord {
	p.t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		p.mu.Lock()
		for id := after + 1; id <= p.nextID; id++ {
			if rec := p.records[id]; rec != nil && matcher.matches(*rec) {
				out := *rec
				p.mu.Unlock()
				return out
			}
		}
		changed := p.changed
		p.mu.Unlock()
		select {
		case <-changed:
		case <-deadline.C:
			p.t.Fatalf("proxy saw no request after fence %d matching %+v within %s", after, matcher, timeout)
		}
	}
}

// WaitForCompletion waits for a previously observed request to finish and
// returns its final response and cancellation fields.
func (p *APIProxy) WaitForCompletion(id uint64, timeout time.Duration) RequestRecord {
	p.t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		p.mu.Lock()
		rec := p.records[id]
		if rec == nil {
			p.mu.Unlock()
			p.t.Fatalf("proxy has no request %d", id)
		}
		if rec.Completed {
			out := *rec
			p.mu.Unlock()
			return out
		}
		changed := p.changed
		p.mu.Unlock()
		select {
		case <-changed:
		case <-deadline.C:
			p.t.Fatalf("proxy request %d did not complete within %s", id, timeout)
		}
	}
}

// History returns request records in sequence order.
func (p *APIProxy) History() []RequestRecord {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]RequestRecord, 0, len(p.records))
	for id := uint64(1); id <= p.nextID; id++ {
		if rec := p.records[id]; rec != nil {
			out = append(out, *rec)
		}
	}
	return out
}

// Counts reports active and total requests, including per resource/verb data.
func (p *APIProxy) Counts() RequestCounts {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := RequestCounts{Active: len(p.active), Total: len(p.records), ByResourceVerb: map[string]int{}, ActiveByResourceVerb: map[string]int{}}
	for id, rec := range p.records {
		key := rec.Resource + "/" + rec.Verb
		out.ByResourceVerb[key]++
		if _, ok := p.active[id]; ok {
			out.ActiveByResourceVerb[key]++
		}
	}
	return out
}

// SetAvailable switches the whole endpoint. Going unavailable also cancels
// active watches, logs, execs, forwards, and ordinary in-flight requests.
func (p *APIProxy) SetAvailable(available bool) {
	p.mu.Lock()
	p.available = available
	var cancels []context.CancelFunc
	if !available {
		for _, cancel := range p.active {
			cancels = append(cancels, cancel)
		}
	}
	p.signalLocked()
	p.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

// FailNext returns a Kubernetes Status response for the next n matches.
func (p *APIProxy) FailNext(matcher RequestMatcher, status, n int) {
	p.t.Helper()
	if n < 1 {
		p.t.Fatalf("proxy FailNext count must be positive, got %d", n)
	}
	switch status {
	case 401, 403, 410, 429, 503:
	default:
		p.t.Fatalf("proxy unsupported Kubernetes status %d", status)
	}
	p.mu.Lock()
	p.faults = append(p.faults, &proxyFault{matcher: matcher, status: status, left: n})
	p.mu.Unlock()
}

// Delay adds a fixed delay before matching requests are forwarded.
func (p *APIProxy) Delay(matcher RequestMatcher, delay time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.delays = append(p.delays, proxyDelay{matcher: matcher, delay: delay})
}

// ClearDelays removes all fixed-delay rules. Requests already waiting retain
// the delay selected when they started; use Hold when release timing matters.
func (p *APIProxy) ClearDelays() {
	p.mu.Lock()
	p.delays = nil
	p.mu.Unlock()
}

// ClearFaults removes all not-yet-consumed response faults.
func (p *APIProxy) ClearFaults() {
	p.mu.Lock()
	p.faults = nil
	p.mu.Unlock()
}

// Hold blocks matching requests until the returned gate is released.
func (p *APIProxy) Hold(matcher RequestMatcher) *RequestGate {
	g := &proxyGate{matcher: matcher, release: make(chan struct{})}
	p.mu.Lock()
	p.gates = append(p.gates, g)
	p.mu.Unlock()
	return &RequestGate{gate: g}
}

// CloseActive cancels every active request matching matcher and returns the
// number selected. Use Verb WATCH to close watches, or Path/query-derived
// resource metadata to close streaming response bodies.
func (p *APIProxy) CloseActive(matcher RequestMatcher) int {
	p.mu.Lock()
	var cancels []context.CancelFunc
	for id, cancel := range p.active {
		if matcher.matches(*p.records[id]) {
			cancels = append(cancels, cancel)
		}
	}
	p.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	return len(cancels)
}

func (p *APIProxy) serveHTTP(w http.ResponseWriter, incoming *http.Request) {
	resource, verb := kubernetesRequest(incoming)
	ctx, cancel := context.WithCancel(incoming.Context())
	incoming = incoming.WithContext(ctx)
	rec := RequestRecord{Method: incoming.Method, URL: *incoming.URL, Resource: resource, Verb: verb, Started: time.Now()}

	p.mu.Lock()
	p.nextID++
	rec.ID = p.nextID
	p.records[rec.ID] = &rec
	p.active[rec.ID] = cancel
	available := p.available
	var status int
	if available {
		for _, fault := range p.faults {
			if fault.left > 0 && fault.matcher.matches(rec) {
				fault.left--
				status = fault.status
				break
			}
		}
	}
	var delay time.Duration
	for _, rule := range p.delays {
		if rule.matcher.matches(rec) {
			delay = rule.delay
		}
	}
	var gates []*proxyGate
	for _, gate := range p.gates {
		if gate.matcher.matches(rec) {
			gates = append(gates, gate)
		}
	}
	p.signalLocked()
	p.mu.Unlock()

	rw := &statusWriter{ResponseWriter: w}
	defer func() {
		cancelled := ctx.Err() != nil
		cancel()
		p.mu.Lock()
		delete(p.active, rec.ID)
		rec.Completed = true
		rec.CompletedAt = time.Now()
		rec.StatusCode = rw.status
		rec.Cancelled = cancelled
		p.signalLocked()
		p.mu.Unlock()
	}()

	if !available {
		writeKubernetesStatus(rw, http.StatusServiceUnavailable)
		return
	}
	for _, gate := range gates {
		select {
		case <-gate.release:
		case <-ctx.Done():
			return
		}
	}
	if delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return
		}
	}
	if status != 0 {
		writeKubernetesStatus(rw, status)
		return
	}

	proxy := httputil.NewSingleHostReverseProxy(p.upstream)
	proxy.Transport = p.transport
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.Host = p.upstream.Host
	}
	proxy.ErrorHandler = func(out http.ResponseWriter, req *http.Request, err error) {
		p.mu.Lock()
		rec.Error = err.Error()
		p.mu.Unlock()
		if req.Context().Err() == nil {
			writeKubernetesStatus(out, http.StatusBadGateway)
		}
	}
	proxy.ServeHTTP(rw, incoming)
}

func (p *APIProxy) signalLocked() {
	close(p.changed)
	p.changed = make(chan struct{})
}

func (p *APIProxy) writeKubeconfig(raw *clientcmdapi.Config) string {
	copy := raw.DeepCopy()
	ctx := copy.Contexts[copy.CurrentContext]
	if ctx == nil {
		p.t.Fatalf("proxy kubeconfig has no current context %q", copy.CurrentContext)
	}
	cluster := copy.Clusters[ctx.Cluster]
	if cluster == nil {
		p.t.Fatalf("proxy kubeconfig context %q has no cluster %q", copy.CurrentContext, ctx.Cluster)
	}
	cluster.Server = p.server.URL
	cluster.InsecureSkipTLSVerify = false
	cluster.CertificateAuthority = ""
	cluster.CertificateAuthorityData = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: p.server.Certificate().Raw})
	path := filepath.Join(p.t.TempDir(), "proxy.kubeconfig")
	if err := clientcmd.WriteToFile(*copy, path); err != nil {
		p.t.Fatalf("writing proxy kubeconfig: %v", err)
	}
	return path
}

func kubernetesRequest(r *http.Request) (resource, verb string) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	start := -1
	if len(parts) >= 2 && parts[0] == "api" {
		start = 2
	}
	if len(parts) >= 3 && parts[0] == "apis" {
		start = 3
	}
	if start >= 0 && start < len(parts) {
		if parts[start] == "namespaces" && start+2 < len(parts) {
			start += 2
		}
		if start < len(parts) {
			resource = parts[start]
		}
	}
	if r.URL.Query().Get("watch") == "true" {
		return resource, "WATCH"
	}
	switch r.Method {
	case http.MethodGet:
		verb = "GET"
		if resource != "" && isCollectionPath(parts, resource) {
			verb = "LIST"
		}
	case http.MethodPost:
		verb = "CREATE"
	case http.MethodPut:
		verb = "UPDATE"
	case http.MethodPatch:
		verb = "PATCH"
	case http.MethodDelete:
		verb = "DELETE"
	default:
		verb = r.Method
	}
	if r.URL.Query().Get("follow") == "true" {
		verb = "STREAM"
	}
	return resource, verb
}

func isCollectionPath(parts []string, resource string) bool {
	for i := len(parts) - 1; i >= 0; i-- {
		if parts[i] == resource {
			return i == len(parts)-1
		}
	}
	return false
}

func writeKubernetesStatus(w http.ResponseWriter, code int) {
	reason := metav1.StatusReasonUnknown
	switch code {
	case 401:
		reason = metav1.StatusReasonUnauthorized
	case 403:
		reason = metav1.StatusReasonForbidden
	case 410:
		reason = metav1.StatusReasonGone
	case 429:
		reason = metav1.StatusReasonTooManyRequests
	case 503:
		reason = metav1.StatusReasonServiceUnavailable
	}
	status := apierrors.NewGenericServerResponse(code, "", schema.GroupResource{}, "", "fault injected by e2e proxy", 0, false).ErrStatus
	status.Reason = reason
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(status)
}

// statusWriter retains optional HTTP interfaces required by Kubernetes
// upgrade and streaming protocols while recording the response code.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}
func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("hijacking unsupported")
	}
	return h.Hijack()
}
func (w *statusWriter) Push(target string, opts *http.PushOptions) error {
	if p, ok := w.ResponseWriter.(http.Pusher); ok {
		return p.Push(target, opts)
	}
	return http.ErrNotSupported
}
