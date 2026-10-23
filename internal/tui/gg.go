package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/tui/components/palette"
)

// gg is vim's go-to-top, reconciled with g opening the goto palette.
//
// The first g opens the palette as it always has. A second g counts as gg
// only when all three hold: it lands within ggArmWindow of the first, the
// query is still empty, and nothing else follows within ggCommitDelay. Only
// then does the palette close and the active task receive "home" — which
// every screen's motion switch already reads as top (verbs.TopBottom).
//
// Anything else is ordinary typing. A held g is typed into the query the
// moment another key arrives, so "g gitlab" typed in one burst searches for
// gitlab, and a slow "g … g" just searches for g. The arm window is a clock
// read in Update (ggNow, swappable in tests) so g's own command stays the
// palette's count fetch alone; the commit delay is a timer message.
const (
	ggArmWindow   = 400 * time.Millisecond
	ggCommitDelay = 300 * time.Millisecond
)

// ggNow is the arm window's clock.
var ggNow = time.Now

type ggPhase int

const (
	ggIdle ggPhase = iota
	// ggArmed: the palette was just opened by g; a second g may be gg.
	ggArmed
	// ggPending: the second g arrived and is held, not yet typed.
	ggPending
)

// ggTimeoutMsg ends phase if seq is still current.
type ggTimeoutMsg struct {
	seq   int
	phase ggPhase
}

func (m *Model) ggEnter(phase ggPhase, after time.Duration) tea.Cmd {
	m.gg = phase
	m.ggSeq++
	seq := m.ggSeq
	return tea.Tick(after, func(time.Time) tea.Msg { return ggTimeoutMsg{seq: seq, phase: phase} })
}

// ggArm is called when g (not ':') opens the goto palette.
func (m *Model) ggArm() {
	m.gg = ggArmed
	m.ggSeq++
	m.ggArmedAt = ggNow()
}

// ggResolve runs before the palette handles msg. It reports consumed when
// msg was the held second g; otherwise it returns the command from typing
// any held g into the query, for the caller to batch with its own handling
// of msg.
func (m *Model) ggResolve(msg tea.KeyPressMsg) (consumed bool, cmd tea.Cmd) {
	switch m.gg {
	case ggArmed:
		m.gg = ggIdle
		if msg.String() == "g" && m.palette.Scope == palette.ScopeGoto && m.palette.Query() == "" &&
			ggNow().Sub(m.ggArmedAt) <= ggArmWindow {
			return true, m.ggEnter(ggPending, ggCommitDelay)
		}
	case ggPending:
		return false, m.ggRelease()
	}
	return false, nil
}

// ggRelease types a held g into the query, as if gg had never been a
// candidate.
func (m *Model) ggRelease() tea.Cmd {
	if m.gg != ggPending {
		return nil
	}
	m.gg = ggIdle
	if m.palette == nil {
		return nil
	}
	return m.typeKey(tea.KeyPressMsg{Code: 'g', Text: "g"})
}

// handleGGTimeout ends an arm window, or commits a held g as gg.
func (m *Model) handleGGTimeout(msg ggTimeoutMsg) tea.Cmd {
	if msg.seq != m.ggSeq || msg.phase != m.gg {
		return nil
	}
	m.gg = ggIdle
	if msg.phase != ggPending || m.palette == nil || m.palette.Scope != palette.ScopeGoto || m.palette.Query() != "" {
		return nil
	}
	m.palette = nil
	m.stopContextProbe()
	m.mode = ModeBrowse
	return m.forward(tea.KeyPressMsg{Code: tea.KeyHome})
}
