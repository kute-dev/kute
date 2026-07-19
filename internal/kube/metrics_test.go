package kube

import (
	"testing"

	resource "k8s.io/apimachinery/pkg/api/resource"
)

func TestFormatCPU(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"25m":   "25m",
		"1500m": "1.5",
		"2":     "2",
	}
	for input, want := range cases {
		input, want := input, want
		t.Run(input, func(t *testing.T) {
			t.Parallel()
			q := resource.MustParse(input)
			if got := FormatCPU(q); got != want {
				t.Fatalf("FormatCPU(%s) = %s, want %s", input, got, want)
			}
		})
	}
}

func TestFormatMemory(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"65536Ki":    "64Mi",
		"1536Mi":     "1.5Gi",
		"1073741824": "1Gi",
	}
	for input, want := range cases {
		input, want := input, want
		t.Run(input, func(t *testing.T) {
			t.Parallel()
			q := resource.MustParse(input)
			if got := FormatMemory(q); got != want {
				t.Fatalf("FormatMemory(%s) = %s, want %s", input, got, want)
			}
		})
	}
}

func TestPodMetricsCanRepresentAggregatedContainerUsage(t *testing.T) {
	t.Parallel()

	metrics := PodMetrics{CPU: FormatCPU(resource.MustParse("819m")), MEM: FormatMemory(resource.MustParse("6656Mi"))}
	if metrics.CPU != "819m" {
		t.Fatalf("CPU = %s, want 819m", metrics.CPU)
	}
	if metrics.MEM != "6.5Gi" {
		t.Fatalf("MEM = %s, want 6.5Gi", metrics.MEM)
	}
}
