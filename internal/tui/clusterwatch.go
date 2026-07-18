package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/kube"
)

// clusterEventMsg/clusterConnMsg wrap one channel read from a cluster whose
// lifetime started after the program did (a ReplaceRootMsg reconnect,
// mvp-plan.md Phase 4) — self-perpetuating tea.Cmd "drain" messages, the
// same shape as podlogs/stream.go's waitForStream and context.go's
// waitForProbe, so forwarding lives entirely inside the Bubble Tea Cmd/Msg
// model instead of needing a *tea.Program reference (which a Cmd built deep
// inside a task's Update, e.g. tasks/setup's retry, never has).
type clusterEventMsg struct {
	events <-chan kube.ResourceChangedMsg
	conn   <-chan kube.ConnStateMsg
	inner  kube.ResourceChangedMsg
}

type clusterConnMsg struct {
	events <-chan kube.ResourceChangedMsg
	conn   <-chan kube.ConnStateMsg
	inner  kube.ConnStateMsg
}

// WatchCluster starts forwarding events/conn into the program as
// clusterEventMsg/clusterConnMsg. Model.Update re-issues it after every
// message it produces, so one call (from ReplaceRootMsg's handling) keeps
// the cluster's channels draining for as long as the program runs.
func WatchCluster(events <-chan kube.ResourceChangedMsg, conn <-chan kube.ConnStateMsg) tea.Cmd {
	return func() tea.Msg {
		select {
		case ev, ok := <-events:
			if !ok {
				return nil
			}
			return clusterEventMsg{events: events, conn: conn, inner: ev}
		case cs, ok := <-conn:
			if !ok {
				return nil
			}
			return clusterConnMsg{events: events, conn: conn, inner: cs}
		}
	}
}
