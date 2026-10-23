package configmapdata

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// externalEdit runs the user's own editor over one buffer value through
// tea.Exec. The temp file is created, read back and removed inside Run, so
// Update does no file I/O — it only builds this value.
type externalEdit struct {
	key     string
	initial string
	result  string

	stdin          io.Reader
	stdout, stderr io.Writer
}

// externalEditMsg is the editor's outcome for the buffer opened on key.
type externalEditMsg struct {
	key   string
	value string
	err   error
}

func (e *externalEdit) SetStdin(r io.Reader)  { e.stdin = r }
func (e *externalEdit) SetStdout(w io.Writer) { e.stdout = w }
func (e *externalEdit) SetStderr(w io.Writer) { e.stderr = w }

func (e *externalEdit) Run() error {
	f, err := os.CreateTemp("", "kute-configmap-*"+editorFileExt(e.key))
	if err != nil {
		return err
	}
	path := f.Name()
	defer func() { _ = os.Remove(path) }()
	if _, err := f.WriteString(e.initial); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	argv := editorCommand(os.Getenv)
	cmd := exec.Command(argv[0], append(argv[1:], path)...) //nolint:gosec // the user's own configured editor
	cmd.Stdin, cmd.Stdout, cmd.Stderr = e.stdin, e.stdout, e.stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	e.result = editorResult(e.initial, string(raw))
	return nil
}

// externalEditCmd hands value to the editor and reports back as an
// externalEditMsg.
func externalEditCmd(key, value string) tea.Cmd {
	e := &externalEdit{key: key, initial: value}
	return tea.Exec(e, func(err error) tea.Msg {
		return externalEditMsg{key: key, value: e.result, err: err}
	})
}

// editorCommand is kubectl edit's own lookup order, so kute opens the
// editor `E` already does: KUBE_EDITOR, then VISUAL, then EDITOR, then vi.
func editorCommand(getenv func(string) string) []string {
	for _, name := range []string{"KUBE_EDITOR", "VISUAL", "EDITOR"} {
		if argv := strings.Fields(getenv(name)); len(argv) > 0 {
			return argv
		}
	}
	return []string{"vi"}
}

// editorResult drops the final newline an editor adds on save when the
// value didn't have one — vim's 'fixeol' would otherwise change every value
// it touches.
func editorResult(initial, edited string) string {
	if !strings.HasSuffix(initial, "\n") {
		edited = strings.TrimSuffix(strings.TrimSuffix(edited, "\n"), "\r")
	}
	return edited
}

// editorFileExt keeps a key's own extension ("nginx.conf" → ".conf") so the
// editor picks its syntax, limited to characters safe in a file name.
func editorFileExt(key string) string {
	ext := filepath.Ext(key)
	for _, r := range ext[min(1, len(ext)):] {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return ""
		}
	}
	return ext
}
