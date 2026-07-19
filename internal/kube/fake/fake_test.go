package fake

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
)

func TestListRawFiltersByNamespace(t *testing.T) {
	t.Parallel()
	c := New("default", "dev")
	c.Seed(kube.KindPod,
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "a", Namespace: "default"}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "b", Namespace: "other"}},
	)

	all, err := c.ListRaw(context.Background(), kube.KindPod, "")
	if err != nil || len(all) != 2 {
		t.Fatalf("ListRaw(all) = %v, %v, want 2 objects", all, err)
	}
	scoped, err := c.ListRaw(context.Background(), kube.KindPod, "default")
	if err != nil || len(scoped) != 1 {
		t.Fatalf("ListRaw(default) = %v, %v, want 1 object", scoped, err)
	}
}

func TestDeleteResourceRemovesAndNotifies(t *testing.T) {
	t.Parallel()
	c := New("default", "dev")
	c.Seed(kube.KindPod, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "a", Namespace: "default"}})

	if err := c.DeleteResource(context.Background(), kube.KindPod, "default", "a"); err != nil {
		t.Fatalf("DeleteResource: %v", err)
	}
	remaining, _ := c.ListRaw(context.Background(), kube.KindPod, "default")
	if len(remaining) != 0 {
		t.Fatalf("expected pod removed, got %d remaining", len(remaining))
	}
	select {
	case msg := <-c.Events():
		if msg.Kind != kube.KindPod {
			t.Fatalf("notify kind = %v, want Pod", msg.Kind)
		}
	default:
		t.Fatalf("expected a ResourceChangedMsg after delete")
	}
}

func TestDeleteResourceMissingReturnsError(t *testing.T) {
	t.Parallel()
	c := New("default", "dev")
	if err := c.DeleteResource(context.Background(), kube.KindPod, "default", "missing"); err == nil {
		t.Fatalf("expected an error deleting a nonexistent pod")
	}
}

func TestCordonAndDrainSkipDaemonSetAndMirrorPods(t *testing.T) {
	t.Parallel()
	c := New("default", "dev")
	c.Seed(kube.KindNode, &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}})
	c.Seed(kube.KindPod,
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "default"}, Spec: corev1.PodSpec{NodeName: "node-1"}},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "ds", Namespace: "default", OwnerReferences: []metav1.OwnerReference{{Kind: "DaemonSet"}}},
			Spec:       corev1.PodSpec{NodeName: "node-1"},
		},
	)

	evicted, err := c.Drain(context.Background(), "node-1")
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if evicted != 1 {
		t.Fatalf("evicted = %d, want 1", evicted)
	}
	remaining, _ := c.ListRaw(context.Background(), kube.KindPod, "default")
	if len(remaining) != 1 {
		t.Fatalf("expected the DaemonSet pod to survive, got %d pods remaining", len(remaining))
	}
}

func TestGetYAMLStripsManagedFields(t *testing.T) {
	t.Parallel()
	c := New("default", "dev")
	c.Seed(kube.KindPod, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "api", Namespace: "default", ResourceVersion: "7",
			ManagedFields: []metav1.ManagedFieldsEntry{{Manager: "kubectl"}},
		},
	})

	yaml, rv, err := c.GetYAML(context.Background(), kube.KindPod, "default", "api")
	if err != nil {
		t.Fatalf("GetYAML: %v", err)
	}
	if rv != "7" {
		t.Fatalf("resourceVersion = %q, want 7", rv)
	}
	if strings.Contains(yaml, "managedFields") {
		t.Fatalf("expected managedFields stripped from yaml:\n%s", yaml)
	}
	if !strings.Contains(yaml, "name: api") {
		t.Fatalf("expected the pod name in the yaml:\n%s", yaml)
	}
}

