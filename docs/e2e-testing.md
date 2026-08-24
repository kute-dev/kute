# kute — end-to-end test suite

What `test/e2e` and its `internal/kube` counterpart actually cover, how the harness works,
and the rules that keep a suite running against a real cluster from going flaky. This is the
source of truth for the guards that exist in the repository today; read it alongside
[`lazy-informers.md`](lazy-informers.md) when changing informer lifecycle coverage.

---

## Why this exists

Every other test in the repo stops at a fake: `internal/kube`'s unit tests drive
`k8s.io/client-go/kubernetes/fake`, and every task package drives a hand-written
`fakeLister`. None of that exercises the actual join — kubeconfig resolution → REST config
→ shared informer factory → lazy `ensureKind` → `resources.List` → rendered frame — against
a real apiserver. This suite does, on a real kind cluster, driving the production startup
path through a real `tea.Program`.

---

## Three layers, each testing something the layer below cannot

1. **Cluster provisioning** (shell). kind is the primary substrate: it has a real kubelet,
   so pod logs, exec, and port-forward are genuinely exercisable, and it runs on GitHub
   Actions runners. kwok is the opt-in substrate for the scale row, which kind cannot reach.
2. **Program harnesses** (Go, `test/e2e`). The ordinary suite runs the real `app.NewModel`
   through a real `tea.Program` with `WithInput`/`WithOutput`/`WithWindowSize`, fed real key
   bytes and asserted on captured frames. The nightly `e2e_pty` variant builds the shipping
   binary and drives it through a kernel PTY for subprocess handoff and terminal-mode checks.
3. **Wire-level invariants** (Go, `package kube`). The lazy-informer rules, asserted against
   a real apiserver by reading the unexported `kindInformers` map, exactly as
   `internal/kube/count_test.go` already does against the fake clientset.

`RunWithConfig` (`internal/app/app.go`) and the build-tagged `RunE2E`
(`internal/app/e2e.go`) both delegate to the same unexported `run`. The harness enters through
`RunE2E` with its own `tea.ProgramOption`s, so it exercises the production composition-root
lifecycle — cluster start, resource and connection event bridge, forward bridge, shutdown,
and state save — without exporting a headless-test seam in shipping builds.

### Rules that keep this from going flaky

- **No golden files.** A real cluster produces nondeterministic AGE, pod-name suffixes, IPs
  and node assignment. Goldens stay the unit-level tool (`internal/testutil/goldentest`);
  E2E asserts substrings and structural predicates.
- **No one-shot assertions.** Informer caches fill asynchronously; every check goes through
  `App.WaitFor`/`WaitForAll`/`WaitLoaded`, which poll the latest frame against a deadline. A
  bare `strings.Contains` on the first frame is a race by construction — and a one-shot check
  for a *negative* passes simply by looking too early.
- **Never assert on transient state.** Polling can't help when the value itself comes and
  goes: a crash-looping container reads `CrashLoopBackOff` only while it waits, so a frame
  captured mid-restart holds a stale word for as long as the backoff. Assert the durable
  statement of the same fact instead — which is also why `flux_test.go` and `argo_test.go`
  install Flux/Argo CRDs with **no controller running**: the hand-written status in their
  fixtures is otherwise permanent, and a real controller would reconcile a Degraded/Stalled
  object away mid-assertion.
- **Never wait on a string the screen you're leaving also shows.** Navigation is dispatched
  as a `tea.Cmd`, so the frame a key press fences on can still be the old screen, and
  `WaitFor` returns on it — aiming everything after at a screen that's being replaced. That's
  why `gotoKind` waits for the destination list's *title*, never for a row or object name
  both screens carry (`shop` is an Ingress *and* the middle of the Secrets list's
  `sh.helm.release.v1.shop.v2`).
- **A key press whose meaning depends on the previous write fences on the screen, not the
  API.** Reading the object back through a client proves the *server* has the write, not
  that kute does, and `browse` reloads from its informer cache on a 250 ms debounce.
