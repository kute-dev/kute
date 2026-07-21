package podlogs

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
)

// fakeRestartLister answers ListRaw with a single pod carrying one
// container's live restart count — letting tests simulate an actual
// container restart happening mid-stream (or not happening at all).
type fakeRestartLister struct {
	podName   string
	container string
	restarts  int32
}

func (f *fakeRestartLister) ListRaw(_ context.Context, kind kube.ResourceKind, _ string) ([]runtime.Object, error) {
	if kind != kube.KindPod {
		return nil, nil
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: f.podName},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: f.container}}},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{{
				Name:         f.container,
				RestartCount: f.restarts,
			}},
		},
	}
	return []runtime.Object{pod}, nil
}

// fakeStreamer replays connects[N] on the Nth call for a given container —
// letting tests simulate a container ending its stream and podlogs
// reconnecting to a "new" instance of it.
type fakeStreamer struct {
	mu        sync.Mutex
	connects  map[string][]string // container -> lines per successive connect
	callCount map[string]int
	err       error
	requests  []kube.LogStreamRequest
}

func (s *fakeStreamer) StreamPodLogs(_ context.Context, req kube.LogStreamRequest) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, req)
	if s.err != nil {
		return nil, s.err
	}
	if s.callCount == nil {
		s.callCount = map[string]int{}
	}
	lines := s.connects[req.Container]
	idx := s.callCount[req.Container]
	s.callCount[req.Container]++
	if idx >= len(lines) {
		return io.NopCloser(strings.NewReader("")), nil
	}
	return io.NopCloser(strings.NewReader(lines[idx])), nil
}

func TestStreamContainerEmitsLinesThenReconnectsWithBoundary(t *testing.T) {
	t.Parallel()

	streamer := &fakeStreamer{connects: map[string][]string{
		"app": {"first\nsecond\n", "third\n"},
	}}
	model := testModel()
	model.streamer = streamer
	model.pod.Restarts = 3

	ctx, cancel := context.WithCancel(context.Background())
	var entries []LogEntry
	err := model.streamContainer(ctx, "app", func(e LogEntry) bool {
		entries = append(entries, e)
		if len(entries) == 4 { // first, second, boundary, third
			cancel()
			return false
		}
		return true
	})
	if err != nil {
		t.Fatalf("streamContainer error = %v", err)
	}
	if len(entries) != 4 {
		t.Fatalf("entries = %+v", entries)
	}
	if entries[0].Message != "first" || entries[1].Message != "second" {
		t.Fatalf("first connect entries = %+v", entries[:2])
	}
	if !entries[2].Boundary || !strings.Contains(entries[2].Message, "restart 3") {
		t.Fatalf("boundary entry = %+v", entries[2])
	}
	if entries[3].Message != "third" {
		t.Fatalf("post-reconnect entry = %+v", entries[3])
	}

	if len(streamer.requests) != 2 {
		t.Fatalf("requests = %+v", streamer.requests)
	}
	if streamer.requests[0].TailLines != DefaultTailLines || streamer.requests[0].SinceSeconds == 0 {
		t.Fatalf("first request missing history window: %+v", streamer.requests[0])
	}
	if streamer.requests[1].TailLines != 0 || streamer.requests[1].SinceSeconds != 0 {
		t.Fatalf("reconnect request should not replay history: %+v", streamer.requests[1])
	}
}

// TestStreamContainerWaitsForActualRestartBeforeReconnecting is a regression
// test for the "kute constantly repeats a log line, k9s shows it once"
// symptom: a container that ends its stream naturally without actually
// restarting (e.g. CrashLoopBackOff's real backoff is far longer than
// reconnectDelay) must not be reconnected to — that just replays the same
// terminated instance's full log again. With a lister available,
// streamContainer must wait for the container's live restart count to
// actually move before opening a new connection.
func TestStreamContainerWaitsForActualRestartBeforeReconnecting(t *testing.T) {
	t.Parallel()

	streamer := &fakeStreamer{connects: map[string][]string{
		"app": {"first\n", "second\n"},
	}}
	model := testModel()
	model.streamer = streamer
	model.lister = &fakeRestartLister{podName: "api", container: "app", restarts: 3}

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(700*time.Millisecond, cancel) // let one reconnectDelay tick pass with no restart

	var entries []LogEntry
	err := model.streamContainer(ctx, "app", func(e LogEntry) bool {
		entries = append(entries, e)
		return true
	})
	if err != nil {
		t.Fatalf("streamContainer error = %v", err)
	}
	if len(entries) != 1 || entries[0].Message != "first" {
		t.Fatalf("entries = %+v, want only the first connect's line while restart count is unchanged", entries)
	}
	if len(streamer.requests) != 1 {
		t.Fatalf("requests = %+v, want exactly one connect attempt while restart count is unchanged", streamer.requests)
	}
}

