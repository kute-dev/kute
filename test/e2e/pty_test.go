//go:build e2e && e2e_pty && !windows

package e2e

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
	"github.com/creack/pty"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestPTYExecSubprocessHandoff runs the shipping binary under a kernel PTY.
// The ordinary e2e harness intentionally cannot cover this path: its input
// and output are pipes, so Bubble Tea has no terminal mode to release to
// kubectl and recapture afterward.
func TestPTYExecSubprocessHandoff(t *testing.T) {
	RequireCluster(t)
	proxy := NewAPIProxy(t, KubeconfigPath())
	pod := runningAPIPod(t)
	binary := buildKuteBinary(t)
	a := launchPTY(t, binary, proxy.KubeconfigPath())

	a.WaitFor("api-", Connect)
	a.Write("/")
	filterStart := a.Checkpoint()
	a.Write(pod)
	// The real renderer emits a cursor-relative diff after every rune, so the
	// query itself is not guaranteed to be contiguous in the byte stream.
	// The filtered row count is the stable rendered consequence we need
	// before the first enter commits typing mode and the second opens the sole
	// match.
	a.WaitForAfter(filterStart, "1 of 1", Settle)
	commitStart := a.Checkpoint()
	a.Write("\r")
	// The Pod action keybar replaces filter typing's "type to narrow" once
	// the first enter commits the filter. Fence on that redraw before the
	// second enter opens the selected row.
	a.WaitForAfter(commitStart, "logs", Settle)
	detailStart := a.Checkpoint()
	a.Write("\r")
	a.WaitForAfter(detailStart, "CONTAINERS", Settle)

	// Clean shell exit: print a value that does not occur in the command text
	// (so remote tty echo cannot satisfy the assertion), then require kute to
	// redraw the pod detail and accept another navigation key.
	clean := enterPTYShell(t, a, proxy, pod)
	markerStart := a.Checkpoint()
	a.Write("printf 'KUTE-PTY-CLEAN-%s\\n' \"$((6*7))\"\n")
	a.WaitForAfter(markerStart, "KUTE-PTY-CLEAN-42", Settle)
	redrawStart := a.Checkpoint()
	a.Write("exit\n")
	proxy.WaitForCompletion(clean.ID, Settle)
	a.WaitForAfter(redrawStart, "CONTAINERS", Settle)

	// A non-zero remote shell exit is reported in the picker, but does not
	// strand the renderer or make the picker unusable.
	nonzero := enterPTYShell(t, a, proxy, pod)
	feedbackStart := a.Checkpoint()
	a.Write("exit 23\n")
	proxy.WaitForCompletion(nonzero.ID, Settle)
	a.WaitForAfter(feedbackStart, "exec exited: exit status 23", Settle)
	backStart := a.Checkpoint()
	a.Write("\x1b")
	time.Sleep(escSettle)
	a.WaitForAfter(backStart, "CONTAINERS", Settle)

	// Queue the application quit as the final exec stream is closing, before
	// tea.ExecProcess has redrawn. Repeating ctrl+q over this short handoff
	// window avoids racing kubectl's stdin copier: the first byte is sent
	// while the subprocess still owns the PTY, and the first byte Bubble Tea
	// sees after recapture quits the app.
	active := enterPTYShell(t, a, proxy, pod)
	a.Write("exit\n")
	a.WriteBestEffort("\x11") // queued while the exec request is still active
	proxy.WaitForCompletion(active.ID, Settle)
	quitDeadline := time.Now().Add(10 * time.Second)
	for !a.Exited() && time.Now().Before(quitDeadline) {
		a.WriteBestEffort("\x11") // ctrl+q
		time.Sleep(25 * time.Millisecond)
	}
	a.Wait(Settle)

	got, err := term.GetState(a.master.Fd())
	if err != nil {
		t.Fatalf("reading PTY terminal state after kute exit: %v", err)
	}
	if !reflect.DeepEqual(got, a.initialState) {
		t.Fatalf("kute left the PTY in a modified terminal mode after subprocess handoff")
	}
}

func enterPTYShell(t *testing.T, a *ptyApp, proxy *APIProxy, pod string) RequestRecord {
	t.Helper()
	pickerStart := a.Checkpoint()
	a.Write("x")
	a.WaitForAfter(pickerStart, "exec › "+pod, Settle)
	fence := proxy.Fence()
	shellStart := a.Checkpoint()
	a.Write("\r")
	rec := proxy.WaitForRequest(fence, RequestMatcher{
		Path: "/api/v1/namespaces/" + Namespace + "/pods/" + pod + "/exec",
	}, Settle)
	if rec.Completed {
		t.Fatalf("exec stream completed before the shell was usable: %+v", rec)
	}
	a.WaitForAfter(shellStart, "/ #", Settle)
	return rec
}

func runningAPIPod(t *testing.T) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	pods, err := e2eClientset(t).CoreV1().Pods(Namespace).List(ctx, metav1.ListOptions{LabelSelector: "app=api"})
	if err != nil {
		t.Fatalf("listing api pods: %v", err)
	}
	var names []string
	for _, pod := range pods.Items {
		if pod.Status.Phase == "Running" {
			names = append(names, pod.Name)
		}
	}
	if len(names) == 0 {
		t.Fatal("no running api pod for PTY exec")
	}
	sort.Strings(names)
	return names[0]
}

