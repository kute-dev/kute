package tui_test

import (
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/kute-dev/kute/internal/tui"
)

// pasteRecordingTask stands in for the screen under an overlay: it records
// every paste it is handed, which is how the leak test below proves the shell
// kept one to itself.
type pasteRecordingTask struct {
	screenTask
	pastes []string
}

func (t *pasteRecordingTask) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if paste, ok := msg.(tea.PasteMsg); ok {
		t.pastes = append(t.pastes, paste.Content)
	}
	return t, nil
}

// TestPasteFillsPaletteQueryAndRefilters pins that a paste reaches the open
// palette's query box and re-ranks the list — a pasted query has to behave
// exactly like a typed one, not just appear in the box.
func TestPasteFillsPaletteQueryAndRefilters(t *testing.T) {
	t.Parallel()
	lister := gotoFakeLister{}
	model := tui.NewWithSession(&screenTask{name: "browse"}, gotoTestSession(lister))
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	updated, _ = updated.(tui.Model).Update(tea.KeyPressMsg{Text: "g"})

	updated, _ = updated.(tui.Model).Update(tea.PasteMsg{Content: "deploy"})
	view := ansi.Strip(updated.(tui.Model).View().Content)

	if !strings.Contains(view, "deploy") {
		t.Fatalf("pasted query missing from the palette:\n%s", view)
	}
	// The ranked list must have narrowed to the pasted query, which is the
	// half a bare insert would miss.
	if strings.Contains(view, "Namespaces") {
		t.Fatalf("palette did not re-filter after the paste:\n%s", view)
	}
}

func TestPaletteSelectionIsReplacedByPaste(t *testing.T) {
	t.Parallel()
	model := tui.NewWithSession(&screenTask{name: "browse"}, gotoTestSession(gotoFakeLister{}))
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	updated, _ = updated.(tui.Model).Update(tea.KeyPressMsg{Text: "g"})
	for _, r := range "deployment" {
		updated, _ = updated.(tui.Model).Update(tea.KeyPressMsg{Text: string(r)})
	}
	for range 4 {
		updated, _ = updated.(tui.Model).Update(tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModShift})
	}
	updated, _ = updated.(tui.Model).Update(tea.PasteMsg{Content: "s"})

	view := ansi.Strip(updated.(tui.Model).View().Content)
	if !strings.Contains(view, "› deploys") {
		t.Fatalf("paste did not replace the palette selection:\n%s", view)
	}
}

// TestPasteWithPaletteOpenDoesNotReachTask is the negative that matters: the
// root shell forwards anything it doesn't claim to the active task, so a
// paste aimed at the palette would otherwise land in the filter box of the
// screen rendered underneath it.
func TestPasteWithPaletteOpenDoesNotReachTask(t *testing.T) {
	t.Parallel()
	task := &pasteRecordingTask{screenTask: screenTask{name: "browse"}}
	model := tui.NewWithSession(task, gotoTestSession(gotoFakeLister{}))
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	updated, _ = updated.(tui.Model).Update(tea.KeyPressMsg{Text: "g"})

	updated.(tui.Model).Update(tea.PasteMsg{Content: "deploy"})
	if len(task.pastes) != 0 {
		t.Fatalf("paste leaked to the task under the palette: %q", task.pastes)
	}

	// With no overlay open the same paste belongs to the task.
	closed, _ := updated.(tui.Model).Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	closed.(tui.Model).Update(tea.PasteMsg{Content: "api"})
	if len(task.pastes) != 1 || task.pastes[0] != "api" {
		t.Fatalf("task pastes = %q, want [api] once the palette is closed", task.pastes)
	}
}

// TestPasteChordAsksForTheClipboard pins ctrl+v with the palette open: the
// shell answers with the clipboard-read cmd rather than letting the chord
// fall through to the palette's own key handling (where bubbles' textinput
// would swallow it and reply with a message nothing can route).
func TestPasteChordAsksForTheClipboard(t *testing.T) {
	t.Parallel()
	model := tui.NewWithSession(&screenTask{name: "browse"}, gotoTestSession(gotoFakeLister{}))
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	updated, _ = updated.(tui.Model).Update(tea.KeyPressMsg{Text: "g"})

	_, cmd := updated.(tui.Model).Update(tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'v'})
	if cmd == nil {
		t.Fatal("ctrl+v with the palette open returned no cmd, want the clipboard read")
	}
	// Asserted by type name because the alternative is invisible otherwise:
	// letting the chord fall through to the palette's textinput also returns
	// a non-nil cmd, but one whose reply is bubbles' unexported pasteMsg,
	// which no Update in this app can route. Either of our own answers is
	// fine — the OS-clipboard read or its OSC52 fallback request.
	switch typ := reflect.TypeOf(cmd()).String(); typ {
	case "tea.PasteMsg", "tea.readClipboardMsg":
	default:
		t.Fatalf("ctrl+v answered with %s, want tui.PasteCmd's own message", typ)
	}
	// The chord must not have been typed into the query box as a "v".
	view := ansi.Strip(updated.(tui.Model).View().Content)
	if strings.Contains(view, "› v") {
		t.Fatalf("ctrl+v leaked into the query box:\n%s", view)
	}
}

// TestPasteWithHelpOpenIsSwallowed: the help overlay has no text entry, so a
// paste there must go nowhere rather than into the screen behind it.
func TestPasteWithHelpOpenIsSwallowed(t *testing.T) {
	t.Parallel()
	task := &pasteRecordingTask{screenTask: screenTask{name: "browse"}}
	model := tui.NewWithSession(task, gotoTestSession(gotoFakeLister{}))
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	updated, _ = updated.(tui.Model).Update(tea.KeyPressMsg{Text: "?"})
	if !updated.(tui.Model).HelpOpen() {
		t.Fatal("'?' did not open the help overlay")
	}

	updated.(tui.Model).Update(tea.PasteMsg{Content: "deploy"})
	if len(task.pastes) != 0 {
		t.Fatalf("paste leaked to the task under the help overlay: %q", task.pastes)
	}
}
