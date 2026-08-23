# kute — end-to-end test suite

What `test/e2e` and its `internal/kube` counterpart actually cover, how the harness works,
and the rules that keep a suite running against a real cluster from going flaky. Companion
to [`lazy-informers.md`](lazy-informers.md), whose rules this suite verifies against a real
apiserver instead of a fake clientset.

---

## Why this exists

Every other test in the repo stops at a fake: `internal/kube`'s unit tests drive
`k8s.io/client-go/kubernetes/fake`, and every task package drives a hand-written
`fakeLister`. None of that exercises the actual join — kubeconfig resolution → REST config
→ shared informer factory → lazy `ensureKind` → `resources.List` → rendered frame — against
a real apiserver. This suite does, on a real kind cluster, driving the real `app.run`
through a real `tea.Program`.

---

## Three layers, each testing something the layer below cannot

1. **Cluster provisioning** (shell). kind is the primary substrate: it has a real kubelet,
   so pod logs, exec, and port-forward are genuinely exercisable, and it runs on GitHub
   Actions runners. kwok is the opt-in substrate for the scale row, which kind cannot reach.
2. **A headless program harness** (Go, `test/e2e`). The real `app.NewModel` run through a
   real `tea.Program` with `WithInput`/`WithOutput`/`WithWindowSize`, fed real key bytes and
   asserted on captured frames. No PTY.
3. **Wire-level invariants** (Go, `package kube`). The lazy-informer rules, asserted against
   a real apiserver by reading the unexported `kindInformers` map, exactly as
   `internal/kube/count_test.go` already does against the fake clientset.

