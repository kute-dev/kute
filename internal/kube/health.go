package kube

import (
	"context"
	"sync"
	"time"

	"k8s.io/client-go/tools/cache"
)

// ConnPhase is the cluster connection's state machine (mvp-plan.md §0.7).
type ConnPhase string

const (
	ConnConnected    ConnPhase = "connected"
	ConnReconnecting ConnPhase = "reconnecting"
	ConnFailed       ConnPhase = "failed"
	ConnNoCluster    ConnPhase = "no-cluster"
)

// ConnState is a snapshot of connection health, fanned out on ConnStateMsg
// for the 4a offline banner/stale strip.
type ConnState struct {
	Phase       ConnPhase
	Latency     time.Duration
	Err         string // verbatim error for the 4a banner
	Attempt     int
	NextRetryAt time.Time
	FetchedAt   time.Time // last successful sync, for the ⧗ stale stamp
}

// ConnStateMsg is emitted (as a tea.Msg, like ResourceChangedMsg) whenever
// ConnState changes.
type ConnStateMsg ConnState

// Offline reports whether Phase is a mid-outage state (watch/ping failing,
// backoff retries under way) rather than a one-shot failure — the single
// predicate every "disconnected"/OFFLINE treatment (4a banner, header badge,
// mutating-verb gate) shares.
func (s ConnState) Offline() bool {
	return s.Phase == ConnReconnecting || s.Phase == ConnFailed
}

const (
	pingInterval   = 2 * time.Second // matches the "sync 2s" header chip
	pingTimeout    = 3 * time.Second
	maxBackoff     = 30 * time.Second
	initialBackoff = time.Second
)

// backoffDelay is the reconnect wait for the given attempt (1-based):
// 1s→2s→4s→…→30s, capped. attempt <= 1 returns the initial delay.
func backoffDelay(attempt int) time.Duration {
	if attempt <= 1 {
		return initialBackoff
	}
	d := initialBackoff
	for i := 1; i < attempt; i++ {
		d *= 2
		if d >= maxBackoff {
			return maxBackoff
		}
	}
	return d
}

// health owns ConnState and its change channel. It's a separate type (not
// fields directly on Cluster) so the transition logic is easy to unit test
// without a live clientset.
type health struct {
	mu    sync.Mutex
	state ConnState
	ch    chan ConnStateMsg
	retry chan struct{}
}

func newHealth() *health {
	return &health{
		state: ConnState{Phase: ConnConnected, FetchedAt: time.Now()},
		ch:    make(chan ConnStateMsg, 8),
		retry: make(chan struct{}, 1),
	}
}

// reset restores health to a fresh Connected state without replacing ch/retry
// — used by Cluster.SwitchContext, which rebuilds the clientset/factory in
// place but must keep the same *Cluster identity: a caller already ranging
// over ConnEvents() (app.RunWithConfig's forwardEvents) holds a reference to
// this channel from program start, and swapping in a brand-new health (and
// therefore a brand-new channel) would silently orphan that reader for the
// rest of the process's life.
func (h *health) reset() {
	h.mu.Lock()
	h.state = ConnState{Phase: ConnConnected, FetchedAt: time.Now()}
	h.mu.Unlock()
}

func (h *health) get() ConnState {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.state
}

func (h *health) set(s ConnState) {
	h.mu.Lock()
	h.state = s
	h.mu.Unlock()
	select {
	case h.ch <- ConnStateMsg(s):
	default:
		// Consumer is behind: drop the oldest queued state, the new one
		// supersedes it.
		select {
		case <-h.ch:
		default:
		}
		select {
		case h.ch <- ConnStateMsg(s):
		default:
		}
	}
}

// onWatchError is the informer WatchErrorHandler: the first dropped
// connection flips to Reconnecting with the verbatim error and schedules a
// backoff retry. Once Reconnecting, the ping loop owns further attempts/
// backoff so repeated watch errors during the same outage don't reset it.
func (h *health) onWatchError(err error) {
	prev := h.get()
	if prev.Phase == ConnReconnecting {
		return
	}
	attempt := prev.Attempt + 1
	h.set(ConnState{
		Phase:       ConnReconnecting,
		Err:         err.Error(),
		Attempt:     attempt,
		NextRetryAt: time.Now().Add(backoffDelay(attempt)),
		FetchedAt:   prev.FetchedAt,
	})
}

// retryNow requests an immediate probe, bypassing the backoff wait.
func (h *health) retryNow() {
	select {
	case h.retry <- struct{}{}:
	default:
	}
}

// ConnEvents streams connection-state changes for app.Run to fan into the
// program alongside Events().
func (c *Cluster) ConnEvents() <-chan ConnStateMsg { return c.health.ch }

// ConnState returns the last known connection state.
func (c *Cluster) ConnState() ConnState { return c.health.get() }

// RetryNow requests an immediate reconnect probe (the 4a "r" key).
func (c *Cluster) RetryNow() { c.health.retryNow() }

// startHealthLoop pings /livez every pingInterval (and immediately on
// RetryNow) to measure latency and detect recovery. It runs until stopCh
// closes.
func (c *Cluster) startHealthLoop(stopCh <-chan struct{}) {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stopCh:
			return
		case <-ticker.C:
			c.ping()
		case <-c.health.retry:
			c.ping()
		}
	}
}

func (c *Cluster) ping() {
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), pingTimeout)
	defer cancel()
	err := c.clientset.Discovery().RESTClient().Get().AbsPath("/livez").Do(ctx).Error()
	latency := time.Since(start)
	prev := c.health.get()

	if err != nil {
		attempt := prev.Attempt
		if prev.Phase != ConnReconnecting {
			attempt = 0
		}
		attempt++
		c.health.set(ConnState{
			Phase:       ConnReconnecting,
			Latency:     latency,
			Err:         err.Error(),
			Attempt:     attempt,
			NextRetryAt: time.Now().Add(backoffDelay(attempt)),
			FetchedAt:   prev.FetchedAt,
		})
		return
	}
	c.health.set(ConnState{Phase: ConnConnected, Latency: latency, FetchedAt: time.Now()})
}

// setWatchErrorHandlers wires health.onWatchError into every informer in
// handlers. Must be called before the factory starts (SetWatchErrorHandler
// returns an error once an informer is running).
func (c *Cluster) setWatchErrorHandlers(handlers map[ResourceKind]cache.SharedIndexInformer) {
	for _, informer := range handlers {
		//nolint:errcheck // best-effort: a failed registration just means no health signal from this informer
		_ = informer.SetWatchErrorHandler(func(_ *cache.Reflector, err error) {
			c.health.onWatchError(err)
		})
	}
}
