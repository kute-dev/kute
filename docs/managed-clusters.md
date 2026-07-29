# kute — GKE and EKS support

What kute needs in order to work on Google's and Amazon's managed Kubernetes, given that
validation so far covers MicroK8s and AKS (both Kubernetes v1.35).

Feeds [`beta-plan.md`](beta-plan.md) §2's EKS and GKE rows, which this replaces with
something more specific than "walk the everyday flows".

**Status (2026-07-29).** §1, §2, §3's node-shell bullet and §4 are implemented, with the
fixture-plugin coverage living in `internal/kube/execauth_test.go` rather than an E2E suite
(see Sequencing). Two corrections to what's written below, found in `client-go v0.36.3`
while implementing it:

- The `IfAvailable` default in §1 applies to **v1beta1/v1alpha1** exec blocks only
  (`SetDefaults_ExecConfig` requires v1 to declare `interactiveMode` explicitly). That is
  still exactly the EKS case — `aws eks update-kubeconfig` writes v1beta1 — and GKE writes
  v1 with `interactiveMode: IfAvailable` spelled out, so both providers land on the same
  behaviour the section describes.
- §2's "client-go already propagates plugin stderr in the error" is **not true**:
  `exec.go` sets `cmd.Stderr = os.Stderr` and never reads it back, so the plugin's message
  goes straight onto the rendered frame and never into any error. Surfacing it in
  `ConnState.Err` therefore needed capturing it — `kube/execauth.go` substitutes a pipe for
  `os.Stderr` for the moment client-go constructs its Authenticator, which is the one
  window where that decision is made. This makes §1's "writes to stdout/stderr straight
  over the rendered frame" a fixed problem rather than a residual one.

---

## The one difference that matters

MicroK8s and AKS both hand you a **long-lived** credential. MicroK8s writes a client
certificate; so does `az aks get-credentials --admin`. (The AAD path is different — it
writes a `kubelogin` exec plugin, so an AAD-authenticated AKS validation would already have
touched some of what follows.)

GKE and EKS are the first clusters where the credential is a **short-lived token minted by
an exec credential plugin** and re-minted *during* a session:

| | Plugin | Token lifetime |
| --- | --- | --- |
| GKE | `gke-gcloud-auth-plugin` | ~1 hour |
| EKS | `aws eks get-token` | ~15 minutes |

Everything below follows from that. There is no per-provider code to write: exec credential
plugins are a generic client-go mechanism, kute already drives them for free, and the
in-tree `gcp`/`azure` auth providers were removed in Kubernetes 1.26 — `client-go v0.36.3`'s
`plugin/pkg/client/auth/plugins.go` now registers only `oidc`. No vendor SDK, no
`aws-iam-authenticator` or `gke-gcloud-auth-plugin` vendoring. Requiring the plugin binary
on the user's `PATH` is their setup, not ours.

The work is entirely in **how a TUI shares a terminal with a subprocess**, and in
**classifying 401**.

---

## 1. The exec plugin is handed raw `os.Stdin` while Bubble Tea owns the terminal

The serious one. The chain, in `client-go v0.36.3`:

1. `tools/clientcmd/api/v1/defaults.go:31` — an exec block with no `interactiveMode`
   defaults to `IfAvailable`, "for backwards compatibility". That is exactly what
   `aws eks update-kubeconfig` writes; GKE writes `interactiveMode: IfAvailable`
   explicitly.
2. `plugin/pkg/client/auth/exec/exec.go:239` — `IfAvailable` resolves to
   `shouldBeInteractive = !config.StdinUnavailable && isTerminalFunc(os.Stdin.Fd())`.
3. `exec.go:212` sets `stdin: os.Stdin`; `exec.go:462` does `cmd.Stdin = a.stdin`.

kute runs on a TTY, so `isTerminalFunc` is true, and `StdinUnavailable` is never set —
`ExecProvider` appears nowhere in `internal/`. So client-go hands the real `os.Stdin` to the
credential plugin **while Bubble Tea holds the terminal in raw mode**. Two readers on one
file descriptor: keystrokes are stolen, and a plugin that prompts (expired SSO, `gcloud`
re-auth, MFA) writes to stdout/stderr straight over the rendered frame.

