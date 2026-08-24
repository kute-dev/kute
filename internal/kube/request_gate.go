package kube

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

// errAuthenticationGated is returned before client-go's credential transport
// is reached while a context has no usable credentials.
var errAuthenticationGated = errors.New("kubernetes traffic paused until credentials are retried")

// authenticationGate sits outside client-go's credential round tripper. It
// prevents reflector retries and one-shot reads from repeatedly invoking a
// failed exec plugin. An explicit retry grants exactly one /livez request.
type authenticationGate struct {
	mu           sync.Mutex
	blocked      bool
	probeAllowed bool
	onBlocked    func(error)
}

func (g *authenticationGate) block(err error) {
	g.mu.Lock()
	first := !g.blocked
	g.blocked = true
	g.probeAllowed = false
	callback := g.onBlocked
	g.mu.Unlock()
	if first && callback != nil {
		callback(err)
	}
}

func (g *authenticationGate) permitProbe() {
	g.mu.Lock()
	if g.blocked {
		g.probeAllowed = true
	}
	g.mu.Unlock()
}

func (g *authenticationGate) open() {
	g.mu.Lock()
	g.blocked = false
	g.probeAllowed = false
	g.mu.Unlock()
}

func (g *authenticationGate) isBlocked() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.blocked
}

func (g *authenticationGate) allow(req *http.Request) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.blocked {
		return true
	}
	if req.URL.Path == "/livez" && g.probeAllowed {
		g.probeAllowed = false
		return true
	}
	return false
}

type observingRoundTripper struct {
	next     http.RoundTripper
	gate     *authenticationGate
	observer *watchObserver
}

func (t *observingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if !t.gate.allow(req) {
		return nil, errAuthenticationGated
	}
	resp, err := t.next.RoundTrip(req)
	if err != nil {
		if IsAuthenticationError(err) {
			t.gate.block(err)
		}
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		t.gate.block(fmt.Errorf("kubernetes API returned %s", resp.Status))
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 && req.URL.Query().Get("watch") == "true" {
		t.observer.established(req)
	}
	return resp, nil
}

type watchRegistration struct {
	group, version, resource, namespace, fieldSelector string
}

type watchObserver struct {
	mu        sync.Mutex
	callbacks map[watchRegistration][]func()
}

func newWatchObserver() *watchObserver {
	return &watchObserver{callbacks: map[watchRegistration][]func(){}}
}

func (o *watchObserver) register(gvr schema.GroupVersionResource, namespace, fieldSelector string, callback func()) {
	o.mu.Lock()
	o.callbacks[watchRegistration{gvr.Group, gvr.Version, gvr.Resource, namespace, fieldSelector}] = append(
		o.callbacks[watchRegistration{gvr.Group, gvr.Version, gvr.Resource, namespace, fieldSelector}], callback)
	o.mu.Unlock()
}

func (o *watchObserver) established(req *http.Request) {
	got, ok := watchRegistrationForRequest(req)
	if !ok {
		return
	}
	o.mu.Lock()
	callbacks := append([]func(){}, o.callbacks[got]...)
	o.mu.Unlock()
	for _, callback := range callbacks {
		callback()
	}
}

func watchRegistrationForRequest(req *http.Request) (watchRegistration, bool) {
	parts := strings.Split(strings.Trim(req.URL.Path, "/"), "/")
	var got watchRegistration
	switch {
	case len(parts) >= 3 && parts[0] == "api":
		got.version = parts[1]
		parts = parts[2:]
	case len(parts) >= 4 && parts[0] == "apis":
		got.group, got.version = parts[1], parts[2]
		parts = parts[3:]
	default:
		return watchRegistration{}, false
	}
	if len(parts) >= 3 && parts[0] == "namespaces" {
		got.namespace = parts[1]
		parts = parts[2:]
	}
	if len(parts) != 1 {
		return watchRegistration{}, false
	}
	got.resource = parts[0]
	got.fieldSelector = req.URL.Query().Get("fieldSelector")
	return got, true
}
