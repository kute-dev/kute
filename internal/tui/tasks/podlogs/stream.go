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
	"github.com/kute-dev/kute/internal/resources"
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

// containerReadyMsg and containerWaitingMsg are checkContainerCmd's two
// outcomes — see beginStream's doc comment for the flow they drive.
type containerReadyMsg struct{ streamID int }
type containerWaitingMsg struct {
	streamID int
	reason   string
}

// containerCheckTimeout bounds checkContainerCmd's ListRaw call — the pod
// cache is a local informer read (no network), so this is just a safety net
// against a misbehaving lister, not a latency budget that matters in
// practice.
const containerCheckTimeout = 10 * time.Second

// beginStream is the single entry point for (re)starting the active
// container's stream — Start() (cold open) and the tab/s key handlers
// (container/since change) all go through it, replacing the old
// restartStream. It resets the same presentation state restartStream
// always has, then either connects immediately (no lister to check
// against — preserves pre-existing behavior for callers/tests with none
// wired) or checks the container's live cached state first via
// checkContainerCmd: a container that hasn't started yet parks in
// StreamWaitingForContainer (see Update's containerWaitingMsg case)
// instead of attempting to stream and erroring on the kubelet's "is
// waiting to start" response. The wait is resolved by the next
// kube.ResourceChangedMsg{Kind: KindPod} the root shell already forwards
// to every task (mirrors poddetail's CONTAINERS grid) — not a poll.
func (m *Model) beginStream(state StreamState) tea.Cmd {
	m.cancelStream()
	m.streamID++
	m.rateGen++
	m.stream = state
	m.feedback = "Loading logs for " + m.scope() + "..."
	m.lastError = ""
	m.permDenied = false
	m.waitingReason = ""
	m.buffer.Entries = nil
	m.buffer.DroppedCount = 0
	m.view.VerticalOffset = 0
	m.linesSinceTick = 0
	m.lastRate = 0

	streamID := m.streamID
	if m.lister == nil {
		return m.connect(streamID)
	}
	return m.checkContainerCmd(streamID)
}

// connect actually spins up the streaming goroutine — reached either
// directly from beginStream (no lister to pre-check with) or once
// containerReadyMsg confirms the active container is no longer waiting. A
// stale streamID (a later beginStream superseded this one while a check
// was in flight, e.g. the user pressed tab again) is a no-op.
func (m *Model) connect(streamID int) tea.Cmd {
	if streamID != m.streamID {
		return nil
	}
	m.streamCh = make(chan tea.Msg, 128)
	// Parented on the session's cluster context, not Background: this one is
	// open-ended by design (a follow stream has no timeout to expire), so
	// without a cancellable parent the goroutine and its HTTP connection
	// outlive both quit and a context switch — the screen's own
	// m.streamCancel only fires when the user leaves the screen.
	ctx, cancel := context.WithCancel(m.session.ClusterContext())
	m.streamCancel = cancel
	model := *m
	go model.runStream(ctx, streamID, m.streamCh)
	return tea.Batch(waitForStream(m.streamCh), rateTickCmd(m.rateGen), m.spinner.Tick)
}

