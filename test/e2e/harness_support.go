//go:build e2e

package e2e

import (
	"fmt"
	"net"
	"runtime"
	"strings"
	"testing"
	"time"

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// WaitForTCPRefused waits until address no longer accepts TCP connections.
// It is intended for listener-cleanup assertions after stopping a forward.
func WaitForTCPRefused(t *testing.T, address string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		conn, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if err != nil {
			return
		}
		_ = conn.Close()
		if time.Now().After(deadline) {
			t.Fatalf("TCP address %s still accepted connections after %s", address, timeout)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// RuntimeSnapshot captures memory plus classified live goroutine stacks.
// Stacks retains the complete dump so a failed budget can explain itself.
type RuntimeSnapshot struct {
	CapturedAt time.Time
	HeapAlloc  uint64
	HeapInuse  uint64
	TotalAlloc uint64
	Goroutines int
	Classes    map[string]int
	Stacks     string
}

// SnapshotRuntime runs a GC, captures the process heap and goroutine dump,
// and classifies stacks relevant to long-lived E2E resources.
func SnapshotRuntime() RuntimeSnapshot {
	runtime.GC()
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, len(buf)*2)
	}
	dump := string(buf)
	classes := map[string]int{"informers": 0, "bubbletea_commands": 0, "streams": 0, "forwards": 0, "other": 0}
	count := 0
	for _, stack := range strings.Split(strings.TrimSpace(dump), "\n\n") {
		if stack == "" {
			continue
		}
		count++
		classes[classifyStack(stack)]++
	}
	return RuntimeSnapshot{CapturedAt: time.Now(), HeapAlloc: mem.HeapAlloc, HeapInuse: mem.HeapInuse, TotalAlloc: mem.TotalAlloc, Goroutines: count, Classes: classes, Stacks: dump}
}

// classifyStack buckets one goroutine stack for the Classes breakdown a
// failed leak budget prints.
//
// The budgets themselves compare a baseline against an after-snapshot of the
// same process, so only the total count decides pass or fail — but the
// breakdown is the entire diagnostic a failure leaves behind, and a
// breakdown that lies is worse than none. So every marker here is a package
// path or a concrete symbol rather than a loose word: matching bare "stream"
// swept up client-go's own watch plumbing, half the TLS stack and anything
// with "Stream" in a frame name, and pairing "bubbletea" with "cmd" matched
// essentially every goroutine the program owns, since bubbletea appears
// somewhere in most of their stacks.
func classifyStack(stack string) string {
	lower := strings.ToLower(stack)
	switch {
	case strings.Contains(lower, "sharedindexinformer") ||
		strings.Contains(lower, "cache.(*reflector)") ||
		strings.Contains(lower, "sharedinformerfactory") ||
		strings.Contains(lower, "cache.(*processorlistener)"):
		return "informers"
	case strings.Contains(lower, "internal/kube.(*forwardmanager)") ||
		strings.Contains(lower, "portforward.(*portforwarder)") ||
		strings.Contains(lower, "internal/kube.(*forward"):
		return "forwards"
	case strings.Contains(lower, "tasks/podlogs") ||
		strings.Contains(lower, "internal/kube.(*logstream") ||
		strings.Contains(lower, "watch.(*streamwatcher)"):
		return "streams"
	// Deliberately last and deliberately narrow: bubbletea's own loop
	// goroutines, not "anything that mentions bubbletea".
	case strings.Contains(lower, "bubbletea.(*program).handlecommands") ||
		strings.Contains(lower, "bubbletea.(*program).eventloop") ||
		strings.Contains(lower, "bubbletea.(*program).handleevents"):
		return "bubbletea_commands"
	default:
		return "other"
	}
}

// Snapshot captures a process snapshot at an App lifecycle boundary.
func (a *App) Snapshot() RuntimeSnapshot {
	a.t.Helper()
	return SnapshotRuntime()
}

// KubeconfigContext selects one context from a source kubeconfig and gives it
// a stable destination name. This avoids collisions when two proxy configs
// were derived from the same kind kubeconfig.
type KubeconfigContext struct {
	Name          string
	Kubeconfig    string
	SourceContext string
}

// BuildMergedKubeconfig builds an isolated kubeconfig containing exactly the
// selected contexts. Cluster and user keys are rewritten per destination
// context, so distinct endpoints and identities cannot overwrite each other.
func BuildMergedKubeconfig(t *testing.T, contexts ...KubeconfigContext) string {
	t.Helper()
	if len(contexts) == 0 {
		t.Fatal("BuildMergedKubeconfig requires at least one context")
	}
	out := clientcmdapi.NewConfig()
	for i, selected := range contexts {
		if selected.Name == "" {
			t.Fatalf("merged kubeconfig context %d has no destination name", i)
		}
		if _, exists := out.Contexts[selected.Name]; exists {
			t.Fatalf("duplicate merged kubeconfig context %q", selected.Name)
		}
		source, err := clientcmd.LoadFromFile(selected.Kubeconfig)
		if err != nil {
			t.Fatalf("loading merged kubeconfig source %s: %v", selected.Kubeconfig, err)
		}
		sourceName := selected.SourceContext
		if sourceName == "" {
			sourceName = source.CurrentContext
		}
		sourceContext := source.Contexts[sourceName]
		if sourceContext == nil {
			t.Fatalf("source kubeconfig %s has no context %q", selected.Kubeconfig, sourceName)
		}
		sourceCluster := source.Clusters[sourceContext.Cluster]
		if sourceCluster == nil {
			t.Fatalf("source context %q has no cluster %q", sourceName, sourceContext.Cluster)
		}

		clusterName := fmt.Sprintf("%s-cluster", selected.Name)
		userName := ""
		if sourceContext.AuthInfo != "" {
			sourceUser := source.AuthInfos[sourceContext.AuthInfo]
			if sourceUser == nil {
				t.Fatalf("source context %q has no user %q", sourceName, sourceContext.AuthInfo)
			}
			userName = fmt.Sprintf("%s-user", selected.Name)
			out.AuthInfos[userName] = sourceUser.DeepCopy()
		}
		out.Clusters[clusterName] = sourceCluster.DeepCopy()
		ctxCopy := sourceContext.DeepCopy()
		ctxCopy.Cluster = clusterName
		ctxCopy.AuthInfo = userName
		out.Contexts[selected.Name] = ctxCopy
		if i == 0 {
			out.CurrentContext = selected.Name
		}
	}
	path := t.TempDir() + "/merged.kubeconfig"
	if err := clientcmd.WriteToFile(*out, path); err != nil {
		t.Fatalf("writing merged kubeconfig: %v", err)
	}
	return path
}
