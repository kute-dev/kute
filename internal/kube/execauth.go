package kube

import (
	"os"
	"sync"
	"time"

	"k8s.io/client-go/rest"
)

// Exec credential plugins (gke-gcloud-auth-plugin, `aws eks get-token`,
// kubelogin) are the credential shape kute meets on GKE and EKS, and the
// first one that is re-minted *during* a session rather than read once from
// the kubeconfig: a GKE token lasts about an hour, an EKS one about fifteen
// minutes. client-go re-runs the plugin binary when the current credential
// expires, from whichever goroutine happens to make the next request — an
// informer's watch, the /livez ping — at an arbitrary point hours after
// launch.
//
// That subprocess and kute are then fighting over one terminal, on both ends:
//
//   - stdin. A v1beta1 exec block with no interactiveMode defaults to
//     IfAvailable "for backwards compatibility"
//     (tools/clientcmd/api/v1/defaults.go), which is exactly what `aws eks
//     update-kubeconfig` writes; GKE writes v1 with interactiveMode:
//     IfAvailable spelled out. Either way IfAvailable resolves to
//     "interactive if os.Stdin is a terminal" — which it is, because kute
//     runs on a tty. The
//     plugin is then handed the real os.Stdin while Bubble Tea holds it in
//     raw mode: two readers on one descriptor, so keystrokes go missing, and
//     a plugin that prompts (expired SSO, gcloud re-auth, MFA) waits forever
//     for input the user cannot give it.
//   - stderr. client-go hands the plugin os.Stderr verbatim
//     (plugin/pkg/client/auth/exec/exec.go's `cmd.Stderr = a.stderr`) and
//     never reads it back, so anything the plugin prints lands on top of the
//     rendered frame — the same splatter silenceKlog exists to prevent, from
//     a source klog can't cover.
//
// Both are fixed here, in one place, because both are the same fact: kute
// owns the terminal, and the credential plugin has to be told so.

// credentialStdinMessage is what client-go appends to its own "standard input
// is unavailable" error for an interactiveMode: Always plugin. It names the
// recovery path, since kute cannot offer the plugin's own prompt: another
// shell, then 4a/4c's r.
const credentialStdinMessage = "kute holds the terminal; re-authenticate in another shell and press r to retry"

// prepareExecProvider tells client-go that this process's stdin is spoken for
// before any credential plugin runs. StdinUnavailable is deliberately not
// settable from a kubeconfig (tools/clientcmd/api/v1/zz_generated.conversion.go
// marks it "opted out of conversion generation") precisely because it is the
// client's statement about itself, not the cluster's: setting it turns an
// IfAvailable plugin non-interactive, and turns an Always plugin's silent
// hang into an immediate, legible error.
func prepareExecProvider(cfg *rest.Config) {
	if cfg == nil || cfg.ExecProvider == nil {
		return
	}
	cfg.ExecProvider.StdinUnavailable = true
	cfg.ExecProvider.StdinUnavailableMessage = credentialStdinMessage
}

// maxCapturedStderr bounds the credential-plugin stderr kept in memory. A
// plugin's failure message is a line or two; this is generous enough for a
// stack-trace-happy one and small enough to keep unconditionally.
const maxCapturedStderr = 4 << 10

// stderrCapture holds the pipe kute substitutes for os.Stderr while client-go
// builds its exec Authenticator, plus a ring of the bytes the plugin has
// written to it since the last read.
//
// The substitution has to happen around client construction rather than
// around the plugin run, because client-go reads os.Stderr once — when the
// Authenticator is created — and keeps that writer for the process's life
// (exec.newAuthenticator's `stderr: os.Stderr`). Authenticators are cached
// globally by exec config, so the first client built for a given kubeconfig
// user is the one that decides where every later token mint's stderr goes.
type stderrCapture struct {
	once sync.Once
	w    *os.File

	mu  sync.Mutex
	buf []byte
}

var pluginStderr stderrCapture

// writer lazily creates the pipe and the goroutine draining it. Returns nil
// if the pipe can't be created, in which case capture is skipped and stderr
// keeps its normal (frame-corrupting, but not worse-than-today) behaviour.
func (s *stderrCapture) writer() *os.File {
	s.once.Do(func() {
		r, w, err := os.Pipe()
		if err != nil {
			return
		}
		s.w = w
		go s.drain(r)
	})
	return s.w
}

// drain reads the pipe forever, appending to the ring. It must keep reading
// whether or not anyone ever asks for the contents: a full pipe buffer would
// block the credential plugin's own write, which is the hang this file exists
// to prevent.
func (s *stderrCapture) drain(r *os.File) {
	chunk := make([]byte, 1024)
	for {
		n, err := r.Read(chunk)
		if n > 0 {
			s.append(chunk[:n])
		}
		if err != nil {
			return
		}
	}
}

func (s *stderrCapture) append(p []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.buf = append(s.buf, p...)
	if len(s.buf) > maxCapturedStderr {
		s.buf = s.buf[len(s.buf)-maxCapturedStderr:]
	}
}

// capture runs fn with os.Stderr pointed at the pipe, so any exec
// Authenticator fn constructs adopts it. The swap is process-global for the
// duration, hence the lock: ProbeContexts builds clients for several contexts
// concurrently, and client construction does no I/O, so serialising it costs
// nothing.
var captureMu sync.Mutex

func (s *stderrCapture) capture(fn func()) {
	w := s.writer()
	if w == nil {
		fn()
		return
	}
	captureMu.Lock()
	real := os.Stderr
	os.Stderr = w
	defer func() {
		os.Stderr = real
		captureMu.Unlock()
	}()
	fn()
}

// take returns everything the credential plugin has written since the last
// call and clears the ring, so a later failure never reports a stale message.
//
// It waits briefly for content when the ring is empty because the caller is
// on the other side of a race it can't otherwise close: the plugin writes,
// exits, and client-go turns the non-zero exit into an error, all before the
// draining goroutine is necessarily scheduled. The wait only ever happens on
// a path that has already decided a credential error occurred, and never on
// the render goroutine.
func (s *stderrCapture) take() string {
	deadline := time.Now().Add(100 * time.Millisecond)
	for {
		s.mu.Lock()
		out := string(s.buf)
		s.buf = nil
		s.mu.Unlock()
		if out != "" || time.Now().After(deadline) {
			return out
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// CredentialPluginOutput returns and clears whatever the exec credential
// plugin last wrote to stderr — the plugin's own account of why it couldn't
// mint a token ("Error loading SSO Token: Token has expired"), which
// client-go itself discards. Empty when there is no exec plugin, or when it
// said nothing.
func CredentialPluginOutput() string { return pluginStderr.take() }
