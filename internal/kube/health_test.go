package kube

import (
	"errors"
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
	h.onWatchError(errors.New("dial tcp: i/o timeout"))

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
	h.onWatchError(errors.New("first error"))
	first := h.get()

	h.onWatchError(errors.New("second error, same outage"))
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
	h.onWatchError(errors.New("boom"))
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
