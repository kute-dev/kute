package kube

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	metricsfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"
)

// podMetricsGVR is the resource the generated metrics client actually talks
// to: "pods", not the "podmetricses" that ObjectTracker.Add guesses from the
// kind name. Seeding a fake metrics clientset through NewSimpleClientset's
// varargs therefore files the objects where the typed client will never look
// and every List comes back empty — hence newPodMetricsClient below.
var podMetricsGVR = schema.GroupVersionResource{Group: "metrics.k8s.io", Version: "v1beta1", Resource: "pods"}

func newPodMetricsClient(t *testing.T, objs ...*metricsv1beta1.PodMetrics) *metricsfake.Clientset {
	t.Helper()
	cs := metricsfake.NewSimpleClientset()
	for _, obj := range objs {
		if err := cs.Tracker().Create(podMetricsGVR, obj, obj.Namespace); err != nil {
			t.Fatalf("seed %s/%s: %v", obj.Namespace, obj.Name, err)
		}
	}
	return cs
}

func podMetric(namespace, name, cpu, mem string) *metricsv1beta1.PodMetrics {
	return &metricsv1beta1.PodMetrics{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Containers: []metricsv1beta1.ContainerMetrics{{
			Name: "app",
			Usage: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(cpu),
				corev1.ResourceMemory: resource.MustParse(mem),
			},
		}},
	}
}

// TestPodMetricsKeyedByNamespace is the guard for a silent cross-namespace
// mix-up: a cluster-wide read ("" namespace) used to key on the bare pod
// name, so two same-named pods — a DaemonSet, or one chart installed into
// several namespaces — overwrote each other and a row could show a
// different pod's CPU and memory entirely.
func TestPodMetricsKeyedByNamespace(t *testing.T) {
	c := &Cluster{metrics: newPodMetricsClient(t,
		podMetric("prod", "log-agent", "500m", "512Mi"),
		podMetric("staging", "log-agent", "10m", "32Mi"),
	)}

	got, err := c.PodMetricsByNamespace(t.Context(), "")
	if err != nil {
		t.Fatalf("PodMetricsByNamespace: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2 — same-named pods collided", len(got))
	}
	if pm := got[PodKey("prod", "log-agent")]; pm.CPUMilli != 500 {
		t.Errorf("prod/log-agent CPUMilli = %d, want 500", pm.CPUMilli)
	}
	if pm := got[PodKey("staging", "log-agent")]; pm.CPUMilli != 10 {
		t.Errorf("staging/log-agent CPUMilli = %d, want 10", pm.CPUMilli)
	}
}

func TestContainerMetricsKeyedByNamespace(t *testing.T) {
	c := &Cluster{metrics: newPodMetricsClient(t,
		podMetric("prod", "log-agent", "500m", "512Mi"),
		podMetric("staging", "log-agent", "10m", "32Mi"),
	)}

	got, err := c.ContainerMetricsByNamespace(t.Context(), "")
	if err != nil {
		t.Fatalf("ContainerMetricsByNamespace: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2 — same-named pods collided", len(got))
	}
	if pm := got[PodKey("prod", "log-agent")]["app"]; pm.CPUMilli != 500 {
		t.Errorf("prod/log-agent app CPUMilli = %d, want 500", pm.CPUMilli)
	}
}