func buildKuteBinary(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kute")
	cmd := exec.CommandContext(t.Context(), "go", "build", "-o", path, "./cmd/kute")
	cmd.Dir = repoRoot()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building kute for PTY test: %v\n%s", err, out)
	}
	return path
}

type ptyApp struct {
	t            *testing.T
	master       *os.File
	cmd          *exec.Cmd
	initialState *term.State
	done         chan struct{}

	mu      sync.Mutex
	output  []byte
	waitErr error
}

func launchPTY(t *testing.T, binary, kubeconfig string) *ptyApp {
	t.Helper()
	home := t.TempDir()
	cmd := exec.Command(binary,
		"--kubeconfig", kubeconfig,
		"--namespace", Namespace,
		"--theme", "dark",
		"--no-update-check",
	)
	cmd.Env = replacedEnv(os.Environ(), map[string]string{
		"HOME":            home,
		"XDG_STATE_HOME":  filepath.Join(home, ".local", "state"),
		"XDG_CONFIG_HOME": filepath.Join(home, ".config"),
		"XDG_CACHE_HOME":  filepath.Join(home, ".cache"),
		"KUBECONFIG":      kubeconfig,
		"TERM":            "xterm-256color",
		"COLORTERM":       "truecolor",
	})

	master, tty, err := pty.Open()
	if err != nil {
		t.Fatalf("opening PTY: %v", err)
	}
	initial, err := term.GetState(tty.Fd())
	if err != nil {
		_ = master.Close()
		_ = tty.Close()
		t.Fatalf("reading initial PTY state: %v", err)
	}
	if err := pty.Setsize(master, &pty.Winsize{Cols: DefaultWidth, Rows: DefaultHeight}); err != nil {
		_ = master.Close()
		_ = tty.Close()
		t.Fatalf("sizing PTY: %v", err)
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := cmd.Start(); err != nil {
		_ = master.Close()
		_ = tty.Close()
		t.Fatalf("starting kute under PTY: %v", err)
	}
	_ = tty.Close()

	a := &ptyApp{t: t, master: master, cmd: cmd, initialState: initial, done: make(chan struct{})}
	go a.read()
	go func() {
		err := cmd.Wait()
		a.mu.Lock()
		a.waitErr = err
		a.mu.Unlock()
		close(a.done)
	}()
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("PTY output:\n%s", a.OutputAfter(0))
		}
		if !a.Exited() {
			_ = a.cmd.Process.Kill()
			<-a.done
		}
		_ = a.master.Close()
	})
	return a
}

func (a *ptyApp) read() {
	buf := make([]byte, 4096)
	for {
		n, err := a.master.Read(buf)
		if n > 0 {
			a.mu.Lock()
			a.output = append(a.output, buf[:n]...)
			a.mu.Unlock()
		}
		if err != nil {
			// Linux PTY masters report EIO when the last slave closes; other
			// Unix implementations report ordinary EOF.
			if !errors.Is(err, io.EOF) && !strings.Contains(err.Error(), "input/output error") {
				a.mu.Lock()
				a.output = append(a.output, []byte("\nPTY read error: "+err.Error())...)
				a.mu.Unlock()
			}
			return
		}
	}
}

func (a *ptyApp) Checkpoint() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.output)
}

func (a *ptyApp) OutputAfter(from int) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if from < 0 || from > len(a.output) {
		from = 0
	}
	return ansi.Strip(string(a.output[from:]))
}

func (a *ptyApp) WaitFor(substr string, timeout time.Duration) {
	a.t.Helper()
	a.WaitForAfter(0, substr, timeout)
}

func (a *ptyApp) WaitForAfter(from int, substr string, timeout time.Duration) {
	a.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		out := a.OutputAfter(from)
		if strings.Contains(out, substr) {
			return
		}
		if a.Exited() {
			a.t.Fatalf("kute exited before PTY output contained %q; output:\n%s", substr, out)
		}
		if time.Now().After(deadline) {
			a.t.Fatalf("PTY output never contained %q within %s; output:\n%s", substr, timeout, out)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func (a *ptyApp) Write(s string) {
	a.t.Helper()
	if _, err := a.master.Write([]byte(s)); err != nil {
		a.t.Fatalf("writing %q to PTY: %v", s, err)
	}
}

func (a *ptyApp) WriteBestEffort(s string) { _, _ = a.master.Write([]byte(s)) }

func (a *ptyApp) Exited() bool {
	select {
	case <-a.done:
		return true
	default:
		return false
	}
}

func (a *ptyApp) Wait(timeout time.Duration) {
	a.t.Helper()
	select {
	case <-a.done:
		a.mu.Lock()
		err := a.waitErr
		a.mu.Unlock()
		if err != nil {
			a.t.Fatalf("kute exited with an error: %v\n%s", err, a.OutputAfter(0))
		}
	case <-time.After(timeout):
		a.t.Fatalf("kute did not exit within %s\n%s", timeout, a.OutputAfter(0))
	}
}

func replacedEnv(base []string, replacements map[string]string) []string {
	out := make([]string, 0, len(base)+len(replacements))
	for _, item := range base {
		key, _, _ := strings.Cut(item, "=")
		if _, replaced := replacements[key]; !replaced {
			out = append(out, item)
		}
	}
	for key, value := range replacements {
		out = append(out, fmt.Sprintf("%s=%s", key, value))
	}
	return out
}
