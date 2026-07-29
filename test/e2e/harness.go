//go:build e2e

// Package e2e runs the real kute program against a real Kubernetes cluster.
//
// Everything else in the repo stops at a fake: internal/kube's tests drive
// client-go's fake clientset, every task package drives a hand-written
// fakeLister. Nothing exercises the whole join — kubeconfig resolution → REST
// config → shared informer factory → lazy ensureKind → resources.List →
// rendered frame — which is what this package is for.
//
// Provision the cluster first:
//
//	scripts/e2e-cluster.sh up
//	go test -tags e2e -count=1 -timeout 15m ./test/e2e/...
//
// Two rules keep the suite from going flaky, and both are load-bearing:
//
//   - No golden files. A real cluster produces nondeterministic AGE values,
//     pod-name suffixes, IPs and node assignment, so E2E asserts substrings
//     and structural predicates. Goldens stay the unit-level tool.
//   - No one-shot assertions. Informer caches fill asynchronously, so every
//     check goes through WaitFor, which polls the latest frame against a
//     deadline. A bare strings.Contains on the first frame is a race by
//     construction.
//
// No test here may call t.Parallel: Launch isolates HOME and the XDG
// directories with t.Setenv, which is process-global.
package e2e

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/kute-dev/kute/internal/app"
	"github.com/kute-dev/kute/internal/testutil/goldentest"
)

const (
	// Namespace is where every fixture in fixtures/ lives.
	Namespace = "kute-e2e"

	// DefaultWidth/DefaultHeight match browse's own 120×36 golden fixtures,
	// so a frame captured here is laid out the way the unit-level goldens
	// pin it.
	DefaultWidth  = 120
	DefaultHeight = 36

	// TypeToConfirm is the phrase only components.TypeNameModal renders —
	// the one reliable way to tell an escalated PROD confirm from the inline
	// y/N card, in either direction.
	TypeToConfirm = "to confirm"

	// Connect is the budget for reaching a first useful frame: three eager
	// informers have to sync against a cold apiserver.
	Connect = 90 * time.Second
	// Settle is the budget for anything after connect — a lazily-started
	// informer filling, a screen being pushed, a mutation's result landing.
	Settle = 45 * time.Second

	// fenceSeq is a focus-in report. The program parses it into a
	// tea.FocusMsg, which kute handles nowhere — so it drives one turn of the
	// event loop, and therefore one frame capture, while changing nothing.
	// That is the whole trick behind sync: see App.sync.
	//
	// Deliberately focus-*in* rather than focus-out: bubbles' cursor blurs on
	// tea.BlurMsg, so a focus-out fence would quietly un-focus the cursor of
	// whatever text input is open.
	fenceSeq = "\x1b[I"

	// escSettle is how long a lone ESC gets to be read as a key before the
	// next write lands. Without it a fence written immediately after ESC
	// arrives in the same read as "\x1b\x1b[I", which the input decoder is
	// entitled to read as one alt-modified sequence rather than two events.
	escSettle = 25 * time.Millisecond
)

// KubeconfigPath returns the admin kubeconfig scripts/e2e-cluster.sh writes.
func KubeconfigPath() string {
	if p := os.Getenv("KUTE_E2E_KUBECONFIG"); p != "" {
		return p
	}
	return filepath.Join(repoRoot(), ".kube", "e2e.config")
}

// RestrictedKubeconfigPath returns the token kubeconfig for the
// kute-restricted ServiceAccount — pods list/get in kute-e2e and nothing
// else, anywhere.
func RestrictedKubeconfigPath() string {
	if p := os.Getenv("KUTE_E2E_RESTRICTED_KUBECONFIG"); p != "" {
		return p
	}
	return filepath.Join(repoRoot(), ".kube", "e2e-restricted.config")
}

// ContextName returns the kubeconfig context the e2e cluster is reached
// through, read out of the kubeconfig rather than hard-coded, so it tracks
// K8S_VERSION instead of pinning the suite to one cluster name.
func ContextName(t *testing.T) string {
	t.Helper()
	cfg, err := clientcmd.LoadFromFile(KubeconfigPath())
	if err != nil {
		t.Fatalf("reading %s: %v", KubeconfigPath(), err)
	}
	if cfg.CurrentContext == "" {
		t.Fatalf("%s has no current-context", KubeconfigPath())
	}
	return cfg.CurrentContext
}

// PartialKubeconfigPath returns the token kubeconfig for the kute-partial
// ServiceAccount — cluster-wide read on exactly the kinds kute needs to
// connect (plus ConfigMaps and Events), and nothing on Secrets, Deployments
// or Ingresses. The identity the per-kind 403 paths are tested through.
func PartialKubeconfigPath() string {
	if p := os.Getenv("KUTE_E2E_PARTIAL_KUBECONFIG"); p != "" {
		return p
	}
	return filepath.Join(repoRoot(), ".kube", "e2e-partial.config")
}

