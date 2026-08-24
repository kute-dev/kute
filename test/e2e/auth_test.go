//go:build e2e && e2e_auth

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

const authExpiredMarker = "KUTE-E2E-AUTH-EXPIRED: run the test login command"

// TestDirectUnauthorizedPausesHealthChecks covers the apiserver-originated
// half of ConnUnauthenticated separately from exec plugin failures. A 401 is
// a whole-connection fact: cached rows stay useful, writes disappear, and no
// automatic /livez loop keeps retrying until the user asks it to.
func TestDirectUnauthorizedPausesHealthChecks(t *testing.T) {
	a := Launch(t)
	a.WaitForAll(Connect, "api-", "worker-")
	proxy := a.Proxy()

	fence := proxy.Fence()
	proxy.FailNext(RequestMatcher{Path: "/livez"}, http.StatusUnauthorized, 1)
	proxy.WaitForRequest(fence, RequestMatcher{Path: "/livez"}, Settle)
	a.WaitForAll(Settle, "credentials expired", "api-", "worker-", "mutating actions disabled")

	// The delete shortcut must not reach even its non-prod inline confirm.
	a.Press("ctrl+d")
	a.Never("CONFIRM", 750*time.Millisecond)

	// This is deliberately a windowed wire assertion. A request-count
	// snapshot immediately after the 401 races the old 2s ticker and proves
	// nothing about whether it was actually stopped.
	proxy.NeverRequest(proxy.Fence(), RequestMatcher{Path: "/livez"}, 4500*time.Millisecond)

	retryFence := proxy.Fence()
	a.Press("r")
	proxy.WaitForRequest(retryFence, RequestMatcher{Path: "/livez"}, Settle)
	a.WaitGone("credentials expired", Settle)
	a.WaitFor("connected", Settle)
}

// TestExecCredentialExpiryRecoversWithoutRestart exercises the real
// client-go exec Authenticator. The plugin first returns the readable
// ServiceAccount's token with a short client-side expiry, then fails with its
// own recognizable stderr. Recovery changes only the plugin's external state
// and presses r; kute itself is never restarted.
func TestExecCredentialExpiryRecoversWithoutRestart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the temporary exec credential plugin is a POSIX shell script")
	}
	RequireCluster(t)

	proxy := NewAPIProxyForwardingClientAuth(t, KubeconfigPath())
	plugin := newCredentialPlugin(t, partialServiceAccountToken(t))
	execConfig := execCredentialKubeconfig(t, proxy.KubeconfigPath(), plugin)
	a := Launch(t, WithKubeconfig(execConfig), WithoutAPIProxy())
	a.WaitForAll(Connect, "api-", "worker-")

	// The first mint stays valid long enough for the eager LIST/WATCH set to
	// fill on a cold runner, but is still short enough for a bounded nightly
	// test. If startup itself crosses the deadline, the healthy plugin simply
	// remints until this gate switches it to failure.
	plugin.fail(t)
	a.WaitForAll(Settle, "credentials expired", "api-", "worker-", "mutating actions disabled")
	a.WaitFor(authExpiredMarker, Settle)
	failedRuns := waitForPluginRuns(t, plugin.countPath, 2, Settle)

	// Active watches authenticated before expiry can keep streaming. Leave
	// them intact for this window so the counter isolates the health loop:
	// after ConnUnauthenticated, it must not periodically re-run the plugin.
	neverPluginRunsAgain(t, plugin.countPath, failedRuns, 4500*time.Millisecond)

	// Once the cached rows are known to be preserved, break the authenticated
	// watches too. A server-side mutation made through the admin client must
	// remain absent until credentials recover and new watches attach.
	if closed := proxy.CloseActive(RequestMatcher{Verb: "WATCH"}); closed == 0 {
		t.Fatal("authentication test found no active watches to close")
	}
	marker := fmt.Sprintf("auth-recovery-%d", time.Now().UnixNano())
	createDisposablePod(t, marker, map[string]string{"phase": "auth-expired"})
	a.Never(marker, time.Second)

	plugin.succeed(t, time.Now().Add(10*time.Minute))
	retryFence := proxy.Fence()
	a.Press("r")
	proxy.WaitForRequest(retryFence, RequestMatcher{Path: "/livez"}, Settle)
	a.WaitGone("credentials expired", Settle)
	a.WaitFor(marker, Settle)
	a.WaitFor("connected", Settle)
}

type credentialPlugin struct {
	modePath     string
	countPath    string
	responsePath string
	token        string
}