- **No `t.Parallel`, anywhere in this suite.** `Launch` isolates `HOME`/XDG with `t.Setenv`,
  `internal/kube`'s own e2e tests set a package-global kubeconfig path, and many scenarios
  intentionally mutate the same real cluster fixtures.

---

## 1. Cluster provisioning

- **`scripts/e2e-cluster.sh`** — `up` / `down` / `fixtures` / `recreate` /
  `restricted-kubeconfig`. Writes to `$KUTE_E2E_KUBECONFIG`, defaulting to
  `<repo>/.kube/e2e.config` — deliberately *not* `.kube/config`, which `mise.toml` points
  `KUBECONFIG` at and which holds the developer's own contexts. `K8S_VERSION` (default
  `1.35`) picks between `kind/config-1.35.yaml` and `config-1.36.yaml`. After `kind create`,
  applies the fixtures, waits for rollouts, and mints token kubeconfigs for the restricted
  ServiceAccounts via `kubectl create token`.
- **`scripts/e2e-scale-cluster.sh`** — 5k pods on kwok, for the scale row kind cannot reach.
  Reuses `kwok-prod-cluster.sh`'s existing `resolve_kwokctl`/`BIN_DIR` bootstrap; seeds
  Deployments with high replica counts onto fake nodes.
- **`scripts/e2e-run.sh`** — one-shot wrapper: bring the cluster up, run the suite, tear it
  down even on failure. `KUTE_E2E_KEEP=1` leaves the cluster up for iterating on a failure;
  `KUTE_E2E_REUSE=1` skips `up` against an already-provisioned cluster and never tears down
  one it didn't create. It builds with `-tags e2e` only; `KUTE_E2E_TAGS` adds the
  nightly-only tags on top (`KUTE_E2E_TAGS=e2e_soak scripts/e2e-run.sh -run TestEventStorm`).
  Without that tag the file is not compiled at all, so the `-run` would match nothing and
  `go test` would exit 0 — the wrapper turns a "no tests to run" report into a failure
  rather than letting a suite that never ran read as green.

## 2. Fixtures — `test/e2e/fixtures/*.yaml`

Namespaced fixtures primarily live in `kute-e2e` (with `kute-e2e-b` for scoped switching).
CRDs and Nodes are cluster-scoped, while the RBAC fixture deliberately mixes Roles and
ClusterRoles. Workload images are pinned by digest and fixture application is idempotent.

| Fixture | What it makes testable |
| --- | --- |
| `00-namespace.yaml` | the shared `kute-e2e` namespace every other fixture lands in |
| `01-secondary-namespace.yaml` — `kute-e2e-b`, a Pod and ConfigMap unique to it | real A→B→A namespace switching and distinct scoped-cache keys |
| `10-workloads.yaml` — Deployments `api`, `worker` (`exit 1`) | list → detail → logs → exec; rollout-restart; CrashLoopBackOff status derivation, poddetail's termination banner, timeline restarts |
| `20-config.yaml` — ConfigMap `app-config`, Secret `app-secret` | §27a in-place `↵` edit and the `e` buffer editor; §27b masked grid, `ctrl-x`, add/remove key |
| `25-batch.yaml` — suspended Job `phase3-job`, suspended CronJob `phase3-cron` | dedicated reusable sources for Job rerun, CronJob run-now, and schedule editing without ambient scheduled work |
| `30-helm-releases.yaml` — two `helm.sh/release.v1` Secrets, revisions 1 and 2 | §18a history rail reads directly from Secrets; the rollback scenario alone uses the pinned Helm CLI |
| `40-routing.yaml` — Services `api`/`web`, Ingress `shop` | §23a routing table, live backend resolution |
| `50-crd.yaml` + `51-widgets.yaml` — CRD `widgets.kute.dev`, two Widgets | discovery → kind registry → §14d, the "CRD support is data, not code" invariant |
| `52-flux-crds.yaml` + `53-flux-objects.yaml` — Kustomization/HelmRelease/GitRepository CRDs, hand-written status, no controller | §30a/§31a, and the Flux-vs-Helm-3 `HelmRelease` name collision the substitution table exists for |
| `54-argocd-crds.yaml` + `55-argocd-objects.yaml` — Application/AppProject CRDs, hand-written status, no controller | §33a's sync×health matrix |
| `56-certmanager-crds.yaml` + `57-certmanager-objects.yaml` — Certificate/CertificateRequest/Order/Challenge/Issuer/ClusterIssuer CRDs across two API groups, hand-written status, no controller | §35a's issuance-chain walk and §35b's EXPIRES/RENEWAL/ISSUER columns; the ACME chain fails at its deepest hop, the CA chain is clean |
| `60-rbac.yaml` — ServiceAccounts `kute-restricted` (Pod list/get/watch only), `kute-partial` (connect kinds + ConfigMaps/Events, no Secrets/Deployments/Ingresses), `kute-team` (Pods/ConfigMaps/Events/pods-log, namespace-bound Roles only, no ClusterRole) | startup, per-kind, and scoped-mode 403 paths, plus real A→B access under `--namespace-scoped` |

