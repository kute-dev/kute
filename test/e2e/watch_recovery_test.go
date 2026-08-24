//go:build e2e

package e2e

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"
)

func TestTypedWatchCloseGoneRelistsWithoutFalseEmpty(t *testing.T) {
	RequireCluster(t)
	a := Launch(t)
	a.WaitForAll(Connect, "api-", "worker-")
	marker := fmt.Sprintf("watch-pod-%d", time.Now().UnixNano())
	exerciseWatchRecovery(t, a, RequestMatcher{Resource: "pods"}, "api-", func() {
		createDisposablePod(t, marker, map[string]string{"watch": "typed"})
	}, marker)
}

func TestDynamicWatchCloseGoneRelistsWithoutFalseEmpty(t *testing.T) {
	RequireCluster(t)
	a := Launch(t)
	a.WaitFor("api-", Connect)
	a.gotoKind(t, "widgets", "Widgets")
	a.WaitForAll(Settle, "sprocket", "flange")
	marker := fmt.Sprintf("watch-widget-%d", time.Now().UnixNano())
	exerciseWatchRecovery(t, a, RequestMatcher{Resource: "widgets"}, "sprocket", func() {
		createDisposableWidget(t, marker)
	}, marker)
}

func TestFilteredHelmWatchCloseGoneRelistsWithoutFalseEmpty(t *testing.T) {
	RequireCluster(t)
	a := Launch(t)
	a.WaitFor("api-", Connect)
	a.gotoKind(t, "helm", "Helm Releases")
	a.WaitForAll(Settle, "shop", "1.3.0", "deployed")
	exerciseWatchRecovery(t, a, RequestMatcher{
		Path:     "/api/v1/namespaces/" + Namespace + "/secrets",
		Resource: "secrets",
		Query:    map[string]string{"fieldSelector": "type=helm.sh/release.v1"},
	}, "shop", func() {
		createHelmRevision(t, 3, "1.4.0")
	}, "1.4.0")
}

func TestEmptyTypedWatchCloseGoneRelistsGapMutation(t *testing.T) {
	RequireCluster(t)
	namespace := createEmptyWatchNamespace(t, "typed")
	a := Launch(t, WithScopeNamespace(namespace))
	a.WaitFor("no pods in "+namespace, Connect)
	marker := fmt.Sprintf("watch-empty-pod-%d", time.Now().UnixNano())
	exerciseWatchRecovery(t, a, RequestMatcher{
		Path:     "/api/v1/namespaces/" + namespace + "/pods",
		Resource: "pods",
	}, "", func() {
		createDisposablePodInNamespace(t, namespace, marker, map[string]string{"watch": "empty-typed"})
	}, marker)
}

func TestEmptyDynamicWatchCloseGoneRelistsGapMutation(t *testing.T) {
	RequireCluster(t)
	namespace := createEmptyWatchNamespace(t, "dynamic")
	a := Launch(t, WithScopeNamespace(namespace))
	a.WaitFor("no pods in "+namespace, Connect)
	a.gotoKind(t, "widgets", "Widgets")
	a.WaitFor("no widgets in "+namespace, Settle)
	marker := fmt.Sprintf("watch-empty-widget-%d", time.Now().UnixNano())
	exerciseWatchRecovery(t, a, RequestMatcher{
		Path:     "/apis/kute.dev/v1/namespaces/" + namespace + "/widgets",
		Resource: "widgets",
	}, "", func() {
		createDisposableWidgetInNamespace(t, namespace, marker)
	}, marker)
}

func TestEmptyFilteredHelmWatchCloseGoneRelistsGapMutation(t *testing.T) {
	RequireCluster(t)
	namespace := createEmptyWatchNamespace(t, "helm")
	a := Launch(t, WithNamespace(namespace))
	a.WaitFor("no pods in "+namespace, Connect)
	a.gotoKind(t, "helm", "Helm Releases")
	a.WaitFor("no helm releases in "+namespace, Settle)
	exerciseWatchRecovery(t, a, RequestMatcher{
		Path:     "/api/v1/namespaces/" + namespace + "/secrets",
		Resource: "secrets",
		Query:    map[string]string{"fieldSelector": "type=helm.sh/release.v1"},
	}, "", func() {
		createHelmRevisionInNamespace(t, namespace, 3, "1.4.0")
	}, "1.4.0")
}