func newCredentialPlugin(t *testing.T, token string) credentialPlugin {
	t.Helper()
	dir := t.TempDir()
	p := credentialPlugin{
		modePath:     filepath.Join(dir, "mode"),
		countPath:    filepath.Join(dir, "runs"),
		responsePath: filepath.Join(dir, "credential.json"),
		token:        token,
	}
	writeControlFile(t, p.modePath, []byte("healthy\n"), 0o600)
	p.succeed(t, time.Now().Add(20*time.Second))

	path := filepath.Join(dir, "credential-plugin.sh")
	body := `#!/bin/sh
printf 'run\n' >> "$2"
if [ "$(cat "$1")" = "fail" ]; then
  printf '%s\n' '` + authExpiredMarker + `' >&2
  exit 42
fi
cat "$3"
`
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("writing exec credential plugin: %v", err)
	}
	// Store the executable beside its controls without adding another field
	// to the test's public assertions.
	writeControlFile(t, filepath.Join(dir, "command"), []byte(path), 0o600)
	return p
}

func (p credentialPlugin) command(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(filepath.Dir(p.modePath), "command"))
	if err != nil {
		t.Fatalf("reading credential plugin command: %v", err)
	}
	return string(b)
}

func (p credentialPlugin) fail(t *testing.T) {
	t.Helper()
	writeControlFile(t, p.modePath, []byte("fail\n"), 0o600)
}

func (p credentialPlugin) succeed(t *testing.T, expires time.Time) {
	t.Helper()
	doc := map[string]any{
		"apiVersion": "client.authentication.k8s.io/v1beta1",
		"kind":       "ExecCredential",
		"status": map[string]string{
			"token":               p.token,
			"expirationTimestamp": expires.UTC().Format(time.RFC3339),
		},
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("encoding exec credential response: %v", err)
	}
	b = append(b, '\n')
	writeControlFile(t, p.responsePath, b, 0o600)
	writeControlFile(t, p.modePath, []byte("healthy\n"), 0o600)
}

// writeControlFile replaces a plugin control atomically, so a mint racing the
// test never observes a truncated mode or JSON document.
func writeControlFile(t *testing.T, path string, body []byte, mode os.FileMode) {
	t.Helper()
	tmp := path + ".new"
	if err := os.WriteFile(tmp, body, mode); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatalf("replacing %s: %v", path, err)
	}
}

func execCredentialKubeconfig(t *testing.T, proxiedBase string, plugin credentialPlugin) string {
	t.Helper()
	cfg, err := clientcmd.LoadFromFile(proxiedBase)
	if err != nil {
		t.Fatalf("loading proxied kubeconfig: %v", err)
	}
	ctx := cfg.Contexts[cfg.CurrentContext]
	if ctx == nil {
		t.Fatalf("proxied kubeconfig has no current context %q", cfg.CurrentContext)
	}
	const user = "kute-e2e-exec-auth"
	ctx.AuthInfo = user
	cfg.AuthInfos = map[string]*clientcmdapi.AuthInfo{
		user: {
			Exec: &clientcmdapi.ExecConfig{
				APIVersion:      "client.authentication.k8s.io/v1beta1",
				Command:         plugin.command(t),
				Args:            []string{plugin.modePath, plugin.countPath, plugin.responsePath},
				InteractiveMode: clientcmdapi.IfAvailableExecInteractiveMode,
			},
		},
	}
	path := filepath.Join(t.TempDir(), "exec.kubeconfig")
	if err := clientcmd.WriteToFile(*cfg, path); err != nil {
		t.Fatalf("writing exec kubeconfig: %v", err)
	}
	return path
}

func partialServiceAccountToken(t *testing.T) string {
	t.Helper()
	cfg, err := clientcmd.LoadFromFile(PartialKubeconfigPath())
	if err != nil {
		t.Fatalf("loading partial-identity kubeconfig: %v", err)
	}
	ctx := cfg.Contexts[cfg.CurrentContext]
	if ctx == nil || cfg.AuthInfos[ctx.AuthInfo] == nil {
		t.Fatalf("partial-identity kubeconfig has no current auth info")
	}
	auth := cfg.AuthInfos[ctx.AuthInfo]
	if auth.Token != "" {
		return auth.Token
	}
	if auth.TokenFile != "" {
		b, readErr := os.ReadFile(auth.TokenFile)
		if readErr != nil {
			t.Fatalf("reading partial-identity token file: %v", readErr)
		}
		if token := strings.TrimSpace(string(b)); token != "" {
			return token
		}
	}
	t.Fatal("partial-identity kubeconfig contains no bearer token")
	return ""
}

func pluginRuns(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatalf("reading credential plugin run count: %v", err)
	}
	return strings.Count(string(b), "run\n")
}

func waitForPluginRuns(t *testing.T, path string, want int, timeout time.Duration) int {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if got := pluginRuns(t, path); got >= want {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("credential plugin ran %d times, want at least %d", pluginRuns(t, path), want)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func neverPluginRunsAgain(t *testing.T, path string, before int, window time.Duration) {
	t.Helper()
	deadline := time.Now().Add(window)
	for time.Now().Before(deadline) {
		if got := pluginRuns(t, path); got != before {
			t.Fatalf("credential plugin kept running while unauthenticated: before=%d after=%d", before, got)
		}
		time.Sleep(25 * time.Millisecond)
	}
}
