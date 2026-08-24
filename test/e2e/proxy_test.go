//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func proxyFixture(t *testing.T, handler http.Handler) (string, *httptest.Server) {
	t.Helper()
	upstream := httptest.NewTLSServer(handler)
	t.Cleanup(upstream.Close)
	cfg := clientcmdapi.NewConfig()
	cfg.CurrentContext = "source"
	cfg.Clusters["cluster"] = &clientcmdapi.Cluster{Server: upstream.URL, InsecureSkipTLSVerify: true}
	cfg.Contexts["source"] = &clientcmdapi.Context{Cluster: "cluster", AuthInfo: "user", Namespace: "fixture"}
	cfg.AuthInfos["user"] = &clientcmdapi.AuthInfo{Token: "fixture-token"}
	path := filepath.Join(t.TempDir(), "source.kubeconfig")
	if err := clientcmd.WriteToFile(*cfg, path); err != nil {
		t.Fatal(err)
	}
	return path, upstream
}

func proxyClient(t *testing.T, path string) *http.Client {
	t.Helper()
	cfg, err := clientcmd.BuildConfigFromFlags("", path)
	if err != nil {
		t.Fatal(err)
	}
	client, err := rest.HTTPClientFor(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestAPIProxyForwardsAndInjectsStatus(t *testing.T) {
	source, _ := proxyFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer fixture-token" {
			t.Errorf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"kind":"PodList","apiVersion":"v1","items":[]}`)
	}))
	p := NewAPIProxy(t, source)
	client := proxyClient(t, p.KubeconfigPath())
	base, err := clientcmd.BuildConfigFromFlags("", p.KubeconfigPath())
	if err != nil {
		t.Fatal(err)
	}

	fence := p.Fence()
	resp, err := client.Get(base.Host + "/api/v1/namespaces/fixture/pods")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("forwarded status = %d", resp.StatusCode)
	}
	rec := p.WaitForRequest(fence, RequestMatcher{Resource: "pods", Verb: "LIST"}, time.Second)
	if rec.Method != http.MethodGet {
		t.Errorf("method = %s", rec.Method)
	}

	p.FailNext(RequestMatcher{Resource: "pods", Verb: "LIST"}, http.StatusGone, 1)
	resp, err = client.Get(base.Host + "/api/v1/namespaces/fixture/pods")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusGone {
		t.Fatalf("fault status = %d, want 410", resp.StatusCode)
	}
	var status metav1.Status
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if status.Reason != metav1.StatusReasonGone {
		t.Errorf("reason = %q", status.Reason)
	}

	p.FailNextStatus(RequestMatcher{Resource: "pods", Verb: "LIST"}, http.StatusServiceUnavailable, "unique fixture fault", 1)
	resp, err = client.Get(base.Host + "/api/v1/namespaces/fixture/pods")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if status.Message != "unique fixture fault" {
		t.Errorf("message = %q", status.Message)
	}

	counts := p.Counts()
	if counts.Total != 3 || counts.ByResourceVerb["pods/LIST"] != 3 {
		t.Errorf("counts = %+v", counts)
	}
}

func TestAPIProxyCanForwardClientAuthentication(t *testing.T) {
	seen := make(chan string, 1)
	source, _ := proxyFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	p := NewAPIProxyForwardingClientAuth(t, source)
	client := proxyClient(t, p.KubeconfigPath())
	cfg, err := clientcmd.BuildConfigFromFlags("", p.KubeconfigPath())
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Get(cfg.Host + "/livez")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	select {
	case got := <-seen:
		if got != "Bearer fixture-token" {
			t.Fatalf("upstream Authorization = %q, want the proxy client's bearer token", got)
		}
	case <-time.After(time.Second):
		t.Fatal("upstream never received the proxied request")
	}
}

func TestAPIProxyInjectsConflict(t *testing.T) {
	source, _ := proxyFixture(t, http.NotFoundHandler())
	p := NewAPIProxy(t, source)
	client := proxyClient(t, p.KubeconfigPath())
	base, err := clientcmd.BuildConfigFromFlags("", p.KubeconfigPath())
	if err != nil {
		t.Fatal(err)
	}

	p.FailNext(RequestMatcher{Resource: "secrets", Verb: "PATCH"}, http.StatusConflict, 1)
	req, err := http.NewRequest(http.MethodPatch, base.Host+"/api/v1/namespaces/fixture/secrets/example", strings.NewReader(`{"stringData":{"key":"value"}}`))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("fault status = %d, want 409", resp.StatusCode)
	}
	var status metav1.Status
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if status.Reason != metav1.StatusReasonConflict {
		t.Errorf("reason = %q, want Conflict", status.Reason)
	}
}

func TestAPIProxyFenceAndCloseActive(t *testing.T) {
	source, _ := proxyFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	p := NewAPIProxy(t, source)
	client := proxyClient(t, p.KubeconfigPath())
	cfg, err := clientcmd.BuildConfigFromFlags("", p.KubeconfigPath())
	if err != nil {
		t.Fatal(err)
	}

	fence := p.Fence()
	done := make(chan error, 1)
	go func() {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, cfg.Host+"/api/v1/pods?watch=true", nil)
		resp, err := client.Do(req)
		if resp != nil {
			_ = resp.Body.Close()
		}
		done <- err
	}()
	rec := p.WaitForRequest(fence, RequestMatcher{Resource: "pods", Verb: "WATCH"}, time.Second)
	if rec.Completed {
		t.Fatal("held upstream request was already complete")
	}
	if got := p.Counts().ActiveByResourceVerb["pods/WATCH"]; got != 1 {
		t.Fatalf("active watches = %d", got)
	}
	if got := p.CloseActive(RequestMatcher{Verb: "WATCH"}); got != 1 {
		t.Fatalf("closed = %d", got)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancelled watch did not return")
	}
	completed := p.WaitForCompletion(rec.ID, time.Second)
	if !completed.Cancelled {
		t.Errorf("record was not marked cancelled: %+v", completed)
	}
}

func TestBuildMergedKubeconfigKeepsDistinctEndpoints(t *testing.T) {
	first, _ := proxyFixture(t, http.NotFoundHandler())
	second, _ := proxyFixture(t, http.NotFoundHandler())
	path := BuildMergedKubeconfig(t,
		KubeconfigContext{Name: "one", Kubeconfig: first},
		KubeconfigContext{Name: "two", Kubeconfig: second},
	)
	cfg, err := clientcmd.LoadFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CurrentContext != "one" {
		t.Errorf("current context = %q", cfg.CurrentContext)
	}
	if cfg.Contexts["one"].Cluster == cfg.Contexts["two"].Cluster {
		t.Error("cluster keys collided")
	}
	if cfg.Clusters[cfg.Contexts["one"].Cluster].Server == cfg.Clusters[cfg.Contexts["two"].Cluster].Server {
		t.Error("endpoints collapsed")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}
