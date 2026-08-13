package resources

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestProjectServiceIncludesExternalIPAfterClusterIP(t *testing.T) {
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "default", CreationTimestamp: metav1.NewTime(time.Now())},
		Spec: corev1.ServiceSpec{
			Type:        corev1.ServiceTypeLoadBalancer,
			ClusterIP:   "10.0.0.1",
			ExternalIPs: []string{"203.0.113.10", "203.0.113.11"},
			Ports:       []corev1.ServicePort{{Port: 80}, {Port: 443}},
		},
	}

	row := projectService(service)
	want := []string{"web", "LoadBalancer", "10.0.0.1", "203.0.113.10,203.0.113.11", "80,443", "0s"}
	if len(row.Cells) != len(want) {
		t.Fatalf("got %d cells, want %d: %v", len(row.Cells), len(want), row.Cells)
	}
	for i := range want {
		if row.Cells[i] != want[i] {
			t.Errorf("cell %d = %q, want %q", i, row.Cells[i], want[i])
		}
	}
}

func TestProjectServiceUsesLoadBalancerIngress(t *testing.T) {
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "mssql", Namespace: "default", CreationTimestamp: metav1.NewTime(time.Now())},
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeLoadBalancer,
			ClusterIP: "10.152.183.106",
			Ports:     []corev1.ServicePort{{Port: 1433}},
		},
		Status: corev1.ServiceStatus{LoadBalancer: corev1.LoadBalancerStatus{
			Ingress: []corev1.LoadBalancerIngress{{IP: "10.100.20.20"}},
		}},
	}

	row := projectService(service)
	if got := row.Cells[3]; got != "10.100.20.20" {
		t.Fatalf("ExternalIP cell = %q, want 10.100.20.20", got)
	}
}