kind ships no metrics-server, so the "CPU/MEM render `–`, never a lie or a crash" row costs
nothing extra.

## 3. Go harness — `test/e2e/`

The PR suite uses `//go:build e2e`. Expensive rows add a second tag:
`e2e && e2e_scale` for kwok, `e2e && e2e_soak` for the bounded long-session suite,
`e2e && e2e_auth` for credential expiry, and `e2e && e2e_pty` for real-terminal
subprocess handoff. The TLS fault-injection proxy is part of ordinary `e2e`, not another
tag. The PTY suite is POSIX-only; the nightly job runs it on Linux.

`harness.go` — `Launch(t, opts...) *App`. Isolates `HOME` and all XDG state/config/cache
paths under `t.TempDir()`, disables the ambient update check, pins the dark theme, then calls
the build-tagged `app.RunE2E` with pipe input, discarded renderer output,
`tea.WithWindowSize(140, 36)`, `tea.WithColorProfile(colorprofile.TrueColor)`, and
`tea.WithContext(ctx)`. Full frames are captured from the model through `tea.WithFilter`;
the renderer's cursor-relative diff stream is not parsed as if it were a screen.

Launch options: `WithKubeconfig`, `WithNamespace`, `WithScopeNamespace` (drives
`--namespace-scoped`), `WithContext`, `WithSize`, `WithProdContexts`, `WithAPIProxy`, and
`WithoutAPIProxy`. The last option is diagnostic for an ordinary launch and required when a
merged/authentication kubeconfig already points at explicitly managed proxies, avoiding an
unobservable proxy-around-proxy layer.

Every launch goes through a per-test TLS API proxy by default. `App.Proxy()` exposes controls
for fixed delays, held requests, Kubernetes `Status` faults, endpoint availability, and
closing active watches or streams. `Fence` plus `WaitForRequest` is the synchronization
contract: install the control, fence the traffic, and wait for the matching request rather
than sleeping or matching client-go log text. `History` and `Counts` expose timestamps,
cancellation, responses, and active/total counts by resource and verb. Matchers include
decoded query parameters, which is how the recovery tests distinguish client-go's streaming
list (`watch=true&sendInitialEvents=true`) from an ordinary watch. `HoldNextStatus` gates a
synthetic response itself, which lets context-switch tests prove cancellation before a
uniquely marked stale failure can return without changing the ordinary one-shot-fault then
recovery-gate behavior.

`App` methods: `Press`/`Type`/`Paste`/`Enter`/`Esc`/`Down` to drive terminal input; `Send`
and `Resize` to inject explicit Bubble Tea messages through the retained real program;
`Frame` to read the current screen (ANSI-stripped); `WaitFor`/`WaitForAll`/`WaitGone`/`WaitForWrapped`/
`WaitLoaded` to poll against a deadline; `Never` to assert a substring stays absent across a
window; `Quit` to shut down. On failure the harness dumps the last frame and any proxied
request that failed or returned a 4xx/5xx response.

