package kube

import (
	"strconv"

	resource "k8s.io/apimachinery/pkg/api/resource"
)

type PodMetrics struct {
	CPU string
	MEM string
	// Exact aggregated usage, so callers don't have to re-parse the formatted
	// strings (which lose precision on decimal quantities like "5.8Gi").
	CPUMilli int64
	MemBytes int64
}

// NodeMetric is a node's live CPU/MEM usage — the 11a nodes-list bars'
// numerator (kube.Cluster.NodeMetrics), same shape as PodMetrics.
type NodeMetric struct {
	CPU      string
	MEM      string
	CPUMilli int64
	MemBytes int64
}

func formatCPU(q resource.Quantity) string {
	milli := q.MilliValue()
	if milli == 0 {
		return "0m"
	}
	if milli < 1000 {
		return strconv.FormatInt(milli, 10) + "m"
	}
	if milli%1000 == 0 {
		return strconv.FormatInt(milli/1000, 10)
	}
	return strconv.FormatFloat(float64(milli)/1000, 'f', 1, 64)
}

func formatMemory(q resource.Quantity) string {
	bytes := q.Value()
	if bytes < 1024 {
		return strconv.FormatInt(bytes, 10) + "B"
	}
	units := []struct {
		suffix string
		value  int64
	}{
		{"Gi", 1024 * 1024 * 1024},
		{"Mi", 1024 * 1024},
		{"Ki", 1024},
	}
	for _, unit := range units {
		if bytes >= unit.value {
			if bytes%unit.value == 0 {
				return strconv.FormatInt(bytes/unit.value, 10) + unit.suffix
			}
			return strconv.FormatFloat(float64(bytes)/float64(unit.value), 'f', 1, 64) + unit.suffix
		}
	}
	return strconv.FormatInt(bytes, 10) + "B"
}
