package podlogs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	corev1 "k8s.io/api/core/v1"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui/components"
)

// reconnectDelay is the pause before re-opening a container's log stream
// after it ends naturally (docs/design README.md §5b's "restart boundary" —
// a real container restart is the common cause, but any benign stream end
// looks the same to the client, so this also covers e.g. an apiserver
// connection drop). Bounds how fast a persistently-cycling container is
// re-queried.
const reconnectDelay = 500 * time.Millisecond

type streamStartedMsg struct{ state StreamState }
type logLineMsg struct {
	streamID int
	entry    LogEntry
}
type streamErrorMsg struct {
	streamID int
	err      error
}
type streamEmptyMsg struct{ streamID int }
type streamClosedMsg struct{ streamID int }
type streamWaitMsg struct{}
type rateTickMsg struct{ gen int }

func (m *Model) restartStream(state StreamState) tea.Cmd {
	m.cancelStream()
	m.streamID++
	m.rateGen++
	m.stream = state
	m.feedback = "Loading logs for " + m.scope() + "..."
	m.lastError = ""
	m.permDenied = false
	m.buffer.Entries = nil
	m.buffer.DroppedCount = 0
	m.view.VerticalOffset = 0
	m.linesSinceTick = 0
	m.lastRate = 0
	m.streamCh = make(chan tea.Msg, 128)
	ctx, cancel := context.WithCancel(context.Background())
	m.streamCancel = cancel
	streamID := m.streamID
	model := *m
	go model.runStream(ctx, streamID, m.streamCh)
	return tea.Batch(waitForStream(m.streamCh), rateTickCmd(m.rateGen), components.SpinnerTick())
}

func rateTickCmd(gen int) tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return rateTickMsg{gen: gen} })
}

func waitForStream(ch <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return streamWaitMsg{}
		}
		return msg
	}
}

func (m Model) nextStreamCmd() tea.Cmd {
	if m.streamCh == nil {
		return nil
	}
	return waitForStream(m.streamCh)
}

// runStream drives the active container's reconnect loop (streamContainer)
// to completion — which only happens once ctx is cancelled (cancelStream,
// on esc/quit/restartStream) or a genuine error occurs — and reports the
// outcome down ch.
func (m Model) runStream(ctx context.Context, streamID int, ch chan<- tea.Msg) {
	defer close(ch)
	if strings.TrimSpace(m.pod.Name) == "" {
		ch <- streamErrorMsg{streamID: streamID, err: errors.New("pod name is required for log streaming")}
		return
	}
	container, ok := m.activeContainer()
	if !ok {
		ch <- streamEmptyMsg{streamID: streamID}
		return
	}
	if m.streamer == nil {
		ch <- streamErrorMsg{streamID: streamID, err: errors.New("pod log streamer is not configured")}
		return
	}

	count := 0
	err := m.streamContainer(ctx, container, func(entry LogEntry) bool {
		count++
		select {
		case <-ctx.Done():
			return false
		case ch <- logLineMsg{streamID: streamID, entry: entry}:
			return true
		}
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		ch <- streamErrorMsg{streamID: streamID, err: fmt.Errorf("stream logs for %s: %w", m.scope(), err)}
		return
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		ch <- streamClosedMsg{streamID: streamID}
		return
	}
	if count == 0 {
		ch <- streamEmptyMsg{streamID: streamID}
		return
	}
	ch <- streamClosedMsg{streamID: streamID}
}

// streamContainer follows container, reconnecting whenever the underlying
// stream ends naturally (not via ctx cancellation) — the common cause is
// the container restarting, so every reconnect past the first synthesizes
// a boundary entry (docs/design README.md §5b's "restart boundaries").
// TailLines/SinceSeconds (the initial history window) only apply to the
// first connection — a reconnected container is a fresh process, so there
// is no "since" continuity with what came before.
func (m Model) streamContainer(ctx context.Context, container string, emit func(LogEntry) bool) error {
	first := true
	for {
		req := kube.LogStreamRequest{
			Namespace:  m.pod.Namespace,
			PodName:    m.pod.Name,
			Container:  container,
			Timestamps: true,
		}
		if first {
			req.TailLines = m.tailLines
			req.SinceSeconds = m.sinceSeconds()
		}
		reader, err := m.streamer.StreamPodLogs(ctx, req)
		if err != nil {
			return err
		}

		if !first {
			if !emit(m.boundaryEntry(ctx, container)) {
				reader.Close()
				return nil
			}
		}
		first = false

		var unretrievable string
		scanErr := kube.ScanLogLines(ctx, reader, func(line string) bool {
			ts, msg := splitTimestamp(line)
			if isUnretrievableLogsLine(msg) {
				unretrievable = msg
				return false
			}
			return emit(LogEntry{Container: container, Timestamp: ts, Message: msg, Severity: parseSeverity(msg)})
		})
		reader.Close()
		if unretrievable != "" {
			// The kubelet logs endpoint answers a terminated/GC'd container's
			// logs with a 200 OK whose body is just this one line (a
			// long-standing kubelet quirk — the error never surfaces as an
			// HTTP-level failure client-go can see). Left alone, streamContainer
			// would read this same line, reconnect after reconnectDelay, and
			// repeat forever — the "constantly logs unable to retrieve
			// container logs" symptom. Treat it as fatal so it surfaces once as
			// an error instead of spamming the reconnect loop.
			return errors.New(unretrievable)
		}
		if scanErr != nil && !errors.Is(scanErr, context.Canceled) {
			return scanErr
		}
		if ctx.Err() != nil {
			return nil
		}

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(reconnectDelay):
		}
	}
}

// unretrievableLogsPrefix is the kubelet containerLogs handler's own error
// text for a container whose log file is gone (already garbage-collected, or
// the container never wrote one) — written straight into a 200 OK body
// rather than returned as an HTTP error, so client-go's Stream() call
// reports success. streamContainer's scan loop watches for it explicitly.
const unretrievableLogsPrefix = "unable to retrieve container logs for"

func isUnretrievableLogsLine(msg string) bool {
	return strings.HasPrefix(strings.TrimSpace(msg), unretrievableLogsPrefix)
}

// boundaryEntry synthesizes a restart marker, with the pod's current
// restart count refreshed via lister when available (falls back to the
// count captured when the screen opened).
func (m Model) boundaryEntry(ctx context.Context, container string) LogEntry {
	return LogEntry{
		Container: container,
		Boundary:  true,
		Timestamp: time.Now().Format("15:04:05"),
		Message:   fmt.Sprintf("container restarted · restart %d", m.currentRestartCount(ctx)),
	}
}

func (m Model) currentRestartCount(ctx context.Context) int32 {
	if m.lister == nil {
		return m.pod.Restarts
	}
	objs, err := m.lister.ListRaw(ctx, kube.KindPod, m.pod.Namespace)
	if err != nil {
		return m.pod.Restarts
	}
	for _, obj := range objs {
		if p, ok := obj.(*corev1.Pod); ok && p.Name == m.pod.Name {
			return kube.PodFromObject(p).Restarts
		}
	}
	return m.pod.Restarts
}