func repoRoot() string {
	// This file is <root>/test/e2e/harness.go and tests run in their own
	// package directory, so the root is two levels up from the working
	// directory.
	wd, err := os.Getwd()
	if err != nil {
		return ".."
	}
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}

// RequireCluster skips the test unless the e2e kubeconfig exists, naming the
// script that makes it. Every E2E test starts here rather than failing with a
// connection error nobody can act on.
func RequireCluster(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(KubeconfigPath()); err != nil {
		t.Skipf("no e2e cluster: %v\nrun: scripts/e2e-cluster.sh up", err)
	}
}

// Option customises a Launch.
type Option func(*options)

type options struct {
	kubeconfig   string
	kubeContext  string
	namespace    string
	width        int
	height       int
	prodContexts []string
}

// WithKubeconfig launches against a kubeconfig other than the admin one —
// RestrictedKubeconfigPath(), for the RBAC suite.
func WithKubeconfig(path string) Option {
	return func(o *options) { o.kubeconfig = path }
}

// WithNamespace pins the namespace the app starts in.
func WithNamespace(ns string) Option {
	return func(o *options) { o.namespace = ns }
}

// WithContext pins the kubeconfig context to launch against.
func WithContext(name string) Option {
	return func(o *options) { o.kubeContext = name }
}

// WithSize launches at a terminal size other than 120×36.
func WithSize(width, height int) Option {
	return func(o *options) { o.width, o.height = width, height }
}

// WithProdContexts writes prodContexts into the isolated config file before
// the app reads it — the only source of PROD status, and so the only way to
// reach the type-the-name confirm modal.
func WithProdContexts(contexts ...string) Option {
	return func(o *options) { o.prodContexts = contexts }
}

// App is a running kute, driven by key bytes and observed through captured
// frames.
type App struct {
	t      *testing.T
	in     *io.PipeWriter
	cancel context.CancelFunc
	done   chan error

	mu     sync.Mutex
	frame  string
	seq    uint64
	fences uint64

	quitOnce sync.Once
}

// Launch starts the real program — app.RunE2E, which is RunWithConfig's own
// body, goroutines and all — against the e2e cluster, and returns once it has
// rendered its first frame.
//
// Frames are captured through tea.WithFilter rather than by parsing the
// program's output: bubbletea's renderer emits cursor-relative diffs, so the
// output stream is a sequence of partial updates that no amount of ANSI
// stripping turns back into a screen. The filter runs inside the event loop,
// before each Update, and kute's View is pure by invariant — so calling it
// there is safe and yields the same full frame the golden tests assert on.
func Launch(t *testing.T, opts ...Option) *App {
	t.Helper()
	RequireCluster(t)

	o := options{
		kubeconfig: KubeconfigPath(),
		namespace:  Namespace,
		width:      DefaultWidth,
		height:     DefaultHeight,
	}
	for _, opt := range opts {
		opt(&o)
	}

	isolateEnv(t, o)

	cfg := app.DefaultConfig()
	cfg.Kubeconfig = o.kubeconfig
	cfg.Namespace = o.namespace
	cfg.Context = o.kubeContext
	// Pin the theme rather than letting terminal-background detection run:
	// there is no terminal here, and a frame's content must not depend on
	// which way detection guessed.
	cfg.Theme = "dark"

	pr, pw := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())

	a := &App{t: t, in: pw, cancel: cancel, done: make(chan error, 1)}

	teaOpts := []tea.ProgramOption{
		tea.WithContext(ctx),
		tea.WithInput(pr),
		// The program still writes its diff stream somewhere; nothing reads
		// it, but discarding it keeps a test run's stdout clean.
		tea.WithOutput(io.Discard),
		tea.WithWindowSize(o.width, o.height),
		tea.WithColorProfile(colorprofile.TrueColor),
		tea.WithoutSignalHandler(),
		// Panics must surface as a failed test, not as bubbletea's own
		// recovered-and-reported error.
		tea.WithoutCatchPanics(),
		tea.WithFilter(a.capture),
	}

	go func() {
		defer func() {
			if r := recover(); r != nil {
				a.done <- fmt.Errorf("kute panicked: %v", r)
			}
		}()
		a.done <- app.RunE2E(cfg, teaOpts...)
	}()

	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("last frame:\n%s", a.Frame())
		}
		a.Quit()
	})

	a.waitForFirstFrame()
	return a
}

