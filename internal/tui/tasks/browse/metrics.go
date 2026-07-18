package browse

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
)

// loadMetrics fetches one round of pod usage for the current namespace,
// tagged with epoch so a reply arriving after a kind/namespace switch (or a
// second poll already in flight) is recognized as stale and dropped.
func (m Model) loadMetrics(epoch int) tea.Cmd {
	metrics := m.metrics
	namespace := m.countNamespace()
	timeout := m.timeout
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		result, err := metrics.PodMetricsByNamespace(ctx, namespace)
		return podMetricsLoadedMsg{epoch: epoch, namespace: namespace, metrics: result, err: err}
	}
}

// scheduleMetricsTick arranges the next poll pollInterval from now, per the
// header's "sync 2s" chip.
func (m Model) scheduleMetricsTick(epoch int) tea.Cmd {
	return tea.Tick(pollInterval, func(time.Time) tea.Msg {
		return metricsTickMsg{epoch: epoch}
	})
}

// scheduleReload debounces a burst of ResourceChangedMsg into one reload
// reloadDebounce after the most recent event, per Phase 1's "watch events
// arrive in bursts" note.
func (m Model) scheduleReload(epoch int) tea.Cmd {
	return tea.Tick(reloadDebounce, func(time.Time) tea.Msg {
		return reloadDueMsg{epoch: epoch}
	})
}