// TestStreamContainerReconnectsAfterActualRestartDetected confirms the other
// half: once the container's live restart count actually moves, streamContainer
// still reconnects and synthesizes a boundary entry carrying the new count.
func TestStreamContainerReconnectsAfterActualRestartDetected(t *testing.T) {
	t.Parallel()

	streamer := &fakeStreamer{connects: map[string][]string{
		"app": {"first\n", "second\n"},
	}}
	lister := &fakeRestartLister{podName: "api", container: "app", restarts: 3}
	model := testModel()
	model.streamer = streamer
	model.lister = lister

	ctx, cancel := context.WithCancel(context.Background())
	var entries []LogEntry
	err := model.streamContainer(ctx, "app", func(e LogEntry) bool {
		entries = append(entries, e)
		if e.Message == "first" {
			lister.restarts = 4 // the real restart happens now
		}
		if len(entries) == 3 { // first, boundary, second
			cancel()
			return false
		}
		return true
	})
	if err != nil {
		t.Fatalf("streamContainer error = %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("entries = %+v", entries)
	}
	if !entries[1].Boundary || !strings.Contains(entries[1].Message, "restart 4") {
		t.Fatalf("boundary entry = %+v, want restart count from the live restart", entries[1])
	}
	if entries[2].Message != "second" {
		t.Fatalf("post-reconnect entry = %+v", entries[2])
	}
}

func TestStreamContainerStopsOnContextCancel(t *testing.T) {
	t.Parallel()

	streamer := &fakeStreamer{connects: map[string][]string{"app": {"only\n"}}}
	model := testModel()
	model.streamer = streamer

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := model.streamContainer(ctx, "app", func(LogEntry) bool { return true })
	if err != nil {
		t.Fatalf("streamContainer error = %v", err)
	}
}

// TestStreamContainerStopsOnUnretrievableLogsLine is a regression test for a
// terminated/GC'd container: the kubelet logs endpoint answers with a 200 OK
// whose body is just "unable to retrieve container logs for containerd://…"
// (a long-standing kubelet quirk — never surfaces as an HTTP error client-go
// can see). Before this fix, streamContainer read that line as ordinary log
// content and reconnected every 500ms forever, spamming the buffer with the
// same line on an endless loop. It must instead surface once as an error.
func TestStreamContainerStopsOnUnretrievableLogsLine(t *testing.T) {
	t.Parallel()

	streamer := &fakeStreamer{connects: map[string][]string{
		"app": {
			"unable to retrieve container logs for containerd://f6095a3ed59be25aaf5ed084fbf43be5b124782cb2bfb1ef56e6bc4e7afdcaad\n",
			"unable to retrieve container logs for containerd://f6095a3ed59be25aaf5ed084fbf43be5b124782cb2bfb1ef56e6bc4e7afdcaad\n",
		},
	}}
	model := testModel()
	model.streamer = streamer

	var entries []LogEntry
	err := model.streamContainer(context.Background(), "app", func(e LogEntry) bool {
		entries = append(entries, e)
		return true
	})
	if err == nil || !strings.Contains(err.Error(), "unable to retrieve container logs for") {
		t.Fatalf("err = %v, want the unretrievable-logs line surfaced as an error", err)
	}
	if len(entries) != 0 {
		t.Fatalf("entries = %+v, want none emitted as log content", entries)
	}
	if len(streamer.requests) != 1 {
		t.Fatalf("requests = %+v, want exactly one connect attempt (no reconnect loop)", streamer.requests)
	}
}

func TestStreamContainerReturnsConnectError(t *testing.T) {
	t.Parallel()

	model := testModel()
	model.streamer = &fakeStreamer{err: errors.New("boom")}
	err := model.streamContainer(context.Background(), "app", func(LogEntry) bool { return true })
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v", err)
	}
}

func TestCurrentRestartCountFallsBackWithoutLister(t *testing.T) {
	t.Parallel()

	model := testModel()
	model.pod.Restarts = 7
	if got := model.currentRestartCount(context.Background()); got != 7 {
		t.Fatalf("restart count = %d, want 7", got)
	}
}

func TestRunStreamValidation(t *testing.T) {
	t.Parallel()

	model := testModel()
	model.pod.Name = ""
	msgs := collectRunStream(model)
	if _, ok := msgs[0].(streamErrorMsg); !ok {
		t.Fatalf("missing pod name did not produce streamErrorMsg: %+v", msgs)
	}

	model = testModel()
	model.pod.Containers = nil
	msgs = collectRunStream(model)
	if _, ok := msgs[0].(streamEmptyMsg); !ok {
		t.Fatalf("missing containers did not produce streamEmptyMsg: %+v", msgs)
	}

	model = testModel()
	model.streamer = nil
	msgs = collectRunStream(model)
	if _, ok := msgs[0].(streamErrorMsg); !ok {
		t.Fatalf("nil streamer did not produce streamErrorMsg: %+v", msgs)
	}
}

func collectRunStream(model Model) []tea.Msg {
	ch := make(chan tea.Msg, 16)
	model.runStream(context.Background(), 1, ch)
	var out []tea.Msg
	for msg := range ch {
		out = append(out, msg)
	}
	return out
}
