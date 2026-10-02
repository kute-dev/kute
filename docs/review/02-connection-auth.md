# 02 — Connection, contexts & auth

Scope: `internal/kube/{context,execauth,probe,health,capability,client,request_gate}.go`, the
context-switch path (`tui.switchContextCmd`, `Cluster.SwitchContext`, `app.attemptReconnect`,
`app.attemptSwitchContext`), `internal/tui/{context,sessionctx,clusterwatch}.go`, the root shell's
4c swap logic (`tui/model.go:593-660`, `:716-745`), `tasks/setup`, and `internal/app/session.go`.
`docs/managed-clusters.md` does not exist (CLAUDE.md points at it; see L9).

Verification: `go vet` is clean, and `go test` passes for `./internal/kube/ ./internal/tui/
./internal/tui/tasks/setup/ ./internal/app/`. Four findings were reproduced with throwaway tests
injected through `go test -overlay`, so nothing in the tree was changed:

- **H1:** `probeContextsWith` blocks its caller.
- **M1:** the ping path overwrites the plugin's stderr.
- **M2:** a probe's stderr leaks into the active banner.
- **M3:** `ping` holds `c.mu` for about 120 ms.

`client-go` v0.37.1's `exec.go:458` was read to confirm that exec plugins run without a context.
Everything else was traced by hand to the `file:line` given. Anything not observed at runtime is
marked **unconfirmed**.

Already covered by 01, so not repeated here: watch-EOF reported as an outage (01 M3), the
`ListRaw`/`ensureKind` switch race (01 M1), discovery and `tzCapability` racing a switch (01 M2),
and the launch-time CRD-kind restore (01 M5).

## Summary

The pieces that 4a/4c lean on most are carefully built:

- The authentication gate and its `/livez` single-shot permit.
- The generation-guarded health loop.
- `StdinUnavailable` plus stderr capture, which keep exec plugins off the terminal.
- `ResetClusterContext` ordering.

The defects sit around that core, in four areas:

1. **The context probe runs on the Update loop.** `ProbeContexts` gained an `errgroup.SetLimit(8)`,
   and `g.Go` *blocks* at the limit. The probe loop runs synchronously inside
   `startContextProbe`/`setup.Init`, so opening `c` with more than 8 contexts freezes the UI for up
   to `probeTimeout` per wave. Exec plugins ignore the probe's deadline, so with hanging plugins
   the freeze has no upper bound.
2. **Credential error text is unreliable.** The plugin-stderr ring is one process-global buffer that
   every probe and every previous context writes into. The ping path also overwrites the good
   message with client-go's generic one about 100 ms later, while holding `Cluster.mu`.
3. **The rebuild paths are fragile:**
   - `attemptReconnect`/`attemptSwitchContext` write `Session` from a Cmd goroutine.
   - They stop the old cluster before the new one exists, which leaves `r` dead after a failed
     build.
   - Neither has a double-submit guard.
   - Both ignore `--kubeconfig`/`--context`.
   - A failed 7a switch is silent, and it always rebuilds the old context, even when nothing was
     torn down.
4. **An unreachable cluster at launch hides behind connect grace.** A blackholed apiserver (VPN
   off, private endpoint) times out rather than refusing. `connectGrace` forgives timeouts for 90 s,
   and every lazy informer start re-arms that window. 4c therefore never appears while the user
   pokes around, despite §4c/§13 saying "on timeout 4c takes over".

Counts: **2 high, 9 medium, 9 low.**

## Findings