`auth_test.go` adds a temporary POSIX exec credential plugin with atomically replaced mode
and response files plus an append-only invocation counter. Its proxy variant strips the
proxy's own upstream credentials and forwards the client's bearer token, so the kind
apiserver — not merely the proxy — validates the token. `pty_test.go` builds `cmd/kute`,
attaches stdin/stdout/stderr to a fresh kernel PTY, records the initial terminal state, and
retains raw output checkpoints for assertions across Bubble Tea renderer suspension and
redraw.

`harness_support.go` adds `WaitForTCPRefused`, heap/allocation/goroutine
`SnapshotRuntime` classification, and `BuildMergedKubeconfig`. `InputFence` measures one
inert event-loop turn while a burst is in flight. The merged builder rewrites cluster and
user keys per named context so two proxies derived from the same kind kubeconfig remain
distinct.

Nightly stress files use `//go:build e2e && e2e_soak`. Their dimensions are bounded and can
be reduced for a local reproduction with `KUTE_E2E_STORM_WIDGET_PATCHES`,
`KUTE_E2E_STORM_POD_PATCHES`, `KUTE_E2E_STORM_EVENTS_PER_SCREEN`,
`KUTE_E2E_SOAK_ITERATIONS`, `KUTE_E2E_SOAK_LOG_LINES_PER_BATCH`, and
`KUTE_E2E_SOAK_NAMESPACES`. The defaults exercise 300 Widget patches, 300 Pod metadata plus
status patches, 360 Event objects (each created and updated), eight full workflow loops,
24,000 streamed log lines, and 24 namespaces.

The kwok scale row has the same bounded-override contract:
`KUTE_E2E_SCALE_NAV_ITERATIONS` defaults to 8 repeated warmed navigation cycles and
`KUTE_E2E_SCALE_BURST_PODS` defaults to a 500-Pod watch burst. The nightly workflow sets
those values explicitly; smaller positive values are useful for local iteration.

### Test files

