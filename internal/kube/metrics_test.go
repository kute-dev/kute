package kube

import (
	"math"
	"testing"

	"github.com/charmbracelet/x/ansi"
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
		"0":          "0B",
		// Three significant digits: a fourth would truncate to "174.…" in
		// browse's metric cell.
		"174.5Mi": "175Mi",
		"12.5Gi":  "13Gi",
		// A fourth digit either way, so both hand off to the next unit up.
		"1000.4Mi": "1.0Gi",
		"1023.6Mi": "1.0Gi",
	}
	for input, want := range cases {
		t.Run(input, func(t *testing.T) {
			t.Parallel()
			q := resource.MustParse(input)
			if got := FormatMemory(q); got != want {
				t.Fatalf("FormatMemory(%s) = %s, want %s", input, got, want)
			}
		})
	}
}

// TestFormatMemoryFitsTheMetricCell is the real constraint behind FormatMemory's
// three-significant-digit rule: browse's CPU/MEM columns are
// resources.MetricColumnWidth (12) wide, of which the mini bar takes 6 and the
// separating space 1, leaving exactly 5 cells for the value. This package can't
// import internal/resources to say so (that would be an import cycle), so the
// coupling lives in this comment — if MetricColumnWidth or the bar width ever
// moves, this bound moves with it.
func TestFormatMemoryFitsTheMetricCell(t *testing.T) {
	t.Parallel()

	const maxCells = 5

	var bytes []int64
	for _, unit := range []int64{1, 1 << 10, 1 << 20, 1 << 30, 1 << 40, 1 << 50, 1 << 60} {
		// Both sides of every rounding boundary within the unit, including the
		// band just under 1000 that rounds up into the next one.
		for _, mult := range []float64{1, 1.04, 1.05, 9.94, 9.95, 10, 99.4, 99.5, 512.5, 999.4, 999.5, 1023.9} {
			scaled := float64(unit) * mult
			if scaled > float64(math.MaxInt64) {
				continue
			}
			bytes = append(bytes, int64(scaled))
		}
	}
	bytes = append(bytes, 0, 1, 1023, math.MaxInt64)

	for _, b := range bytes {
		q := resource.NewQuantity(b, resource.BinarySI)
		got := FormatMemory(*q)
		if n := ansi.StringWidth(got); n > maxCells {
			t.Errorf("FormatMemory(%d bytes) = %q, %d cells wide, want at most %d", b, got, n, maxCells)
		}
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