| # | Sev | Location | Issue | Suggestion |
|---|---|---|---|---|
| H1 | high | `kube/probe.go:54-60`; `tui/context.go:187`; `tui/model.go:1243`; `setup/model.go:137` | `probeContextsWith` calls `g.Go` in a loop on the caller's goroutine. `SetLimit(8)` makes `g.Go` block, and the callers are the root `Update` (`startContextProbe`) and `setup.Init`. With more than 8 contexts, opening `c` or reaching 4c freezes the UI until earlier probes finish: about `probeTimeout` (6 s) per extra wave, and unbounded if 8 exec plugins hang, because client-go runs plugins with `exec.Command` and no context (`client-go/.../exec.go:458`). Reproduced: 16 names at 300 ms per probe blocked the caller for 300 ms. | Move the loop into the goroutine that already closes `out`, e.g. `go func(){ for … g.Go(…); g.Wait(); close(out) }()`. Then bound each probe's wall time independently of the plugin, e.g. abandon the result after `probeTimeout` via a `select`. |
| H2 | high | `kube/health.go:478-486`, `:268`, `:434`; `kube/cluster.go:592`, `dynamic.go:93`, `helm.go:640` | `inConnectGrace` forgives *timeouts* for 90 s whenever any started kind is unsynced, and does not check whether the cluster has ever been reached. A launch against a blackholed endpoint (dial i/o timeout, ping `DeadlineExceeded`) stays on browse's spinner for ≥90 s before 4c. `noteListBurst` re-arms the window on every lazy informer start, so a user who navigates kinds during the spinner never gets 4c. | Apply grace only once `c.reached` is true, since the premise is "a link we know works is busy". Pass `reached` into `recordPing`/`onWatchError` next to `synced`, and do not let `noteListBurst` extend grace for a never-reached cluster. |
| M1 | medium | `kube/request_gate.go:96-100` → `cluster.go:864-873`; `health.go:364-395`, `:425-431` | When a credential plugin fails on the health ping, the round tripper's `gate.block` callback runs `recordAuthenticationFailure` → `setUnauthenticated` → `take()`, which consumes the plugin stderr and emits the good message. `ping` then calls `recordPing` with the same error. `needsReauth` → `setUnauthenticated` → `take()` returns `""`, so a second state carrying client-go's `getting credentials: exec: executable … failed with exit code 255` replaces it. Reproduced: two states emitted, and the final `Err` is the generic one. The e2e `WaitFor(authExpiredMarker)` passes only by catching the transient first frame. | In `recordPing`/`onWatchError`, when the phase is already `ConnUnauthenticated` and nothing changed, keep `prev.Err`; only re-take on an explicit `RetryNow`. Alternatively, have `setUnauthenticated` fall back to `prev.Err` when the ring is empty and `prev` is already unauthenticated. |
| M2 | medium | `kube/execauth.go:102`, `:166-180`, `:293-306`; `probe.go:69-79` | `pluginStderr` is one process-global ring, and every client build (every probe, every past context) points its plugin's stderr at it. Only a credential failure on the active cluster drains it. Three effects follow. First, a probe of another EKS/GKE context that fails (or a plugin that prints warnings on *successful* mints) leaves text that becomes the active cluster's banner at its next auth failure; reproduced: "OTHER CONTEXT: run gcloud auth login" became the active banner. Second, the text survives `SwitchContext`. Third, opening `c` runs the exec plugin of every context (**unconfirmed**: kubelogin's default auth-code flow may open a browser per OIDC context). | Make the capture per client: give `newClientForContext` its own ring and keep it on `Client`/`Cluster`. The Authenticator cache is keyed per exec config, so key the ring by that too, or drain it on `SwitchContext`. Have `ProbeContexts` ignore stderr, and consider skipping exec-plugin contexts whose cached token has expired (label them "needs login"). |
| M3 | medium | `health.go:374-394`; `cluster.go:824-842`, `:864-873`; `execauth.go:201-244` | `take()` waits at least 100 ms, longer if the plugin keeps writing. It is reached with `Cluster.mu` held from `ping` (deferred unlock across `recordPing`), `recordWatchError`, and `recordAuthenticationFailure`. Every credential failure therefore stalls anything on the Update loop that touches `c.mu` (`ListRaw`→`ensureKind`, `KindSynced`). Reproduced: `ping` took 121 ms. This also contradicts `go-practices-review.md`'s "ping() takes and releases c.mu before recordPing", which is no longer true. | Compute the message (`credentialErrorMessage`) *before* taking `c.mu`, and pass the string into `setUnauthenticated`. Keep only the generation check and state write inside the critical section. |
| M4 | medium | `app/app.go:784-838`, `:847-889`; `setup/switchcontext.go:98-110`, `setup/keys.go:246-257` | `attemptReconnect`/`attemptSwitchContext` write `sess.Cluster`, `Lister`, `Metrics`, `Location`, `Registry`, `Groups` and `Forwards` from the Cmd goroutine. Meanwhile the Update loop reads them: `model.go:635` `m.session.Cluster`, the `c` palette (which setup allows, as it's a `Screen`), and `switchContextCmd`. That violates CLAUDE.md's "Session reads/writes happen before the closure". Setup also doesn't guard `↵`/`r` while `retrying`, so a double press runs two rebuilds, and the losing `*Cluster` is never stopped (leaked informers and health loop). `c` + `↵` during a 4c rebuild calls `SwitchContext` on the cluster that was just `Stop`ped, which revives it. Race-detector hit is **unconfirmed** (no test drives it). | Build the new cluster in the Cmd. Return it in `ReplaceRootMsg` and do all `Session` writes in the root's `ReplaceRootMsg` handler. Ignore `↵`/`r`/`e` while `m.retrying`. |
| M5 | medium | `app/app.go:794-799`, `:850-855`; `setup/keys.go:248-250`; `app.go:689` | Both rebuild paths `Stop()` the current cluster *before* `NewCluster…` can fail. On failure, `RetryFailedMsg` leaves 4c up with `RetryNow` bound to the stopped cluster, whose health loop has exited, so plain `r` silently does nothing from then on. | Build first, and stop the old cluster only after the new one exists. On failure, keep the old one running. |
| M6 | medium | `tui/context.go:329-341`; `tui/model.go:716-745`; `browse/update.go:213-216`; `kube/cluster.go:1071-1075` | A failed 7a switch is invisible: the root and browse both ignore `SwitchContextMsg.Err`, and there is no "switching…" indicator for the up to 30 s (switch plus rollback). The rollback runs even when `NewClientForContext` failed before anything was torn down, which needlessly rebuilds the healthy cluster. A real rollback also wipes every lazy informer, and nothing tells browse to reload, so a Secrets/CRD list freezes with no further events. During the switch window, informer events from the *target* reload browse's old kind/namespace against the target's caches under the old header, which can produce a false empty state. | Return a typed "no-op failure" from `SwitchContext` when the client build fails, and skip the rollback in that case. Surface `Err` as a transient header/flash line. After rollback, send a reload (or `SwitchContextMsg` with the old location) so browse re-reads. Suppress task reloads while a switch is in flight (a root `switching` flag). |
| M7 | medium | `app/app.go:786-788`, `:797`; `kube/client.go:60-71` | `attemptReconnect` repoints only `$KUBECONFIG`. `kubeconfigSource` prefers the `--kubeconfig` flag, so 10b's `k` and 4c's `e` are inert for any session launched with `--kubeconfig`, and LOOKED IN keeps showing the flag path. It also calls `kube.NewCluster()`, which uses the kubeconfig's current-context. That drops `--context`, the restored recent context, and `-n`/PerContext restore, so 4c's `e` (even with the same path) silently lands on a different cluster. | Call `kube.SetKubeconfigPath(path)` when a path is entered. Re-resolve with `startupContext(cfg, sess.State)`, and apply the same namespace/kind restore `BuildSession` does (factor it out). |
| M8 | medium | `kube/client.go:193-200`, `:240-256`; `app/session.go:105-108` | Any `ClientConfig()` error is turned into a `ConfigLookupError`: an unknown `--context`, a stale context in 7a, a missing cert file. `kute --context typo` therefore renders 10b "no kubeconfig found", with the valid file listed as "invalid kubeconfig" (reproduced). `startupContext`'s comment says 4c. Worse, when `rest.InClusterConfig()` succeeds (kute run in a pod), every such error silently falls back to the in-cluster API server, while `ctx.ContextName` is still set to the *requested* name. The header and `config.IsProd(name)` then describe a cluster you are not on (**unconfirmed** at runtime). | Fall back to in-cluster only when no kubeconfig was found at all, i.e. `clientcmd.IsEmptyConfig(err)` or the explicit file is missing. Return other errors verbatim, with a distinct type for "context not found" so 4c/the palette can name it. |
| M9 | medium | `app/app.go:667-672`; `browse/model.go:1392-1410`; `tui/model.go:737-743` | Browse is rebuilt on a 7a switch only when the stack is non-empty. From the resting screen it keeps `currentUser` (and `OpenCronJobDetail`/`OpenJobAttempts`'s captured `UserName`, and `openLogs`' captured `clusterName`) from the **old** context. The next run-now writes the old identity into `kube.AnnotationTriggeredBy` on the new cluster (`job_actions.go:113`, `cronjob_actions.go:34`). `switchContext` also returns `nil` without reloading when the restored kind isn't in the new registry (CRD not installed there, or discovery timed out), which leaves the previous context's rows on screen. | Always rebuild browse via `buildBrowse` on a successful switch, as the pushed-stack path already does. Or read the user from `Session`/the cluster at use time rather than capturing it at build. If the restored kind is missing, fall back to Pods and reload. |
| L1 | low | `tui/context.go:290-299` | With no PerContext entry, `restoreNS` defaults to `"default"` instead of the target context's kubeconfig namespace. That differs from launch (`session.go:75`) and from 4c (`app.go:879`). A namespace-bound identity's first switch lands on a 403, and in scoped mode the eager Pod cache is scoped to `default`. | Leave `restoreNS` empty and fill it from the new `cluster.Context.Namespace` after `SwitchContext` returns. |
| L2 | low | `kube/health.go:19-20`, `:58`, `:345` | `ConnFailed` and `ConnNoCluster` are never assigned anywhere in the tree. The dead branches make `Offline()`/`nextProbeDelay` read as if a terminal-failure phase exists. | Delete them, or document that 10b's "○ no cluster" is meant to use `ConnNoCluster`. |
| L3 | low | `setup/model.go:91`, `setup/update.go:31` | `probeGen` is never incremented, so the stale-drain guard is inert. The SWITCH CONTEXT list is probed once at `Init` and never refreshed, unlike 7a's `r`. | Re-probe on `r` (bump `probeGen`), or drop the gen machinery. |
| L4 | low | `tui/context.go:150-174`; `kube/probe.go:69-79` | Every probe error renders "✕ unreachable", including an expired plugin credential and a 401/403 on `/livez` (anonymous access disabled). Those clusters are reachable, and §4c's "(current · timeout)" style wants the reason. | Classify with `IsAuthenticationError`/`IsPermissionError` and render "needs login" or "reachable · 403". |
| L5 | low | `kube/health.go:400-420` | After re-authenticating and pressing `r`, `/livez` succeeds but `watchUnhealthy` is non-empty, so the state becomes `Reconnecting` with `Err: prev.Err`. The banner keeps the stale "Token has expired…" text until the watches re-attach. | Clear `Err` (or say "re-establishing watches") when the previous phase was `ConnUnauthenticated`. |
| L6 | low | `kube/health.go:148-161` | `reset()` doesn't emit on `ch`. After a switch out of an `Unauthenticated`/`Reconnecting` context, the root's `m.conn`, and with it the OFFLINE pill and the mutating-verb gate, stays stale until the first ping (about 2 s). | Emit the fresh Connected state from `reset()`, or from `SwitchContext` after the unlock. |
| L7 | low | `kube/cluster.go:1030-1031`; `kube/rbac.go:328`; `app/app.go:621`, `:667-689` | `Cluster.Context` is written under `c.mu` (`SwitchNamespace`, `SwitchContext`, `SetNamespaceScope`) but read unlocked by `CurrentNamespace`/`CurrentContext`, `WhoCan` (a Cmd goroutine), and the composition root. | Add a locked `ContextSnapshot()` accessor and unexport the field. |
| L8 | low | `kube/execauth.go:172-178` | `capture` assigns the package var `os.Stderr` without any synchronisation visible to other readers. `captureMu` serialises writers only, so any concurrent `os.Stderr` reader races (**unconfirmed**: no reader observed during client builds). | Acceptable as documented. Note it in the comment, or avoid it by building the exec transport via `rest.Config.ExecProvider` + a custom `exec.Authenticator` writer if client-go ever exposes one. |
| L9 | low | `CLAUDE.md` (Architecture, `execauth.go` bullet) | It cites `docs/managed-clusters.md`, which doesn't exist. Separately, every reconnect leaves a `WatchCluster` Cmd blocked forever on the stopped cluster's never-closed channels (`tui/clusterwatch.go:32-46`), one goroutine per rebuild. | Restore or remove the doc reference. Have `Stop` close `events`/`ConnEvents`, or let `WatchCluster` select on a done channel. |

## Details (high / medium)

### H1 — opening the context palette can freeze the UI

`probeContextsWith` (`probe.go:54-60`):

```go
g.SetLimit(probeConcurrency)
for _, name := range names {
    g.Go(func() error { … })   // blocks once 8 are in flight
}
go func() { _ = g.Wait(); close(out) }()
```

The concurrency cap was added in response to `go-practices-review.md` §4, and the comment even
says "SetLimit blocks at g.Go". Every caller invokes `kube.ProbeContexts` synchronously:

- `startContextProbe` runs inside the root `Update` (`model.go:1243`), via `openPalette` and the
  palette's `r`.
- `setup.Model.Init` runs on the program goroutine (`setup/model.go:137`).

With N contexts, the caller blocks until N−8 probes have *finished*. Each probe is bounded by
`probeTimeout` only for the HTTP request. The exec plugin, run inside `RoundTrip` by client-go
(`exec.Command`, no context), can hang indefinitely: `kubelogin` waiting on a browser,
`aws sso` waiting on the network. A corporate kubeconfig with 20 EKS contexts and expired SSO
therefore turns `c` into a frozen terminal.

The overlay test `probeContextsWith(16 names, 300ms probe)` returned after 300 ms instead of
immediately. The existing `TestProbeContextsBoundsConcurrency` never times the *call* itself.

Fix: run the `for … g.Go` loop inside the goroutine that already exists. Wrap each probe in a
`select` on `time.After(probeTimeout)`, so a hung plugin yields "✕ timeout" and frees the UI.
The goroutine stays leaked, but it is bounded by the plugin's own lifetime.

### H2 — an unreachable cluster at launch hides behind connect grace

`inConnectGrace(synced, err, now)` returns true for any `isTimeout(err)` while
`now - startedAt < 90s` and some started kind is unsynced. On a cold launch against a blackholed
IP:

- The ping fails with `context.DeadlineExceeded` (10 s `pingTimeout`), and grace swallows it
  (`health.go:434`).
- The reflector LISTs fail with `dial tcp …: i/o timeout`, which is a `net.Error` with
  `Timeout()` true. Grace swallows these too (`health.go:268`).
- Nothing else drives 4c. The root swaps to setup only on an `Offline()` `ConnStateMsg`
  (`model.go:652`).

`noteListBurst` runs on every lazy start (`cluster.go:592`, `dynamic.go:93`, `helm.go:640`) and
resets `startedAt`. Each kind the user opens while staring at the spinner pushes 4c out by another
90 s.

The grace exists for "an SSH port-forward that's healthy but saturated". That premise needs
evidence of reachability, which `c.reached` already latches: on `/livez` success
(`health.go:388`), or on any cache sync. Gating grace on `reached` keeps the slow-link case and
restores the design's "on timeout 4c takes over". Add a unit test:
`recordPing(timeoutErr, synced=false, reached=false)` must report `Reconnecting`.

### M1 — the plugin's own message is replaced by client-go's

The overlay test, which runs `NewClusterForContext` with a failing exec plugin and then `c.ping(…)`,
captured two emitted states:

```
state 0: unauthenticated  "Error loading SSO Token: Token has expired and refresh failed"
state 1: unauthenticated  "Get \"https://…/livez\": getting credentials: exec: executable … failed with exit code 255"
```

Both come from the same failure. The gate's first-block callback and the ping's own
`recordPing` each call `setUnauthenticated`, and `credentialErrorMessage` drains the ring on the
first call. `execauth.go:284-289` says the plugin's stderr "wins". In practice it loses whenever
the ping is the request that discovers the expiry, which is the likely case with a 2 s ping. A
reflector-first discovery has the same overwrite if a ping is in flight: the gated ping returns
`errAuthenticationGated`, which `IsAuthenticationError` accepts at `health.go:382`, so the banner
reads "kubernetes traffic paused until credentials are retried".

### M2 — one stderr ring for every context

`newClientForContext` routes every exec Authenticator's stderr into `pluginStderr`
(`client.go:219`). That includes `defaultProbe`'s throwaway clients, one per kubeconfig context,
every time the palette opens. Only the *active* cluster's credential failure ever drains the ring.
The overlay test probed a context whose plugin printed "OTHER CONTEXT: run gcloud auth login",
then fed the active health a credential failure with no stderr of its own. The banner read
"OTHER CONTEXT: run gcloud auth login".

The same leak happens:

- **Across a `SwitchContext`:** the old context's last plugin output becomes the new context's
  explanation.
- **From warnings printed on successful mints** (aws-cli deprecation notices, for example): they
  sit in the 4 KiB tail until the next failure, hours later.

### M3 — sleeping under `Cluster.mu`

The three callers of `setUnauthenticated` that hold `c.mu` are `ping` (lock taken at
`health.go:374`, deferred), `recordWatchError` (`cluster.go:824-842`), and
`recordAuthenticationFailure` (`cluster.go:864-873`). `take()` then blocks ≥100 ms on a timer.
`ListRaw`→`ensureKind` and `KindSynced` take the same lock from the Update loop, so each credential
failure, and each `r` press while unauthenticated, is a visible hitch. Under M1's double call it is
two hitches. The fix is mechanical: resolve the message string before locking.

### M4 / M5 — 4c's rebuild hooks

`attemptSwitchContext` runs entirely inside a `tea.Cmd`:

1. `ResetClusterContext`
2. `sess.Cluster.Stop()`
3. `NewClusterForContext`
4. `Start` (up to 15 s)
5. Writes `sess.Cluster/Lister/Metrics/Location/Registry/Groups`

The root keeps processing messages throughout. The 4c probe drain is delivering
`switchProbeMsg`s, the user can open `c` (setup is a `Screen`, `model.go:858`), and `View` runs
after each message. Three concrete outcomes follow.

- **Session race.** `model.go:635` reads `m.session.Cluster` on every `ConnStateMsg`, and
  `contextItems`/`switchContextCmd` read `sess.Location`/`sess.Cluster`, all while the goroutine
  writes them.
- **Double submit.** `connectToSelected` and `doRetry` set `retrying` but never check it. Two `↵`
  presses run two rebuilds, both `ReplaceRootMsg`s land, and the first cluster's informers and
  health loop run for the life of the process.
- **Dead `r`.** If step 3 fails (a broken context entry, a missing cert file), the old cluster is
  already stopped and `RetryNow` (bound at `app.go:689`) pokes a health loop that has returned.

### M6 — silent, overreaching rollback

`switchContextCmd` (`context.go:329-341`) always runs a second full `SwitchContext` back to the
old context on error. `SwitchContext` returns before mutating anything when the client build
fails (`cluster.go:1072-1075`), so in that case the rollback tears down a healthy cluster for
nothing. In both cases:

- Every lazily started informer is gone afterwards, and browse is never told to reload.
- The error lands in `SwitchContextMsg.Err`, which nothing renders: the root and browse both
  ignore it.

`TestFailedContextSwitchKeepsOriginalSession` only checks that Pods come back. Pods are eager and
emit Add events after the rollback.

### M7 / M8 — kubeconfig resolution in the recovery paths

- `kute --kubeconfig ./wrong` → 10b → `k` → type the right path → `↵`. `attemptReconnect` sets
  `$KUBECONFIG`, but `kubeconfigSource` (`client.go:60-71`) still returns the flag path, so the
  retry fails identically.
- `kute --context typo` → `ClientConfig()` returns `context "typo" does not exist` →
  `buildConfigLookupError` → 10b "no kubeconfig found", with the perfectly valid file marked
  "invalid kubeconfig" (overlay test). Inside a pod, the same error silently connects to the
  in-cluster API server and labels it `typo`.

### M9 — the resting browse outlives its context

`tui/model.go:737-743` rebuilds browse only when `len(m.stack) > 0`. Otherwise browse's
`switchContext` updates kind, namespace and filter, but `currentUser` keeps the value captured at
`app.go:672`. Run-now on a CronJob after `c ↵` stamps the previous context's user. The explicit
contract stated at `model.go:737-740` ("discard … and return to a fresh browse screen") is
only half-applied.

## Test gaps

- **`ProbeContexts` caller latency (H1).** Assert that `ProbeContexts` returns in under 10 ms for
  `probeConcurrency*2` names with a blocking probe.
- **Grace vs. never-reached (H2).** Unit test on `recordPing`/`onWatchError` with a `reached`
  flag. Add an e2e that launches against a blackholed address (e.g. `10.255.255.1`) and asserts
  4c within `pingTimeout + ε`.
- **The ping-path double emit (M1).** Assert the *final* `ConnState.Err` contains the plugin's
  stderr after `c.ping` with a failing plugin. The e2e `auth_test.go:77`
  `WaitFor(authExpiredMarker)` asserts on transient state, which CLAUDE.md's third e2e rule
  forbids. Make it `WaitFor` the marker and then `Never` the generic text.
- **Cross-context stderr isolation (M2).** Probe a failing context, then trigger an active-cluster
  failure, and assert the banner doesn't carry the probe's text.
- **No test exercises `attemptReconnect`/`attemptSwitchContext` (M4/M5/M7).** Needed: a failed
  `NewClusterForContext` keeps `r` working, a double `↵` doesn't leak, `--kubeconfig` plus an
  edited path works, and `-race` driving the root model during a rebuild.
- **`switchContextCmd` rollback (M6).** No unit test covers the rollback branch. It needs:
  - A client-build failure, which must cause no rollback.
  - A timeout followed by a rollback that reloads a lazy kind.
  - `Err` being surfaced.
- **Palette switch from the resting browse (M9).** Assert `currentUser`/registry-miss behaviour.
- **`classifyTimeZoneCapability`.** It is covered (`capability_test.go`). The probe-ignores-ctx
  hang is already in `go-practices-review.md`'s deferred list.

## Quick wins

1. Move the `g.Go` loop into the existing goroutine (H1). This is a three-line diff.
2. Compute `credentialErrorMessage` before `c.mu.Lock()` in `ping`, `recordWatchError` and
   `recordAuthenticationFailure` (M3). The same diff can keep `prev.Err` when already
   unauthenticated and the ring is empty (M1).
3. Guard `connectToSelected`/`doRetry` on `m.retrying` (M4, part).
4. In `attemptReconnect`, call `kube.SetKubeconfigPath(path)` instead of `os.Setenv` (M7, part).
5. Skip the rollback when `SwitchContext` failed before mutating anything, and render
   `SwitchContextMsg.Err` (M6, part).
6. Delete `ConnFailed`/`ConnNoCluster` (L2). Bump `setup.probeGen` on `r`, or delete it (L3).