| File | Covers |
| --- | --- |
| `flow_test.go` | the everyday list → Pod detail → logs → events → timeline flow, a separate real rollout-restart with remain-on-screen behavior, and durable crash-loop evidence |
| `kinds_test.go` | ConfigMap, Secret, Ingress, discovered Widget, and synthetic Helm-release screens, plus discovery fenced across one already-open palette instance; other files cover Pods, Deployments, Nodes, Events, Jobs, CronJobs, and the remaining curated screens |
| `editors_test.go` | §27a/§27b's confirm → execute → refresh → show result → *remain on screen* contract — the thing a unit test against a fake can't check, because it's about what the screen does after the write lands |
| `exec_test.go` | §10a's exec picker against a real container (real shell-detection round-trip, stops short of handing off the tty) and port-forward carrying real traffic — the reason this suite runs on kind rather than kwok |
| `flux_test.go` | §30a/§31a against real Flux CRDs, including the HelmRelease name-collision regression and fresh resource-version/timestamp assertions with fixture restoration |
| `argo_test.go` | §33a's sync×health matrix plus fresh refresh/sync resource versions and restored shared write fields against real Argo CD CRDs |
| `rbac_test.go` | restricted/partial identities across browse, Pod detail, logs, Events, Timeline, Nodes, and refused Deployments/Secrets/Ingresses — no spinner or false-empty claim for unreadable kinds |
| `scoped_test.go` | `--namespace-scoped` end-to-end: real pod rows, cluster-scoped kinds forbidden honestly, lazy per-namespace cache fills, the namespace palette's denied notice |
| `metrics_test.go` | no metrics-server: `–` in browse, poddetail's bars, nodedetail, overview's capacity bars — no crash, no zeroes presented as real |
| `prod_test.go` | prod-context delete requires the typed name; non-prod delete stays inline `y/N` |
| `proxy_test.go` | transparent forwarding, client-auth forwarding, Kubernetes Status injection (including 409), request fences/cancellation, and collision-free merged kubeconfigs |
| `forward_lifecycle_test.go` | listener teardown on stop/quit, Service target replacement, and stop-during-retry cancellation |
| `network_test.go` | cached offline rows, disabled writes, responsive input, wire-timestamped retry pacing, and recovery without restart |
| `watch_recovery_test.go` | active WATCH close → injected 410 → gated streaming relist for typed, dynamic, and filtered Helm informers, including successful-livez-before-WATCH ordering, populated and initially empty scopes, and no false-empty frames |
| `context_switch_test.go` | merged-context restore, cancellation of a delayed log stream, YAML managedFields GET, and old WATCH, return-to-browse from pushed tasks, endpoint isolation, and failed-switch rollback |
| `churn_test.go` | external create/update/delete, selection clamping, empty settlement, UID replacement, and Pod-detail gone state |
| `log_lifecycle_test.go` | deleted-Pod terminal state, follow-request cancellation on `esc` and quit, and continued navigation plus connection-badge recovery while logs remain open; context-switch cancellation lives in `context_switch_test.go` |
| `terminal_test.go` | resize survival and real bracketed-paste routing through filters, palettes, confirmations, port input, and multiline editors |
| `mutation_delete_test.go` | committed non-prod/PROD deletes, selection clamping, and disposable-Pod force-delete escalation |
| `mutation_editors_test.go` | ConfigMap multiline/add/remove/restart mutations and Secret rewrite recovery after an injected 409 Conflict |
| `mutation_workloads_test.go` | cordon/uncordon with cleanup plus scale, set-image, resources, and metadata editors against a disposable Deployment |
| `mutation_batch_test.go` | Job rerun and CronJob run-now/schedule editing against dedicated fixtures |
| `mutation_helm_test.go` | real Helm rollback from the stored revision Secrets, new revision rendering, and fixture restoration |
| `certchain_test.go` | §35a's Certificate → CertificateRequest → Order → Challenge walk across two API groups: deepest-failure promotion on the ACME chain, the short CA chain with no banner, and both refs-strip branches (missing Secret + ClusterIssuer, existing Secret + namespaced Issuer) |
| `batch_screens_test.go` | §37b's attempt ledger (per-attempt pods joined by controller ownerRef, real exit codes, no index grid on a non-Indexed Job) and §36e's CronJob detail (facts grid, retention limits, settled with zero Jobs) |
| `inspectors_test.go` | §22a who-can resolved from cache with no authorization round-trip, and §41d's node debug panel staging its command without handing off the terminal |
| `scale_test.go` | build tag `e2e && e2e_scale`, kwok substrate: 5k-Pod connect/heap budget, warmed repeated-navigation heap/goroutine deltas, and a responsive 500-Pod burst with unrelated LIST/WATCH policing — excluded from the PR job |
| `storm_test.go` | build tag `e2e && e2e_soak`: Widget, Pod-detail, Events, and Timeline bursts converge to stable final values with bounded input, request, and goroutine growth |
| `soak_test.go` | build tag `e2e && e2e_soak`: repeated detail/log/event/timeline/YAML, ten-kind, three-palette, forward, context, and namespace workflows return near their settled runtime baseline |
| `high_rate_logs_test.go` | build tag `e2e && e2e_soak`: two 12k-line live batches overflow the bounded log buffer, advance its dropped counter, plateau heap, and remain responsive |
| `namespace_fanout_test.go` | build tag `e2e && e2e_soak`: 24 scoped namespaces enforce one retained cache/watch per namespace and zero new requests on revisit |
| `auth_test.go` | build tag `e2e && e2e_auth`: direct 401 and short-lived exec credential failure, cached rows/write gate, stopped automatic retries, and watch recovery after explicit `r` |
| `pty_test.go` | build tag `e2e && e2e_pty` (POSIX): clean/non-zero real exec exits, redraw and continued input, quit across an active handoff, and terminal-mode restoration |

### Coverage audit

The lifecycle expansion and its six post-implementation cleanup items now have direct PR or
nightly guards. The coverage audit has no remaining unguarded bullet: discovery is
request-fenced, GitOps writes require fresh resource versions and restore their fixtures,
watch health outranks a successful probe at the wire, YAML's live GET is cancelled across
context switches, logs recover while still active, and the kwok row covers post-navigation
runtime deltas plus breadth-safe event churn.

## 4. Wire invariants — `internal/kube/e2e_lazy_test.go`, `e2e_scoped_test.go`, `e2e_resilience_test.go`