func exerciseWatchRecovery(t *testing.T, a *App, resource RequestMatcher, stable string, mutate func(), final string) {
	t.Helper()
	proxy := a.Proxy()
	watch := resource
	watch.Verb = "WATCH"
	if watch.Query == nil {
		watch.Query = map[string]string{}
	}
	watch.Query["sendInitialEvents"] = "true"
	proxy.FailNext(watch, 410, 1)
	gate := proxy.Hold(watch)

	// A cancelled streaming request and 410 Gone are both normal reflector
	// restart paths. Fail one health probe to make the degraded phase
	// observable, then hold its successful successor so it can be completed in
	// the exact ordering this test guards: livez=200 while the resource WATCH is
	// still unhealthy must not turn the header green.
	probe := RequestMatcher{Path: "/livez"}
	proxy.FailNext(probe, 409, 1)
	probeGate := proxy.Hold(probe)
	probeFence := proxy.Fence()
	failedProbe := proxy.WaitForRequest(probeFence, probe, Settle)
	completed := proxy.WaitForCompletion(failedProbe.ID, Settle)
	if completed.StatusCode != 409 {
		t.Fatalf("health probe status = %d, want 409 Conflict", completed.StatusCode)
	}
	// Observe the failure before looking for recovery. Otherwise a wait for
	// "connected" can accept the frame that predates the fault.
	a.WaitFor("disconnected", Settle)

	fence := proxy.Fence()
	if active := matchingActiveRequests(proxy.History(), watch); len(active) != 1 {
		t.Fatalf("active %+v requests before close = %d, want exactly one: %+v", watch, len(active), active)
	}
	if closed := proxy.CloseActive(watch); closed != 1 {
		t.Fatalf("closed %d active %+v watches, want exactly one", closed, resource)
	}

	goneWatch := proxy.WaitForRequest(fence, watch, Settle)
	completed = proxy.WaitForCompletion(goneWatch.ID, Settle)
	if completed.StatusCode != 410 {
		t.Fatalf("replacement watch status = %d, want 410 Gone", completed.StatusCode)
	}
	relist := proxy.WaitForRequest(goneWatch.ID, watch, Settle)
	if relist.Completed {
		t.Fatalf("streaming relist passed the request gate before the gap mutation: %+v", relist)
	}
	if stable != "" {
		if frame, lost := a.poll(func(f string) bool { return !strings.Contains(f, stable) }, 750*time.Millisecond); lost {
			t.Fatalf("cached row %q disappeared during relist gap:\n%s", stable, frame)
		}
	}
	// The object must exist before the held streaming relist reaches the API
	// server. Its initial events, rather than an ordinary healthy watch event,
	// are therefore the only path by which the cache can learn this value.
	mutate()
	a.Never(final, 750*time.Millisecond)
	successfulProbe := proxy.WaitForRequest(failedProbe.ID, probe, Settle)
	if successfulProbe.Completed {
		t.Fatalf("successful health probe passed its gate before watch recovery was staged: %+v", successfulProbe)
	}
	probeGate.Release()
	completed = proxy.WaitForCompletion(successfulProbe.ID, Settle)
	if completed.StatusCode != 200 {
		t.Fatalf("health probe status while relist was held = %d, want 200: %+v", completed.StatusCode, completed)
	}
	if stable != "" && !strings.Contains(a.Frame(), stable) {
		t.Fatalf("cached row %q disappeared after livez recovered but before its watch did:\n%s", stable, a.Frame())
	}
	a.WaitFor("disconnected", Settle)
	a.Never("● connected", 750*time.Millisecond)

	recoveryProbeFence := proxy.Fence()
	releasedAt := time.Now()
	gate.Release()
	recoveryProbe := proxy.WaitForRequest(recoveryProbeFence, probe, Settle)
	if delay := recoveryProbe.Started.Sub(releasedAt); delay > time.Second {
		t.Errorf("watch establishment did not request an immediate health retry: delay=%s request=%+v", delay, recoveryProbe)
	}
	a.WaitForAll(Settle, stable, final)
	a.WaitGone("disconnected", Settle)
	a.WaitFor("connected", Settle)
	waitForOneActiveWatch(t, proxy, watch, Settle)
}