This does not fire at launch, where a prompt might be survivable. Credentials are minted
lazily and refreshed on expiry, so it fires at an arbitrary point hours into a session, from
a background informer goroutine.

**Fix.** In `newClientForContext` (`internal/kube/client.go:126`), after `restConfig` is
built and alongside the existing QPS/Burst adjustment, set:

```go
if restConfig.ExecProvider != nil {
    restConfig.ExecProvider.StdinUnavailable = true
    restConfig.ExecProvider.StdinUnavailableMessage = "kute holds the terminal; re-authenticate in another shell and press r to retry"
}
```

`StdinUnavailable` is deliberately not settable from a kubeconfig —
`tools/clientcmd/api/v1/zz_generated.conversion.go:402` marks it "opted out of conversion
generation" — because it exists for exactly this case: a client that knows stdin is already
spoken for. Setting it turns a display-corrupting hang into a clean error, and for an
`interactiveMode: Always` plugin turns it into `exec.go:245`'s explicit
"standard input is unavailable: <our message>".

## 2. A 401 is indistinguishable from a network failure

`internal/kube/health.go:67` lumps 401 in with "a refused connection, DNS failure, TLS
error" — all flip to `ConnReconnecting` and drive the 4c unreachable-context screen, with
backoff 1s→2s→…→30s. There is no `apierrors.IsUnauthorized` check anywhere in the tree; the
only auth classification is `IsForbidden`, in `logs.go:88`.

On a cert-based cluster that is correct — a 401 there really does mean something is broken
and retrying is reasonable. On EKS with an expired SSO session, kute retries a plugin that
cannot succeed, forever, showing "reconnecting" where it should show "credentials expired".
This is the failure users will actually hit, and the one that produces a bug report saying
"kute can't connect to my cluster".

**Fix.** A fifth `ConnPhase` beside `ConnConnected`/`ConnReconnecting`/`ConnFailed`/
`ConnNoCluster` (`health.go:16-21`) — call it `ConnUnauthenticated` — entered when the ping
or a watch returns `apierrors.IsUnauthorized`. It should:

- count as `ConnState.Offline()` (`health.go:42`), so the existing 4a banner, header badge
  and mutating-verb gate all treat it correctly with no change;
- **stop the backoff loop**, since re-running a failing plugin is not a retry strategy;
- surface the plugin's own stderr verbatim in `ConnState.Err`. Better than kute guessing at
  `aws sso login` versus `gcloud auth login` — client-go already propagates plugin stderr in
  the error, and the plugin knows what the user needs to run. Fixing §1 first is what makes
  that error clean rather than interleaved with a half-drawn prompt.
- offer an explicit retry on the 4c screen, so re-authenticating in another shell and
  pressing a key is the recovery path.

## 3. Feature availability, not auth

Neither needs code today, but both need to be a documented expectation rather than a
surprise:

- **Stock EKS ships no metrics-server** (GKE does). CPU/MEM render `–` everywhere — the same
  path kind exercises, already covered by [`e2e-plan.md`](e2e-plan.md)'s no-metrics-server
  row. Nothing to build; confirm it degrades rather than lies.
- **Node shell cannot work on GKE Autopilot or EKS Fargate.** `internal/kube/nodeshell.go`
  shells out to `kubectl debug --profile=sysadmin`, which needs a privileged host-namespace
  container. Autopilot rejects those; Fargate has no node to attach to. The honest handling
  is a clear error naming the reason, not a hidden key.

## 4. Adjacent, cheap, not GKE/EKS

kute has no blank import of `k8s.io/client-go/plugin/pkg/client/auth`. This does **not**
affect GKE or EKS — exec plugins are wired into the REST config path directly — but in v0.36
that package registers OIDC, so one line buys OIDC-authenticated clusters (Rancher, Dex,
Keycloak). Worth doing while in this code, worth nothing to these two providers.

