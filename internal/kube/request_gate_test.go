package kube

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestAuthenticationGateBlocksBeforeCredentialTransport(t *testing.T) {
	t.Parallel()
	gate := &authenticationGate{}
	observer := newWatchObserver()
	var mu sync.Mutex
	calls := 0
	next := roundTripFunc(func(*http.Request) (*http.Response, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok"))}, nil
	})
	rt := &observingRoundTripper{next: next, gate: gate, observer: observer}
	gate.block(errors.New("expired"))

	req, _ := http.NewRequest(http.MethodGet, "https://cluster/api/v1/pods", nil)
	if _, err := rt.RoundTrip(req); !errors.Is(err, errAuthenticationGated) {
		t.Fatalf("blocked request error = %v", err)
	}
	mu.Lock()
	if calls != 0 {
		t.Fatalf("credential transport calls = %d, want zero", calls)
	}
	mu.Unlock()

	gate.permitProbe()
	livez, _ := http.NewRequest(http.MethodGet, "https://cluster/livez", nil)
	if _, err := rt.RoundTrip(livez); err != nil {
		t.Fatalf("permitted /livez: %v", err)
	}
	if _, err := rt.RoundTrip(livez); !errors.Is(err, errAuthenticationGated) {
		t.Fatalf("second /livez error = %v, want gate rejection", err)
	}
	gate.open()
	if _, err := rt.RoundTrip(req); err != nil {
		t.Fatalf("request after successful probe: %v", err)
	}
}

func TestWatchObserverMatchesTypedDynamicAndFilteredHelmWatches(t *testing.T) {
	t.Parallel()
	observer := newWatchObserver()
	seen := map[string]int{}
	var mu sync.Mutex
	register := func(name string, gvr schema.GroupVersionResource, namespace, selector string) {
		observer.register(gvr, namespace, selector, func() {
			mu.Lock()
			seen[name]++
			mu.Unlock()
		}, func(error) {})
	}
	register("typed", schema.GroupVersionResource{Version: "v1", Resource: "pods"}, "team-a", "")
	register("dynamic", schema.GroupVersionResource{Group: "example.io", Version: "v1", Resource: "widgets"}, "", "")
	register("helm", schema.GroupVersionResource{Version: "v1", Resource: "secrets"}, "team-a", helmReleaseFieldSelector)

	for _, rawURL := range []string{
		"https://cluster/api/v1/namespaces/team-a/pods?watch=true",
		"https://cluster/apis/example.io/v1/widgets?watch=true",
		"https://cluster/api/v1/namespaces/team-a/secrets?watch=true&fieldSelector=type%3Dhelm.sh%2Frelease.v1",
	} {
		req, _ := http.NewRequest(http.MethodGet, rawURL, nil)
		observer.established(req)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, name := range []string{"typed", "dynamic", "helm"} {
		if seen[name] != 1 {
			t.Errorf("%s WATCH callbacks = %d, want one", name, seen[name])
		}
	}
}

func TestWatchObserverIgnoresFailedAndNonWatchResponses(t *testing.T) {
	t.Parallel()
	gate := &authenticationGate{}
	observer := newWatchObserver()
	called := 0
	observer.register(schema.GroupVersionResource{Version: "v1", Resource: "pods"}, "", "", func() { called++ }, func(error) {})
	status := http.StatusGone
	rt := &observingRoundTripper{
		gate: gate, observer: observer,
		next: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(""))}, nil
		}),
	}
	watch, _ := http.NewRequest(http.MethodGet, "https://cluster/api/v1/pods?watch=true", nil)
	_, _ = rt.RoundTrip(watch)
	if called != 0 {
		t.Fatal("failed WATCH was reported established")
	}
	status = http.StatusOK
	list, _ := http.NewRequest(http.MethodGet, "https://cluster/api/v1/pods", nil)
	_, _ = rt.RoundTrip(list)
	if called != 0 {
		t.Fatal("successful LIST was reported as WATCH establishment")
	}
}

func TestWatchObserverReportsEstablishedBodyEnd(t *testing.T) {
	t.Parallel()
	gate := &authenticationGate{}
	observer := newWatchObserver()
	established := 0
	var ended error
	endedCalls := 0
	observer.register(
		schema.GroupVersionResource{Version: "v1", Resource: "pods"}, "", "",
		func() { established++ },
		func(err error) { ended, endedCalls = err, endedCalls+1 },
	)
	rt := &observingRoundTripper{
		gate: gate, observer: observer,
		next: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(""))}, nil
		}),
	}
	watch, _ := http.NewRequest(http.MethodGet, "https://cluster/api/v1/pods?watch=true", nil)
	resp, err := rt.RoundTrip(watch)
	if err != nil {
		t.Fatal(err)
	}
	if established != 1 {
		t.Fatalf("WATCH establishment callbacks = %d, want one", established)
	}
	if _, err := io.ReadAll(resp.Body); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(ended, io.EOF) {
		t.Fatalf("WATCH end error = %v, want EOF", ended)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if endedCalls != 1 {
		t.Fatalf("WATCH end callbacks after EOF and close = %d, want one", endedCalls)
	}
}
