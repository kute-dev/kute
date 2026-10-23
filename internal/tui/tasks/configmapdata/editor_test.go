package configmapdata

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kute-dev/kute/internal/tui"
)

func TestEditorCommandFollowsKubectlOrder(t *testing.T) {
	env := map[string]string{}
	get := func(k string) string { return env[k] }
	if got := editorCommand(get); strings.Join(got, " ") != "vi" {
		t.Fatalf("no env: %q, want vi", got)
	}
	env["EDITOR"] = "nano"
	env["VISUAL"] = "nvim"
	if got := editorCommand(get); got[0] != "nvim" {
		t.Fatalf("VISUAL must beat EDITOR, got %q", got)
	}
	env["KUBE_EDITOR"] = "code --wait"
	if got := editorCommand(get); strings.Join(got, " ") != "code --wait" {
		t.Fatalf("KUBE_EDITOR must win and keep its args, got %q", got)
	}
}

func TestEditorResultDropsTheSavedFinalNewline(t *testing.T) {
	for _, tc := range []struct{ initial, edited, want string }{
		{"a=1", "a=2\n", "a=2"},
		{"a=1\n", "a=2\n", "a=2\n"},
		{"a=1", "a=2", "a=2"},
	} {
		if got := editorResult(tc.initial, tc.edited); got != tc.want {
			t.Errorf("editorResult(%q, %q) = %q, want %q", tc.initial, tc.edited, got, tc.want)
		}
	}
}

func TestEditorFileExt(t *testing.T) {
	for key, want := range map[string]string{"nginx.conf": ".conf", "app.yaml": ".yaml", "plain": "", "x.b$d": ""} {
		if got := editorFileExt(key); got != want {
			t.Errorf("editorFileExt(%q) = %q, want %q", key, got, want)
		}
	}
}

// TestExternalEditRunsTheEditor drives Run with a fake editor that rewrites
// the file, the way vim would on :wq.
func TestExternalEditRunsTheEditor(t *testing.T) {
	script := filepath.Join(t.TempDir(), "fake-editor")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf 'line1\\nedited\\n' > \"$1\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBE_EDITOR", script)
	e := &externalEdit{key: "cfg.ini", initial: "line1\nline2"}
	if err := e.Run(); err != nil {
		t.Fatal(err)
	}
	if e.result != "line1\nedited" {
		t.Fatalf("result = %q", e.result)
	}
}

func TestApplyExternalEditLoadsOnlyIntoTheSameBuffer(t *testing.T) {
	m := &Model{multiline: newMultilineEditState("k", "old", tui.Dark())}
	m.applyExternalEdit(externalEditMsg{key: "other", value: "nope"})
	if m.multiline.value() != "old" {
		t.Fatal("an edit for another key loaded into this buffer")
	}
	m.applyExternalEdit(externalEditMsg{key: "k", value: "new\nlines"})
	if m.multiline.value() != "new\nlines" || m.notice != "" {
		t.Fatalf("value=%q notice=%q", m.multiline.value(), m.notice)
	}
	m.applyExternalEdit(externalEditMsg{key: "k", value: "a\tb"})
	if m.multiline.value() != "new\nlines" || m.notice == "" {
		t.Fatalf("a tab value must be refused with a notice: value=%q notice=%q", m.multiline.value(), m.notice)
	}
}
