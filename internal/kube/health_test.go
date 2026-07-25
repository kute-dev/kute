package kube

import (
	"context"
	"errors"
	"net"
	"net/url"
	"testing"
	"time"
)

func TestBackoffDelaySchedule(t *testing.T) {
	t.Parallel()
	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{0, time.Second},
		{1, time.Second},
		{2, 2 * time.Second},
		{3, 4 * time.Second},
		{4, 8 * time.Second},
		{5, 16 * time.Second},
		{6, 30 * time.Second}, // 32s would exceed the cap
		{7, 30 * time.Second},
		{100, 30 * time.Second},
	}
	for _, tt := range tests {
		if got := backoffDelay(tt.attempt); got != tt.want {
			t.Errorf("backoffDelay(%d) = %v, want %v", tt.attempt, got, tt.want)
		}
	}
}

func TestHealthOnWatchErrorFlipsToReconnecting(t *testing.T) {
	t.Parallel()
	h := newHealth()
	h.onWatchError(errors.New("dial tcp: i/o timeout"), true, time.Now())

	got := h.get()
	if got.Phase != ConnReconnecting {
		t.Fatalf("Phase = %v, want Reconnecting", got.Phase)
	}
	if got.Err != "dial tcp: i/o timeout" {
		t.Fatalf("Err = %q, want verbatim error", got.Err)
	}
	if got.Attempt != 1 {
		t.Fatalf("Attempt = %d, want 1", got.Attempt)
	}
	if !got.NextRetryAt.After(time.Now()) {
		t.Fatalf("NextRetryAt should be in the future")
	}
}

func TestHealthOnWatchErrorDoesNotResetAttemptWhileReconnecting(t *testing.T) {
	t.Parallel()
	h := newHealth()
	h.onWatchError(errors.New("first error"), true, time.Now())
	first := h.get()

	h.onWatchError(errors.New("second error, same outage"), true, time.Now())
	second := h.get()

	if second.Attempt != first.Attempt {
		t.Fatalf("Attempt changed from %d to %d on a repeated watch error mid-outage", first.Attempt, second.Attempt)
	}
	if second.Err != first.Err {
		t.Fatalf("Err should stay from the first watch error, got %q", second.Err)
	}
}

func TestHealthSetEmitsOnChannel(t *testing.T) {
	t.Parallel()
	h := newHealth()
	h.set(ConnState{Phase: ConnFailed})

	select {
	case msg := <-h.ch:
		if msg.Phase != ConnFailed {
			t.Fatalf("got phase %v, want Failed", msg.Phase)
		}
	default:
		t.Fatalf("expected a ConnStateMsg on the channel")
	}
}

func TestHealthSetDropsOldestWhenChannelFull(t *testing.T) {
	t.Parallel()
	h := newHealth()
	// Fill the buffered channel (capacity 8) past its limit.
	for i := 0; i < 10; i++ {
		h.set(ConnState{Phase: ConnConnected, Attempt: i})
	}
	// The struct's own state must reflect the latest set regardless of
	// channel backpressure.
	if got := h.get().Attempt; got != 9 {
		t.Fatalf("get().Attempt = %d, want 9 (latest)", got)
	}
}

func TestHealthRetryNowIsNonBlocking(t *testing.T) {
	t.Parallel()
	h := newHealth()
	h.retryNow()
	h.retryNow() // second call must not block even though the buffer is 1
	select {
	case <-h.retry:
	default:
		t.Fatalf("expected a pending retry signal")
	}
}

func TestNewHealthStartsConnected(t *testing.T) {
	t.Parallel()
	h := newHealth()
	got := h.get()
	if got.Phase != ConnConnected {
		t.Fatalf("initial Phase = %v, want Connected", got.Phase)
	}
	if got.FetchedAt.IsZero() {
		t.Fatalf("expected FetchedAt to be stamped")
	}
}

// timeoutErr mimics what client-go hands back when the ping's own context
// deadline fires: the transport error wrapped in a *url.Error.
func timeoutErr() error {
	return &url.Error{
		Op:  "Get",
		URL: "https://localhost:10443/livez",
		Err: context.DeadlineExceeded,
	}
}