---

## Testing this without a cloud account

The whole of §1 and §2 is testable on kind, in CI, with no GCP or AWS credentials — which is
what stops this becoming "go rent an EKS cluster".

An exec credential plugin is just a binary that prints an `ExecCredential` JSON document to
stdout. A handful of shell-script fixtures in the E2E suite covers every path that matters:

| Fixture plugin | Proves |
| --- | --- |
| prints a valid token, exits 0 | the happy path works at all |
| prints a token with a 5-second `expirationTimestamp` | mid-session refresh, the case AKS/MicroK8s never reach |
| reads stdin before printing | §1 — blocks forever without `StdinUnavailable`, errors cleanly with it |
| exits non-zero with a message on stderr | §2 — the message reaches `ConnState.Err` and the phase is `ConnUnauthenticated`, not `ConnReconnecting` |

Point them at the kind cluster's real CA and server, with the plugin minting a real
ServiceAccount token. That is a faithful reproduction of the EKS and GKE credential shape.

Real GKE and EKS clusters are still worth one manual walk each — for discovery differences,
the GKE metrics shape, and Autopilot's admission rules — but they stop being the only way to
catch a regression.

**Where the fixtures actually landed.** The E2E suite [`e2e-plan.md`](e2e-plan.md) describes
doesn't exist yet, so all four fixture plugins ship as ordinary unit tests in
`internal/kube/execauth_test.go`, driving a real client-go client (real kubeconfig, real
exec provider, real subprocess) against an `httptest.NewTLSServer` standing in for the
apiserver. Everything in the table above is asserted there today except one thing, which no
test without a pty can reach: `go test`'s stdin is not a terminal, so client-go's
`IfAvailable` check comes out non-interactive whether or not `StdinUnavailable` is set. The
fix's *input* is asserted directly instead, and the tty-attached run stays the manual step
in Verification below. When the E2E suite lands, these move to it as-is — pointing them at
kind's real CA and server is a change of two fields.

Two details worth keeping if these are ever rewritten: the stub apiserver must be **TLS**
(clientcmd skips a user's auth information entirely on a plain-HTTP connection, silently
producing a config with no `ExecProvider` at all), and the fixture kubeconfigs use
`client.authentication.k8s.io/v1beta1` because that is the version whose `interactiveMode`
defaults, and the one `aws eks update-kubeconfig` writes.

## Sequencing

1. ✅ `fix(kube): stop credential plugins stealing the terminal from the running app` — §1,
   plus the stderr capture the correction above explains. On its own, and first: it is the
   prerequisite for §2's error message being legible.
2. ~~`test(e2e): drive kute against a cluster behind an exec credential plugin`~~ — folded
   into 1 as unit tests, since the E2E suite doesn't exist yet. See "Where the fixtures
   actually landed".
3. ✅ `fix(tui): say that credentials expired instead of retrying forever` — §2.
4. ✅ `fix(kube): explain why node shell is unavailable on this cluster` — §3's second
   bullet.
5. ✅ `feat(kube): support OIDC-authenticated clusters` — §4, one line plus a note in the
   README's auth expectations.
6. Manual walk of one GKE and one EKS cluster, recorded against `beta-plan.md` §2. **Still
   outstanding** — the only item here that needs a cloud account.

§3's first bullet needed no code, as predicted, and was confirmed rather than assumed:
`browse`'s `podMetricsLoadedMsg`/`nodeMetricsLoadedMsg` handlers drop a failed metrics poll
instead of surfacing it, leaving the CPU/MEM cells on their `–` placeholder. A cluster with
no metrics-server degrades; it doesn't lie or error.

## Verification

```sh
go test -race ./internal/kube             # includes the exec-plugin fixtures
```

Then, on the kind cluster with the stdin-reading fixture plugin installed: launch kute,
navigate for longer than the fixture token's lifetime, and confirm the frame is never
corrupted and no keystroke is lost. Before the §1 fix, that same run should visibly break —
if it doesn't, the fixture isn't reading stdin the way a real plugin does.