func waitForOneActiveWatch(t *testing.T, proxy *APIProxy, matcher RequestMatcher, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if got := activeMatchingRequests(proxy.History(), matcher); got == 1 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("active %+v requests = %d, want exactly one", matcher, activeMatchingRequests(proxy.History(), matcher))
}

func activeMatchingRequests(records []RequestRecord, matcher RequestMatcher) int {
	return len(matchingActiveRequests(records, matcher))
}

func matchingActiveRequests(records []RequestRecord, matcher RequestMatcher) []RequestRecord {
	var active []RequestRecord
	for _, rec := range records {
		if !rec.Completed && matcher.matches(rec) {
			active = append(active, rec)
		}
	}
	return active
}

func createDisposableWidget(t *testing.T, name string) {
	createDisposableWidgetInNamespace(t, Namespace, name)
}

func createDisposableWidgetInNamespace(t *testing.T, namespace, name string) {
	t.Helper()
	cfg, err := clientcmd.BuildConfigFromFlags("", KubeconfigPath())
	if err != nil {
		t.Fatal(err)
	}
	client, err := dynamic.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	gvr := schema.GroupVersionResource{Group: "kute.dev", Version: "v1", Resource: "widgets"}
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "kute.dev/v1", "kind": "Widget",
		"metadata": map[string]any{"name": name, "namespace": namespace},
		"spec":     map[string]any{"size": "gap", "colour": "violet"},
		"status":   map[string]any{"phase": "Recovered"},
	}}
	ctx, cancel := context.WithTimeout(context.Background(), Settle)
	defer cancel()
	if _, err := client.Resource(gvr).Namespace(namespace).Create(ctx, obj, metav1.CreateOptions{}); err != nil {
		t.Fatalf("creating widget %s: %v", name, err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		_ = client.Resource(gvr).Namespace(namespace).Delete(cleanupCtx, name, metav1.DeleteOptions{})
	})
}

func createHelmRevision(t *testing.T, revision int, chartVersion string) {
	createHelmRevisionInNamespace(t, Namespace, revision, chartVersion)
}

func createHelmRevisionInNamespace(t *testing.T, namespace string, revision int, chartVersion string) {
	t.Helper()
	client := e2eClientset(t)
	ctx, cancel := context.WithTimeout(context.Background(), Settle)
	defer cancel()
	source, err := client.CoreV1().Secrets(Namespace).Get(ctx, "sh.helm.release.v1.shop.v2", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("reading source Helm release: %v", err)
	}
	raw, err := base64.StdEncoding.DecodeString(string(source.Data["release"]))
	if err != nil {
		t.Fatalf("decoding Helm release: %v", err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("opening Helm release: %v", err)
	}
	var release map[string]any
	if err := json.NewDecoder(gz).Decode(&release); err != nil {
		t.Fatalf("decoding Helm JSON: %v", err)
	}
	_ = gz.Close()
	release["version"] = revision
	release["namespace"] = namespace
	release["chart"].(map[string]any)["metadata"].(map[string]any)["version"] = chartVersion
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	if err := json.NewEncoder(zw).Encode(release); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("sh.helm.release.v1.shop.v%d", revision)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: map[string]string{
			"owner": "helm", "name": "shop", "status": "deployed", "version": fmt.Sprint(revision),
		}},
		Type: "helm.sh/release.v1",
		Data: map[string][]byte{"release": []byte(base64.StdEncoding.EncodeToString(compressed.Bytes()))},
	}
	if _, err := client.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{}); err != nil {
		t.Fatalf("creating Helm revision: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		_ = client.CoreV1().Secrets(namespace).Delete(cleanupCtx, name, metav1.DeleteOptions{})
	})
}

func createEmptyWatchNamespace(t *testing.T, kind string) string {
	t.Helper()
	client := e2eClientset(t)
	namespace := fmt.Sprintf("kute-watch-%s-%d", kind, time.Now().UnixNano())
	ctx, cancel := context.WithTimeout(context.Background(), Settle)
	defer cancel()
	if _, err := client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: namespace},
	}, metav1.CreateOptions{}); err != nil {
		t.Fatalf("creating empty watch-recovery namespace %s: %v", namespace, err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		_ = client.CoreV1().Namespaces().Delete(cleanupCtx, namespace, metav1.DeleteOptions{})
	})
	return namespace
}