func TestIsTimeout(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"bare deadline exceeded", context.DeadlineExceeded, true},
		{"wrapped in url.Error", timeoutErr(), true},
		{"net.Error reporting timeout", &net.DNSError{IsTimeout: true}, true},
		{"connection refused", errors.New("dial tcp 127.0.0.1:10443: connect: connection refused"), false},
		{"unauthorized", errors.New("the server has asked for the client to provide credentials"), false},
	}
	for _, tt := range tests {
		if got := isTimeout(tt.err); got != tt.want {
			t.Errorf("isTimeout(%s) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// TestRecordPingForgivesTimeoutDuringInitialSync is the SSH-tunnel fix: while
// the informer caches are still syncing, Start()'s 21 cluster-wide LISTs
// saturate the link and the /livez ping blows its deadline. That must not
// surface as an outage.
func TestRecordPingForgivesTimeoutDuringInitialSync(t *testing.T) {
	t.Parallel()
	h := newHealth()
	h.recordPing(pingTimeout, timeoutErr(), false, h.startedAt.Add(30*time.Second))

	if got := h.get(); got.Phase != ConnConnected {
		t.Fatalf("Phase = %v, want Connected (timeout mid-sync is startup congestion)", got.Phase)
	}
	select {
	case msg := <-h.ch:
		t.Fatalf("a forgiven ping must not emit a ConnStateMsg, got %+v", msg)
	default:
	}
}

func TestRecordPingReportsTimeoutOnceCachesSynced(t *testing.T) {
	t.Parallel()
	h := newHealth()
	h.recordPing(pingTimeout, timeoutErr(), true, h.startedAt.Add(30*time.Second))

	got := h.get()
	if got.Phase != ConnReconnecting {
		t.Fatalf("Phase = %v, want Reconnecting (post-sync timeout is a real outage)", got.Phase)
	}
	if got.Attempt != 1 {
		t.Fatalf("Attempt = %d, want 1", got.Attempt)
	}
}

func TestRecordPingReportsTimeoutPastConnectGrace(t *testing.T) {
	t.Parallel()
	h := newHealth()
	h.recordPing(pingTimeout, timeoutErr(), false, h.startedAt.Add(connectGrace+time.Second))

	if got := h.get(); got.Phase != ConnReconnecting {
		t.Fatalf("Phase = %v, want Reconnecting — the grace window is bounded, not open-ended", got.Phase)
	}
}

// TestRecordPingReportsNonTimeoutImmediately pins the 4c path: an unreachable
// context still has to reach the setup screen on the first ping, so only
// timeouts are forgiven.
func TestRecordPingReportsNonTimeoutImmediately(t *testing.T) {
	t.Parallel()
	h := newHealth()
	refused := errors.New("dial tcp 127.0.0.1:10443: connect: connection refused")
	h.recordPing(time.Millisecond, refused, false, h.startedAt.Add(2*time.Second))

	got := h.get()
	if got.Phase != ConnReconnecting {
		t.Fatalf("Phase = %v, want Reconnecting on a refused connection mid-sync", got.Phase)
	}
	if got.Err != refused.Error() {
		t.Fatalf("Err = %q, want the verbatim error", got.Err)
	}
}

// TestRecordPingAdvancesAttemptMidOutage: post-sync, a timeout during an
// outage already in progress keeps advancing the attempt/backoff.
func TestRecordPingAdvancesAttemptMidOutage(t *testing.T) {
	t.Parallel()
	h := newHealth()
	h.onWatchError(errors.New("watch dropped"), true, time.Now())
	h.recordPing(pingTimeout, timeoutErr(), true, time.Now())

	got := h.get()
	if got.Phase != ConnReconnecting {
		t.Fatalf("Phase = %v, want Reconnecting", got.Phase)
	}
	if got.Attempt != 2 {
		t.Fatalf("Attempt = %d, want 2 (the outage's attempts keep accruing)", got.Attempt)
	}
}

// TestConnectGraceIsPhaseBlind pins the deliberate choice in inConnectGrace:
// a lone watch error early in the sync must not re-arm the banner storm for
// every congested ping that follows it.
func TestConnectGraceIsPhaseBlind(t *testing.T) {
	t.Parallel()
	h := newHealth()
	// A reflector LIST times out mid-sync and flips us to Reconnecting...
	h.onWatchError(timeoutErr(), true, time.Now())
	if h.get().Phase != ConnReconnecting {
		t.Fatalf("precondition: expected the watch error to flip to Reconnecting")
	}
	before := h.get()

	// ...but the congested pings behind it are still forgiven while syncing.
	h.recordPing(pingTimeout, timeoutErr(), false, h.startedAt.Add(5*time.Second))

	if got := h.get(); got.Attempt != before.Attempt {
		t.Fatalf("Attempt advanced %d→%d on a forgiven mid-sync ping", before.Attempt, got.Attempt)
	}
}

// TestOnWatchErrorForgivesTimeoutDuringInitialSync: the reflector's own LIST
// competing with twenty siblings for a constrained link is congestion, not an
// outage — same rule the ping path follows.
func TestOnWatchErrorForgivesTimeoutDuringInitialSync(t *testing.T) {
	t.Parallel()
	h := newHealth()
	h.onWatchError(timeoutErr(), false, h.startedAt.Add(10*time.Second))

	if got := h.get(); got.Phase != ConnConnected {
		t.Fatalf("Phase = %v, want Connected (LIST timeout mid-sync is congestion)", got.Phase)
	}
}

// TestOnWatchErrorReportsNonTimeoutDuringInitialSync: a genuinely broken link
// still flips immediately, even mid-sync.
func TestOnWatchErrorReportsNonTimeoutDuringInitialSync(t *testing.T) {
	t.Parallel()
	h := newHealth()
	h.onWatchError(errors.New("connect: connection refused"), false, h.startedAt.Add(time.Second))

	if got := h.get(); got.Phase != ConnReconnecting {
		t.Fatalf("Phase = %v, want Reconnecting on a refused connection", got.Phase)
	}
}

func TestRecordPingSuccessClearsOutage(t *testing.T) {
	t.Parallel()
	h := newHealth()
	h.onWatchError(errors.New("watch dropped"), true, time.Now())
	h.recordPing(226*time.Millisecond, nil, true, time.Now())

	got := h.get()
	if got.Phase != ConnConnected {
		t.Fatalf("Phase = %v, want Connected", got.Phase)
	}
	if got.Latency != 226*time.Millisecond {
		t.Fatalf("Latency = %v, want the measured round trip", got.Latency)
	}
	if got.Attempt != 0 {
		t.Fatalf("Attempt = %d, want 0 after recovery", got.Attempt)
	}
}

// TestHealthResetRestampsConnectGrace: a SwitchContext into a slow cluster
// gets the same startup forgiveness a cold launch does.
func TestHealthResetRestampsConnectGrace(t *testing.T) {
	t.Parallel()
	h := newHealth()
	h.startedAt = time.Now().Add(-time.Hour) // pretend the process is old

	h.reset()
	h.recordPing(pingTimeout, timeoutErr(), false, time.Now())

	if got := h.get(); got.Phase != ConnConnected {
		t.Fatalf("Phase = %v, want Connected — reset() must restamp the grace window", got.Phase)
	}
}

// TestHealthResetPreservesChannelIdentity pins the SwitchContext fix
// (cluster.go now calls health.reset() instead of replacing health
// wholesale via newHealth()): a caller already ranging over the original
// ch (app.RunWithConfig's forwardEvents, which reads ConnEvents() once at
// program start) must keep receiving events after a context switch, which
// only holds if reset() reuses the same channel rather than building a new
// health struct.
func TestHealthResetPreservesChannelIdentity(t *testing.T) {
	t.Parallel()
	h := newHealth()
	h.onWatchError(errors.New("boom"), true, time.Now())
	origCh, origRetry := h.ch, h.retry

	h.reset()

	if h.ch != origCh {
		t.Fatalf("reset() replaced ch — a reader from before the reset is now orphaned")
	}
	if h.retry != origRetry {
		t.Fatalf("reset() replaced retry — RetryNow()'s existing signal path is now orphaned")
	}
	got := h.get()
	if got.Phase != ConnConnected {
		t.Fatalf("Phase after reset = %v, want Connected", got.Phase)
	}
	if got.Attempt != 0 {
		t.Fatalf("Attempt after reset = %d, want 0", got.Attempt)
	}
}