func TestObjectEventsFiltersByInvolvedObject(t *testing.T) {
	t.Parallel()
	c := New("default", "dev")
	c.Seed(kube.KindEvent,
		&corev1.Event{
			ObjectMeta:     metav1.ObjectMeta{Name: "ev-1", Namespace: "default"},
			InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "api"},
			Reason:         "BackOff",
		},
		&corev1.Event{
			ObjectMeta:     metav1.ObjectMeta{Name: "ev-2", Namespace: "default"},
			InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "other"},
		},
	)

	got, err := c.ObjectEvents(context.Background(), "default", kube.KindPod, "api")
	if err != nil {
		t.Fatalf("ObjectEvents: %v", err)
	}
	if len(got) != 1 || got[0].Reason != "BackOff" {
		t.Fatalf("ObjectEvents = %+v, want one BackOff event", got)
	}
}

func TestSwitchNamespaceAndContext(t *testing.T) {
	t.Parallel()
	c := New("default", "dev")
	c.AddContext("prod", "prod-ns")

	c.SwitchNamespace("kube-system")
	if c.CurrentNamespace() != "kube-system" {
		t.Fatalf("CurrentNamespace = %q, want kube-system", c.CurrentNamespace())
	}

	if err := c.SwitchContext(context.Background(), "prod"); err != nil {
		t.Fatalf("SwitchContext: %v", err)
	}
	if c.CurrentContext() != "prod" || c.CurrentNamespace() != "prod-ns" {
		t.Fatalf("after SwitchContext: context=%q namespace=%q, want prod/prod-ns", c.CurrentContext(), c.CurrentNamespace())
	}

	if err := c.SwitchContext(context.Background(), "does-not-exist"); err == nil {
		t.Fatalf("expected an error switching to an unknown context")
	}
}

func TestStreamPodLogsReplaysSeededLines(t *testing.T) {
	t.Parallel()
	c := New("default", "dev")
	c.SeedLogs("default", "api", []string{"line one", "line two"})

	rc, err := c.StreamPodLogs(context.Background(), kube.LogStreamRequest{Namespace: "default", PodName: "api"})
	if err != nil {
		t.Fatalf("StreamPodLogs: %v", err)
	}
	defer rc.Close()
	buf := make([]byte, 1024)
	n, _ := rc.Read(buf)
	got := string(buf[:n])
	if !strings.Contains(got, "line one") || !strings.Contains(got, "line two") {
		t.Fatalf("streamed content = %q, want both seeded lines", got)
	}
}

func TestSetConnStateEmitsOnChannel(t *testing.T) {
	t.Parallel()
	c := New("default", "dev")
	c.SetConnState(kube.ConnState{Phase: kube.ConnReconnecting, Err: "dial timeout"})

	if got := c.ConnState().Phase; got != kube.ConnReconnecting {
		t.Fatalf("ConnState().Phase = %v, want Reconnecting", got)
	}
	select {
	case msg := <-c.ConnEvents():
		if msg.Phase != kube.ConnReconnecting {
			t.Fatalf("ConnEvents phase = %v, want Reconnecting", msg.Phase)
		}
	default:
		t.Fatalf("expected a ConnStateMsg after SetConnState")
	}
}