`RunWithConfig` (`internal/app/app.go`) is a thin wrapper around `run(cfg Config, opts
...tea.ProgramOption)` for exactly this reason — the harness calls `run` with its own
`tea.ProgramOption`s so it exercises the *real* startup path (the four goroutines wiring
`forwardEvents`, `cluster.Start`, `watchForwardManager`, `updateCheckCmd`), not a copy that
can silently drift from it.

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
- **No `t.Parallel`, anywhere in this suite.** `lipgloss.SetColorProfile` is a process
  global (as with `browse`'s truecolor goldens), `Launch` isolates `HOME`/XDG with
  `t.Setenv`, and `internal/kube`'s own e2e tests set the kubeconfig path — both process-wide.

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
  one it didn't create.

## 2. Fixtures — `test/e2e/fixtures/*.yaml`

Everything lives in namespace `kute-e2e`, pinned by image digest so a re-run is not a new
cluster.

| Fixture | What it makes testable |
| --- | --- |
| `00-namespace.yaml` | the shared `kute-e2e` namespace every other fixture lands in |
| `01-secondary-namespace.yaml` — `kute-e2e-b`, a Pod and ConfigMap unique to it | real A→B→A namespace switching and distinct scoped-cache keys |
| `10-workloads.yaml` — Deployments `api`, `worker` (`exit 1`) | list → detail → logs → exec; rollout-restart; CrashLoopBackOff status derivation, poddetail's termination banner, timeline restarts |
| `20-config.yaml` — ConfigMap `app-config`, Secret `app-secret` | §27a in-place `↵` edit and the `e` buffer editor; §27b masked grid, `ctrl-x`, add/remove key |
| `30-helm-releases.yaml` — two `helm.sh/release.v1` Secrets, revisions 1 and 2 | §18a history rail and rollback, with no helm binary anywhere |
| `40-routing.yaml` — Services `api`/`web`, Ingress `shop` | §23a routing table, live backend resolution |
| `50-crd.yaml` + `51-widgets.yaml` — CRD `widgets.kute.dev`, two Widgets | discovery → kind registry → §14d, the "CRD support is data, not code" invariant |
| `52-flux-crds.yaml` + `53-flux-objects.yaml` — Kustomization/HelmRelease/GitRepository CRDs, hand-written status, no controller | §30a/§31a, and the Flux-vs-Helm-3 `HelmRelease` name collision the substitution table exists for |
| `54-argocd-crds.yaml` + `55-argocd-objects.yaml` — Application/AppProject CRDs, hand-written status, no controller | §33a's sync×health matrix |
| `60-rbac.yaml` — ServiceAccounts `kute-restricted` (pods list/get only), `kute-partial` (connect kinds + ConfigMaps/Events, no Secrets/Deployments/Ingresses), `kute-team` (Pods/ConfigMaps/Events/pods-log, namespace-bound Role only, no ClusterRole) | every 403 path, including per-kind degradation and `--namespace-scoped` coverage |

kind ships no metrics-server, so the "CPU/MEM render `–`, never a lie or a crash" row costs
nothing extra.

## 3. Go harness — `test/e2e/`, all files `//go:build e2e`

`harness.go` — `Launch(t, opts...) *App`. Points `XDG_STATE_HOME`/`XDG_CONFIG_HOME` at
`t.TempDir()`, then calls `app.run` with `tea.WithInput`, `tea.WithOutput`,
`tea.WithWindowSize(140, 36)`, `tea.WithColorProfile(colorprofile.TrueColor)`,
`tea.WithContext(ctx)`.

Launch options: `WithKubeconfig`, `WithNamespace`, `WithScopeNamespace` (drives
`--namespace-scoped`), `WithContext`, `WithSize`, `WithProdContexts`, `WithAPIProxy`.

Every launch goes through a per-test TLS API proxy by default. `App.Proxy()` exposes controls
for fixed delays, held requests, Kubernetes `Status` faults, endpoint availability, and
closing active watches or streams. `Fence` plus `WaitForRequest` is the synchronization
contract: install the control, fence the traffic, and wait for the matching request rather
than sleeping or matching client-go log text. `History` and `Counts` expose timestamps,
cancellation, responses, and active/total counts by resource and verb. `WithoutAPIProxy`
exists only for diagnosing proxy transparency.

`App` methods: `Press`/`Type`/`Paste`/`Enter`/`Esc`/`Down` to drive terminal input; `Send`
and `Resize` to inject explicit Bubble Tea messages through the retained real program;
`Frame` to read the current screen (ANSI-stripped); `WaitFor`/`WaitForAll`/`WaitGone`/`WaitForWrapped`/
`WaitLoaded` to poll against a deadline; `Never` to assert a substring stays absent across a
window; `Quit` to shut down. On failure the harness dumps the last frame.

`harness_support.go` adds `WaitForTCPRefused`, heap/goroutine `SnapshotRuntime`
classification, and `BuildMergedKubeconfig`. The merged builder rewrites cluster and user
keys per named context so two proxies derived from the same kind kubeconfig remain distinct.

### Test files

| File | Covers |
| --- | --- |
| `flow_test.go` | the everyday flow (list → pod detail → logs → events → timeline → rollout-restart), that the screen is *still the screen* after a commit, and CrashLoopBackOff surfacing |
| `kinds_test.go` | one subtest per resource kind's screen, opened from `browse` with fixture data reaching the frame; the jump palette gaining kinds while open |
| `editors_test.go` | §27a/§27b's confirm → execute → refresh → show result → *remain on screen* contract — the thing a unit test against a fake can't check, because it's about what the screen does after the write lands |
| `exec_test.go` | §10a's exec picker against a real container (real shell-detection round-trip, stops short of handing off the tty) and port-forward carrying real traffic — the reason this suite runs on kind rather than kwok |
| `flux_test.go` | §30a/§31a against real Flux CRDs, including the HelmRelease name-collision regression |
| `argo_test.go` | §33a's sync×health matrix against real Argo CD CRDs |
| `rbac_test.go` | the restricted/partial kubeconfigs across every screen — the 403 card, and that no screen spins forever or claims zero of a kind it merely cannot read |
| `scoped_test.go` | `--namespace-scoped` end-to-end: real pod rows, cluster-scoped kinds forbidden honestly, lazy per-namespace cache fills, the namespace palette's denied notice |
| `metrics_test.go` | no metrics-server: `–` in browse, poddetail's bars, nodedetail, overview's capacity bars — no crash, no zeroes presented as real |
| `prod_test.go` | prod-context delete requires the typed name; non-prod delete stays inline `y/N` |
| `forward_lifecycle_test.go` | listener teardown on stop/quit, Service target replacement, and stop-during-retry cancellation |
| `network_test.go` | cached offline rows, disabled writes, responsive input, wire-timestamped retry pacing, and recovery without restart |
| `watch_recovery_test.go` | WATCH close → 410 → LIST → WATCH recovery for typed, dynamic, and filtered Helm informers without false-empty frames |
| `context_switch_test.go` | merged-context restore, old read/stream cancellation, pushed-task return-to-browse, endpoint isolation, and failed-switch rollback |
| `churn_test.go` | external create/update/delete, selection clamping, empty settlement, UID replacement, and Pod-detail gone state |
| `log_lifecycle_test.go` | deleted-Pod terminal state plus follow-request cancellation on esc, context switch, quit, and disconnect navigation |
| `terminal_test.go` | resize survival and real bracketed-paste routing through filters, palettes, confirmations, port input, and multiline editors |
| `scale_test.go` | build tag `e2e && e2e_scale`, kwok substrate: connect-time budget, first-frame render, informer heap via `runtime.ReadMemStats` — excluded from the PR job |

## 4. Wire invariants — `internal/kube/e2e_lazy_test.go`, `e2e_scoped_test.go`, `e2e_resilience_test.go`

`package kube`, `//go:build e2e`, living beside `count_test.go` because they need the
unexported `kindInformers` map. Against a real apiserver:

- after `Start`, exactly the three `eagerKinds` have informers;
- `CountLive` across a dozen kinds still leaves it at three;
- the first `ListRaw(KindSecret)` adds exactly one;
- the Helm release informer is per-namespace and carries the `type=helm.sh/release.v1` field
  selector;
- under the restricted SA, a forbidden kind has non-nil `KindError` *and* `KindSynced` true —
  settled-but-errored, never a hang or a false empty.
- typed, dynamic, and filtered Helm informer identities remain singular across
  real server churn; proxy-level tests pin the corresponding 410 relist order.

`e2e_scoped_test.go` reads `kindInformers` keyed by the full `scopeKey{kind, namespace}`
rather than merely by kind, verifying `--namespace-scoped` starts one cache per namespace
actually read (`lazy-informers.md` §5.6).

## 5. CI

- **`.github/workflows/ci.yml`** — `e2e` job: mise-action, `scripts/e2e-cluster.sh up`, then
  `go test -tags e2e -count=1 -timeout 15m ./test/e2e/... ./internal/kube/...`. Uploads
  cluster state (`kubectl get all`/`describe pods`/`get events`) on failure. Runs kind 1.35
  on every PR.
- **`.github/workflows/e2e-nightly.yml`** — the 1.36 leg on a schedule, plus the `e2e_scale`
  kwok run (`-tags "e2e e2e_scale" -run TestScale`), keeping PR latency to the single 1.35
  job.

---

## Verification

Local, in order:

```sh
scripts/e2e-cluster.sh up                        # kind + fixtures, ~90s cold
go test -tags e2e -count=1 -timeout 15m ./test/e2e/... ./internal/kube/...
K8S_VERSION=1.36 scripts/e2e-cluster.sh recreate && go test -tags e2e ./test/e2e/...
scripts/e2e-scale-cluster.sh up && go test -tags "e2e e2e_scale" -run TestScale ./test/e2e/...
scripts/e2e-cluster.sh down
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

- Revert `54d5d1e`'s laziness locally (make `Start` register every kind) and confirm
  `e2e_lazy_test.go` fails. A wire-level test that passes both ways proves nothing — the
  lesson `newTypedFactory` in `internal/kube/lazy_test.go` already encodes.
- Point the harness at the restricted kubeconfig with the 403 handling stubbed out and
  confirm `rbac_test.go` fails on the false-empty assertion, not just the card.
