package app

import (
	"context"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/kube"
)

type bridgeEventSource struct {
	events chan kube.ResourceChangedMsg
	conn   chan kube.ConnStateMsg
}

func (s bridgeEventSource) Events() <-chan kube.ResourceChangedMsg { return s.events }
func (s bridgeEventSource) ConnEvents() <-chan kube.ConnStateMsg   { return s.conn }

type bridgeReadyMsg struct{}

type bridgeCaptureModel struct {
	ready   chan struct{}
	updates chan kube.ResourceKind
	once    sync.Once
}

func (m *bridgeCaptureModel) Init() tea.Cmd { return nil }
func (m *bridgeCaptureModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case bridgeReadyMsg:
		m.once.Do(func() { close(m.ready) })
	case kube.ResourceChangedMsg:
		m.updates <- msg.Kind
	}
	return m, nil
}
func (*bridgeCaptureModel) View() tea.View { return tea.NewView("") }

func TestForwardEventsCoalescesStormByKindAndDeliversTheFinalTurn(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	model := &bridgeCaptureModel{ready: make(chan struct{}), updates: make(chan kube.ResourceKind, 8)}
	program := tea.NewProgram(model, tea.WithContext(ctx), tea.WithoutRenderer(), tea.WithInput(nil), tea.WithoutSignalHandler())
	done := make(chan error, 1)
	go func() {
		_, err := program.Run()
		done <- err
	}()
	program.Send(bridgeReadyMsg{})
	select {
	case <-model.ready:
	case <-time.After(time.Second):
		t.Fatal("Bubble Tea program did not start")
	}

	source := bridgeEventSource{events: make(chan kube.ResourceChangedMsg, 512), conn: make(chan kube.ConnStateMsg, 1)}
	go forwardEvents(ctx, source, program, nil)
	for range 200 {
		source.events <- kube.ResourceChangedMsg{Kind: kube.KindPod}
		source.events <- kube.ResourceChangedMsg{Kind: kube.KindEvent}
	}

	seen := map[kube.ResourceKind]int{}
	deadline := time.NewTimer(5 * resourceEventDebounce)
	defer deadline.Stop()
	for len(seen) < 2 {
		select {
		case kind := <-model.updates:
			seen[kind]++
		case <-deadline.C:
			t.Fatalf("coalesced kinds never arrived: %v", seen)
		}
	}
	if seen[kube.KindPod] != 1 || seen[kube.KindEvent] != 1 {
		t.Fatalf("storm delivered %+v, want one turn per kind", seen)
	}
	select {
	case kind := <-model.updates:
		t.Fatalf("storm accumulated another %s turn after settling", kind)
	case <-time.After(2 * resourceEventDebounce):
	}

	// A later change still gets its own turn; coalescing is not a latch.
	source.events <- kube.ResourceChangedMsg{Kind: kube.KindPod}
	select {
	case kind := <-model.updates:
		if kind != kube.KindPod {
			t.Fatalf("later turn kind = %s", kind)
		}
	case <-time.After(5 * resourceEventDebounce):
		t.Fatal("later Pod change was lost")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Bubble Tea program did not stop")
	}
}