// capture is the tea.WithFilter hook: one frame per message, plus a separate
// count of the fence messages sync waits on.
//
// Counting fences specifically — rather than messages in general — is what
// makes sync exact. A Type("api") followed by one fence can arrive in a
// single read, and then four captures happen in a burst; a sync that merely
// waited for "some capture happened" would return after the first of them and
// read a frame two keystrokes stale.
func (a *App) capture(m tea.Model, msg tea.Msg) tea.Msg {
	content := goldentest.Plain(m.View().Content)
	_, isFence := msg.(tea.FocusMsg)
	a.mu.Lock()
	a.frame = content
	a.seq++
	if isFence {
		a.fences++
	}
	a.mu.Unlock()
	return msg
}

func (a *App) waitForFirstFrame() {
	a.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		a.mu.Lock()
		got := a.seq > 0
		a.mu.Unlock()
		if got {
			return
		}
		select {
		case err := <-a.done:
			a.done <- err
			a.t.Fatalf("kute exited before rendering anything: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			a.t.Fatal("kute rendered no frame within 30s")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// write feeds raw bytes to the program's input.
func (a *App) write(s string) {
	a.t.Helper()
	if _, err := a.in.Write([]byte(s)); err != nil {
		a.t.Fatalf("writing %q to kute's input: %v", s, err)
	}
}

// sync drives the event loop one inert turn and waits for the resulting frame
// capture, so that Frame afterwards reflects every message queued before it —
// including the key just pressed. Without it the last capture is always one
// message stale, because the filter sees a model *before* its Update.
func (a *App) sync() {
	a.t.Helper()
	a.mu.Lock()
	before := a.fences
	a.mu.Unlock()

	a.write(fenceSeq)

	deadline := time.Now().Add(5 * time.Second)
	for {
		a.mu.Lock()
		moved := a.fences > before
		a.mu.Unlock()
		if moved {
			return
		}
		select {
		case err := <-a.done:
			a.done <- err
			a.t.Fatalf("kute exited while waiting for a frame: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			a.t.Fatal("kute stopped processing messages (no frame within 5s of a focus event)")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Press sends one key. Names follow bubbletea's own vocabulary ("enter",
// "esc", "ctrl+d", "down", …); anything else is sent as literal runes.
func (a *App) Press(key string) {
	a.t.Helper()
	seq, isEsc := encodeKey(key)
	a.write(seq)
	if isEsc {
		// Let a lone ESC be decoded as a key before the fence lands.
		time.Sleep(escSettle)
	}
	a.sync()
}

// Type sends a string one rune at a time — what a user typing into the
// palette, a filter, or a type-the-name confirm actually produces.
func (a *App) Type(s string) {
	a.t.Helper()
	for _, r := range s {
		a.write(string(r))
	}
	a.sync()
}

// Enter, Esc and Down are the three keys used often enough to be worth a name.
func (a *App) Enter() { a.t.Helper(); a.Press("enter") }
func (a *App) Esc()   { a.t.Helper(); a.Press("esc") }
func (a *App) Down()  { a.t.Helper(); a.Press("down") }

// Frame returns the most recent captured frame, ANSI stripped.
func (a *App) Frame() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.frame
}

// WaitFor polls until substr appears in a frame, and fails the test with the
// last frame if it never does. This — not a bare Contains — is how every
// assertion in this package reads a screen: informer caches fill
// asynchronously, so the frame that first shows a fixture may be several
// event-loop turns after the key that asked for it.
func (a *App) WaitFor(substr string, timeout time.Duration) string {
	a.t.Helper()
	frame, ok := a.poll(func(f string) bool { return strings.Contains(f, substr) }, timeout)
	if !ok {
		a.t.Fatalf("never saw %q within %s; last frame:\n%s", substr, timeout, frame)
	}
	return frame
}

// WaitForAll waits for every substring to be present in one and the same
// frame — the way to assert on a screen that assembles in pieces without
// accepting a frame that only ever showed them separately.
func (a *App) WaitForAll(timeout time.Duration, substrs ...string) string {
	a.t.Helper()
	frame, ok := a.poll(func(f string) bool {
		for _, s := range substrs {
			if !strings.Contains(f, s) {
				return false
			}
		}
		return true
	}, timeout)
	if !ok {
		a.t.Fatalf("never saw all of %q within %s; last frame:\n%s", substrs, timeout, frame)
	}
	return frame
}

// WaitGone polls until substr is absent from a frame — the shape every
// "spinner must not hang" assertion takes.
func (a *App) WaitGone(substr string, timeout time.Duration) string {
	a.t.Helper()
	frame, ok := a.poll(func(f string) bool { return !strings.Contains(f, substr) }, timeout)
	if !ok {
		a.t.Fatalf("%q was still on screen after %s; last frame:\n%s", substr, timeout, frame)
	}
	return frame
}

// WaitLoaded waits until no loading state is on screen. Screens spell theirs
// differently — a pushed detail renders "⣽ Loading app-config...", a list
// renders "⣻ listing configmaps in kute-e2e…" — so this matches the word
// case-insensitively rather than making every caller remember which.
//
// It is also the anti-hang assertion: a screen whose loading state is gated
// on the wrong signal never clears it, and fails here.
func (a *App) WaitLoaded(timeout time.Duration) string {
	a.t.Helper()
	frame, ok := a.poll(func(f string) bool {
		return !strings.Contains(strings.ToLower(f), "loading")
	}, timeout)
	if !ok {
		a.t.Fatalf("still loading after %s; last frame:\n%s", timeout, frame)
	}
	return frame
}

// Never asserts substr does not appear at any point in the window — the only
// honest way to test a negative about an asynchronous screen. A one-shot
// check would pass simply by looking too early.
func (a *App) Never(substr string, window time.Duration) {
	a.t.Helper()
	frame, found := a.poll(func(f string) bool { return strings.Contains(f, substr) }, window)
	if found {
		a.t.Fatalf("%q appeared, and must not have; frame:\n%s", substr, frame)
	}
}

func (a *App) poll(pred func(string) bool, timeout time.Duration) (string, bool) {
	a.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		frame := a.Frame()
		if pred(frame) {
			return frame, true
		}
		if time.Now().After(deadline) {
			return frame, false
		}
		select {
		case err := <-a.done:
			a.done <- err
			a.t.Fatalf("kute exited mid-assertion: %v", err)
		default:
		}
		// Pump the event loop rather than merely sleeping: a screen that is
		// waiting on nothing produces no messages of its own, so without a
		// fence the captured frame would never catch up with the model.
		a.sync()
		time.Sleep(25 * time.Millisecond)
	}
}

// Quit stops the program and waits for it to exit, so informers and their
// goroutines are torn down before the next test launches its own. Safe to
// call more than once; t.Cleanup always calls it.
func (a *App) Quit() {
	a.quitOnce.Do(func() {
		a.cancel()
		_ = a.in.Close()
		select {
		case err := <-a.done:
			// A cancelled context is how this harness always stops the
			// program, so ErrProgramKilled is the success case.
			if err != nil && !errors.Is(err, tea.ErrProgramKilled) && !errors.Is(err, context.Canceled) {
				a.t.Errorf("kute exited with %v", err)
			}
		case <-time.After(30 * time.Second):
			a.t.Error("kute did not exit within 30s of its context being cancelled")
		}
	})
}

// encodeKey turns a key name into the bytes a terminal would send, and
// reports whether it was a bare ESC (which needs a gap after it).
func encodeKey(key string) (string, bool) {
	switch key {
	case "enter":
		return "\r", false
	case "esc", "escape":
		return "\x1b", true
	case "tab":
		return "\t", false
	case "space":
		return " ", false
	case "backspace":
		return "\x7f", false
	case "up":
		return "\x1b[A", false
	case "down":
		return "\x1b[B", false
	case "right":
		return "\x1b[C", false
	case "left":
		return "\x1b[D", false
	case "pgup":
		return "\x1b[5~", false
	case "pgdown":
		return "\x1b[6~", false
	case "home":
		return "\x1b[H", false
	case "end":
		return "\x1b[F", false
	}
	if name, ok := strings.CutPrefix(key, "ctrl+"); ok && len(name) == 1 {
		c := name[0]
		if c >= 'a' && c <= 'z' {
			return string([]byte{c - 'a' + 1}), false
		}
	}
	return key, false
}

// isolateEnv points every path kute persists to at the test's own temp
// directory: internal/state writes $XDG_STATE_HOME/kute/state.json,
// internal/config reads $HOME/.config/kute/config.yaml, and internal/helmrepo
// reads the XDG config/cache dirs. Without this an E2E run would read — and
// rewrite — the developer's own recents, per-context namespaces and prod
// markings.
func isolateEnv(t *testing.T, o options) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	// KUBECONFIG is set repo-wide by mise.toml and would otherwise be the
	// fallback if anything ignored cfg.Kubeconfig.
	t.Setenv("KUBECONFIG", o.kubeconfig)

	dir := filepath.Join(home, ".config", "kute")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating the isolated config dir: %v", err)
	}
	// 28a's release-feed check is off in every E2E run. It is the one thing
	// kute does that reaches outside the cluster, and leaving it on would put
	// an "update available" chip in the header and a what's-new hint in the
	// keybar — real frame content, dependent on the network and on whatever
	// the newest release happens to be that day.
	body := "update:\n  check: false\n"
	if len(o.prodContexts) > 0 {
		body += "prodContexts:\n"
		for _, c := range o.prodContexts {
			body += "  - " + c + "\n"
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0o644); err != nil {
		t.Fatalf("writing the isolated config file: %v", err)
	}
}