// checkContainerCmd reads the active container's live state from the pod
// cache and reports whether it's safe to connect. A lookup failure (lister
// error, pod/container not found — e.g. a momentary cache hiccup) degrades
// to "ready": it never blocks the screen on something that isn't a
// positively-observed wait, matching the old behavior of just attempting
// to stream.
func (m Model) checkContainerCmd(streamID int) tea.Cmd {
	container, ok := m.activeContainer()
	if !ok {
		return func() tea.Msg { return streamEmptyMsg{streamID: streamID} }
	}
	lister := m.lister
	namespace := m.pod.Namespace
	podName := m.pod.Name
	parent := m.session.ClusterContext()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, containerCheckTimeout)
		defer cancel()
		info, found := lookupContainerInfo(ctx, lister, namespace, podName, container)
		if found && info.State != "" && info.State != "Waiting" {
			return containerReadyMsg{streamID: streamID}
		}
		if !found {
			return containerReadyMsg{streamID: streamID}
		}
		return containerWaitingMsg{streamID: streamID, reason: info.Reason}
	}
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
// on esc/quit/beginStream) or a genuine error occurs — and reports the
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
		if kube.IsContainerNotStartedError(err) {
			// The race window beginStream's pre-check can't fully close:
			// the container flipped back to waiting between that check and
			// this connect attempt. Treat it the same as a positive
			// checkContainerCmd observation rather than a fatal error — the
			// goroutine just ends, and the next Pod-cache change event
			// resumes the check (see Update's containerWaitingMsg/
			// kube.ResourceChangedMsg cases).
			ch <- containerWaitingMsg{streamID: streamID, reason: "starting"}
			return
		}
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
	fallbackAttempted := false
	liveStarted := false
	lastRestarts, tracking := m.containerRestartCount(ctx, container)
	for {
		initialRequest := first
		fallbackConnection := fallbackAttempted
		historyConnection := initialRequest || fallbackConnection
		reconnect := liveStarted && !historyConnection
		req := kube.LogStreamRequest{
			Namespace:  m.pod.Namespace,
			PodName:    m.pod.Name,
			Container:  container,
			Follow:     !historyConnection,
			Timestamps: true,
		}
		if initialRequest {
			req.TailLines = m.tailLines
			req.SinceSeconds = m.sinceSeconds()
		} else if fallbackAttempted {
			// A one-page historical read is the explicit fallback for an empty
			// since-window. It must not be repeated as a live reconnect request.
			req.TailLines = m.tailLines
		} else if !liveStarted {
			// The history request is finite; follow from just after it so the
			// viewer does not wait forever for an empty since-window. A one-second
			// overlap avoids missing lines emitted between the two requests.
			req.SinceSeconds = 1
		}
		fallbackAttempted = false
		reader, err := m.streamer.StreamPodLogs(ctx, req)
		if err != nil {
			return err
		}

		if reconnect {
			restarts := lastRestarts
			if !tracking {
				restarts = m.currentRestartCount(ctx)
			}
			if !emit(m.boundaryEntry(container, restarts)) {
				_ = reader.Close()
				return nil
			}
		}
		if !historyConnection {
			liveStarted = true
		}
		first = false

		var unretrievable string
		connectionEntries := 0
		scanErr := kube.ScanLogLines(ctx, reader, func(line string) bool {
			ts, msg := splitTimestamp(line)
			if isUnretrievableLogsLine(msg) {
				unretrievable = msg
				return false
			}
			connectionEntries++
			return emit(LogEntry{Container: container, Timestamp: ts, Message: msg, Severity: parseSeverity(msg)})
		})
		_ = reader.Close()
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
			//nolint:nilerr // a cancelled context is the viewer closing, not a
			// stream failure — reporting it would put an error banner on a
			// screen the user just left.
			return nil
		}
		if initialRequest && connectionEntries == 0 && m.sinceSeconds() > 0 {
			fallbackAttempted = true
			continue
		}
		if historyConnection {
			if connectionEntries == 0 {
				return nil
			}
			continue
		}

		if tracking {
			// A stream that ends naturally usually means the container
			// restarted — but not always: a container the kubelet still
			// reports as the same (not-yet-restarted) instance answers a
			// reconnect with its full log again (Follow just replays to EOF
			// and closes, since nothing new is coming from a dead process).
			// CrashLoopBackOff's actual restart delay is typically far longer
			// than reconnectDelay, so blindly reconnecting every 500ms
			// replayed the same lines on a tight loop — the "logs repeat
			// constantly" symptom. Wait for the live restart count to
			// actually move before reconnecting.
			count, ok := m.waitForContainerRestart(ctx, container, lastRestarts)
			if !ok {
				return nil
			}
			lastRestarts = count
			continue
		}

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(reconnectDelay):
		}
	}
}

// waitForContainerRestart polls container's live restart count (via lister)
// every reconnectDelay until it exceeds last — a genuine restart — or ctx is
// cancelled. Returns the new count and true, or 0 and false on cancellation.
func (m Model) waitForContainerRestart(ctx context.Context, container string, last int32) (int32, bool) {
	for {
		select {
		case <-ctx.Done():
			return 0, false
		case <-time.After(reconnectDelay):
		}
		if count, ok := m.containerRestartCount(ctx, container); ok && count > last {
			return count, true
		}
	}
}

// lookupContainerInfo reads container's own live status from the pod
// cache via lister — the ListRaw-and-find-this-pod-and-container dance
// shared by containerRestartCount and checkContainerCmd. ok is false
// whenever the answer can't be determined at all (no lister, list failure,
// pod/container not found) — callers each have their own fail-open
// fallback for that case.
func lookupContainerInfo(ctx context.Context, lister resources.RawLister, namespace, podName, container string) (kube.ContainerInfo, bool) {
	if lister == nil {
		return kube.ContainerInfo{}, false
	}
	objs, err := lister.ListRaw(ctx, kube.KindPod, namespace)
	if err != nil {
		return kube.ContainerInfo{}, false
	}
	for _, obj := range objs {
		p, ok := obj.(*corev1.Pod)
		if !ok || p.Name != podName {
			continue
		}
		for _, ci := range kube.PodFromObject(p).ContainerInfos {
			if ci.Name == container {
				return ci, true
			}
		}
		return kube.ContainerInfo{}, false
	}
	return kube.ContainerInfo{}, false
}

// containerRestartCount reads container's own live restart count —
// distinct from currentRestartCount's pod-level sum across every
// container, since only this container's own count tells us whether *it*
// has actually restarted. ok is false whenever that can't be determined —
// callers fall back to the blind reconnect-after-delay behavior in that
// case.
func (m Model) containerRestartCount(ctx context.Context, container string) (int32, bool) {
	info, ok := lookupContainerInfo(ctx, m.lister, m.pod.Namespace, m.pod.Name, container)
	if !ok {
		return 0, false
	}
	return info.Restarts, true
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

// boundaryEntry synthesizes a restart marker carrying restarts, the live
// count the caller already resolved (containerRestartCount when tracking,
// else currentRestartCount's pod-level fallback).
func (m Model) boundaryEntry(container string, restarts int32) LogEntry {
	return LogEntry{
		Container: container,
		Boundary:  true,
		Timestamp: time.Now().Format("15:04:05"),
		Message:   fmt.Sprintf("container restarted · restart %d", restarts),
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