func TestNewDemoIsFeatureComplete(t *testing.T) {
	t.Parallel()
	c := NewDemo()
	ctx := context.Background()

	pods, err := c.ListRaw(ctx, kube.KindPod, "")
	if err != nil || len(pods) != 14 {
		t.Fatalf("ListRaw(Pod) = %d, %v, want 14 fixture pods", len(pods), err)
	}
	if pod, ok := findPod(pods, "api-7d9f6c8-abcde"); !ok || len(pod.Spec.Containers) < 2 {
		t.Fatalf("expected a multi-container pod (10a's exec-picker is otherwise unreachable in --demo), got %+v (ok=%v)", pod, ok)
	}
	deploys, _ := c.ListRaw(ctx, kube.KindDeployment, "")
	if len(deploys) != 14 {
		t.Fatalf("expected 14 deployment fixtures, got %d", len(deploys))
	}
	nodes, _ := c.ListRaw(ctx, kube.KindNode, "")
	if len(nodes) != 4 {
		t.Fatalf("expected 4 node fixtures, got %d", len(nodes))
	}
	namespaces, _ := c.ListRaw(ctx, kube.KindNamespace, "")
	if len(namespaces) != 10 {
		t.Fatalf("expected 10 namespace fixtures, got %d", len(namespaces))
	}
	events, err := c.ObjectEvents(ctx, "default", kube.KindPod, "worker-0")
	if err != nil || len(events) == 0 {
		t.Fatalf("expected events for the crashlooping pod, got %d, %v", len(events), err)
	}
	nsEvents, err := c.NamespaceEvents(ctx, "default")
	if err != nil || len(nsEvents) != len(events) {
		t.Fatalf("NamespaceEvents(default) = %d, %v, want %d", len(nsEvents), err, len(events))
	}

	// Every consumer seam must at least be callable without panicking.
	podMetrics, err := c.PodMetricsByNamespace(ctx, "default")
	if err != nil {
		t.Fatalf("PodMetricsByNamespace: %v", err)
	}
	if pm, ok := podMetrics["api-7d9f6c8-abcde"]; !ok || pm.CPU == "n/a" || pm.CPUMilli == 0 {
		t.Fatalf("expected real (non-n/a) CPU usage for a Running pod, got %+v (ok=%v)", pm, ok)
	}
	if nm, err := c.NodeMetrics(ctx); err != nil || len(nm) != 4 {
		t.Fatalf("NodeMetrics = %d, %v, want 4 fixture nodes", len(nm), err)
	}
	if _, _, err := c.GetYAML(ctx, kube.KindPod, "default", "worker-0"); err != nil {
		t.Fatalf("GetYAML: %v", err)
	}
	if _, err := c.StreamPodLogs(ctx, kube.LogStreamRequest{Namespace: "default", PodName: "worker-0"}); err != nil {
		t.Fatalf("StreamPodLogs: %v", err)
	}
}

// TestPodAndNodeMetricsAreDeterministicNotNA pins the §6a/CLAUDE.md
// feature-completeness fix: --demo's CPU/MEM columns used to be
// unconditionally "n/a" for every pod and node, so those bars/columns could
// never actually be exercised by driving --demo mode. Usage is now
// synthesized from each object's own limits/allocatable, and must be the
// same across repeated calls (a stable demo, not flickering random noise).
func TestPodAndNodeMetricsAreDeterministicNotNA(t *testing.T) {
	t.Parallel()
	c := NewDemo()
	ctx := context.Background()

	first, err := c.PodMetricsByNamespace(ctx, "default")
	if err != nil {
		t.Fatalf("PodMetricsByNamespace: %v", err)
	}
	pm, ok := first["api-7d9f6c8-abcde"]
	if !ok {
		t.Fatal("expected api-7d9f6c8-abcde in the fixture set")
	}
	if pm.CPU == "n/a" || pm.MEM == "n/a" || pm.CPUMilli <= 0 || pm.MemBytes <= 0 {
		t.Fatalf("expected real usage for a Running pod, got %+v", pm)
	}

	second, _ := c.PodMetricsByNamespace(ctx, "default")
	if second["api-7d9f6c8-abcde"] != pm {
		t.Fatalf("expected deterministic usage across calls, got %+v then %+v", pm, second["api-7d9f6c8-abcde"])
	}

	nodeMetrics, err := c.NodeMetrics(ctx)
	if err != nil {
		t.Fatalf("NodeMetrics: %v", err)
	}
	for name, nm := range nodeMetrics {
		if nm.CPU == "n/a" || nm.MEM == "n/a" || nm.CPUMilli <= 0 || nm.MemBytes <= 0 {
			t.Fatalf("expected real usage for node %q, got %+v", name, nm)
		}
	}
}

// findPod locates name among objs (runtime.Object values from ListRaw),
// returning the underlying *corev1.Pod.
func findPod(objs []runtime.Object, name string) (*corev1.Pod, bool) {
	for _, obj := range objs {
		if pod, ok := obj.(*corev1.Pod); ok && pod.Name == name {
			return pod, true
		}
	}
	return nil, false
}

// Compile-time interface satisfaction: the fake must actually implement
// kube.Mutator, the only formally exported multi-method consumer contract
// it stands in for today.
var _ kube.Mutator = (*Cluster)(nil)