`package kube`, `//go:build e2e`, living beside `count_test.go` because they need the
unexported `kindInformers` map. Against a real apiserver:

- after `Start`, exactly the three `eagerKinds` have informers;
- `CountLive` across a dozen kinds still leaves it at three;
- the first `ListRaw(KindSecret)` adds exactly one;
- the Helm release informer is per-namespace and carries the `type=helm.sh/release.v1` field
  selector;
- under the restricted SA, a forbidden kind has non-nil `KindForbidden`, nil `KindError`,
  and `KindSynced` true. The real-server screen tests consume the explicit forbidden signal,
  while `KindError` remains the retryable/stalled-cache reason — settled-but-denied or
  settled-but-errored is never rendered as a hang or a false empty.
- typed, dynamic, and filtered Helm informer identities remain singular across real server
  events; proxy-level tests pin close → 410 → streaming-relist ordering and one final active
  WATCH for each shape.

`e2e_scoped_test.go` reads `kindInformers` keyed by the full `scopeKey{kind, namespace}`
rather than merely by kind, verifying `--namespace-scoped` starts one cache per namespace
actually read (`lazy-informers.md` §5.6).

## 5. CI

- **`.github/workflows/ci.yml`** — `e2e` job: mise-action, `scripts/e2e-cluster.sh up`, then
  `go test -tags e2e -count=1 -timeout 15m ./test/e2e/... ./internal/kube/...`. Uploads
  cluster state (`kubectl get all`/`describe pods`/`get events`) on failure. Runs kind 1.35
  on every PR. Because every unqualified lifecycle and mutation file uses the ordinary
  `e2e` tag, all of those tests — including all six watch-recovery shapes — run here; only
  scale, soak, authentication-expiry, and PTY files are excluded by their second tags.
- **`.github/workflows/e2e-nightly.yml`** — both supported kind versions on a schedule, plus
  the `e2e_soak` lifecycle job, `e2e_auth`/`e2e_pty` process-and-terminal job, and
  `e2e_scale` kwok run. The soak job runs only the four bounded long-session scenarios and
  uploads the same cluster diagnostics as the ordinary kind legs.

---

## Verification

Local, in order:

```sh
scripts/e2e-cluster.sh up                        # kind + fixtures, ~90s cold
go test -tags e2e -count=1 -timeout 15m ./test/e2e/... ./internal/kube/...
go test -tags "e2e e2e_auth e2e_pty" -count=1 -timeout 15m -run 'Test(DirectUnauthorizedPausesHealthChecks|ExecCredentialExpiryRecoversWithoutRestart|PTYExecSubprocessHandoff)$' ./test/e2e/...
go test -tags "e2e e2e_soak" -count=1 -timeout 25m -run 'Test(EventStorm|RepeatedWorkflowSoak|HighRateLogs|NamespaceFanOut)' ./test/e2e/...
scripts/e2e-cluster.sh down
K8S_VERSION=1.36 scripts/e2e-run.sh              # provisions, tests, and removes 1.36
scripts/e2e-scale-cluster.sh up
go test -tags "e2e e2e_scale" -count=1 -timeout 30m -run TestScale ./test/e2e/...
scripts/e2e-scale-cluster.sh down
```

Or, for iterating on a single failure: `scripts/e2e-run.sh -run TestFluxScreens`.

Then confirm the suite has not leaked into the normal path:

```sh
go test -race -shuffle=on -count=1 ./...   # must pass with no cluster reachable
go vet ./... && golangci-lint run
```

### Keeping it honest

Two checks that the suite is real rather than merely green, worth repeating whenever the
lazy-informer rules change:

- Temporarily make `Cluster.Start` register every kind and confirm `e2e_lazy_test.go` fails.
  A wire-level test that passes both ways proves nothing — the lesson `newTypedFactory` in
  `internal/kube/lazy_test.go` already encodes.
- Point the harness at the restricted kubeconfig with the 403 handling stubbed out and
  confirm `rbac_test.go` fails on the false-empty assertion, not just the card.
