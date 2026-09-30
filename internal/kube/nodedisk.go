package kube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// ErrNodeStatsForbidden is NodeDiskUsage's sentinel for a caller without
// `get nodes/proxy` — the permission the kubelet summary API sits behind.
// It is a sentinel rather than a raw apierrors value so the UI can tell
// "you can't see this" from "the kubelet didn't answer" without importing
// apimachinery.
var ErrNodeStatsForbidden = errors.New("forbidden: get nodes/proxy")

// NodeDisk is a node's root filesystem usage as the kubelet itself reports
// it — the filesystem the kubelet's root dir, logs and emptyDirs live on,
// the one the DiskPressure (nodefs) eviction signal watches. metrics-server
// carries no disk figure at all, so this is the only live source.
type NodeDisk struct {
	UsedBytes     int64
	CapacityBytes int64
}

// NodeDiskUsage reads one node's root filesystem usage from the kubelet
// summary API, proxied through the API server
// (/api/v1/nodes/<name>/proxy/stats/summary). It is a per-node read, never a
// cluster-wide sweep: the summary carries every pod's stats alongside the
// node's own, so its size scales with the node's pod count, and 11b asks for
// it only for the one node on screen, on a slow cadence of its own.
func (c *Cluster) NodeDiskUsage(ctx context.Context, nodeName string) (NodeDisk, error) {
	raw, err := c.clientset.CoreV1().RESTClient().Get().
		AbsPath("/api/v1/nodes", nodeName, "proxy", "stats", "summary").
		DoRaw(ctx)
	if err != nil {
		if apierrors.IsForbidden(err) {
			return NodeDisk{}, fmt.Errorf("%w: %w", ErrNodeStatsForbidden, err)
		}
		return NodeDisk{}, err
	}
	return parseNodeDiskSummary(raw)
}

// parseNodeDiskSummary pulls node.fs out of a kubelet stats summary. Only
// the two fields read are declared — the rest of the payload (every pod's
// CPU/memory/network/volume stats) is skipped by the decoder.
func parseNodeDiskSummary(raw []byte) (NodeDisk, error) {
	var summary struct {
		Node struct {
			Fs *struct {
				UsedBytes     *uint64 `json:"usedBytes"`
				CapacityBytes *uint64 `json:"capacityBytes"`
			} `json:"fs"`
		} `json:"node"`
	}
	if err := json.Unmarshal(raw, &summary); err != nil {
		return NodeDisk{}, fmt.Errorf("decode kubelet stats summary: %w", err)
	}
	fs := summary.Node.Fs
	if fs == nil || fs.UsedBytes == nil || fs.CapacityBytes == nil {
		return NodeDisk{}, errors.New("kubelet stats summary carries no node filesystem figures")
	}
	return NodeDisk{UsedBytes: int64(*fs.UsedBytes), CapacityBytes: int64(*fs.CapacityBytes)}, nil
}
