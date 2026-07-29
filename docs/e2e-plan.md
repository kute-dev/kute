# kute — end-to-end test plan

How kute gets an end-to-end suite: what it runs against, what it asserts, and why each
piece exists. Companion to [`beta-plan.md`](beta-plan.md), whose §2 validation matrix this
automates.

---

## Why

Every one of the repo's 144 test files stops at a fake. `internal/kube` tests drive
`k8s.io/client-go/kubernetes/fake`; every task package drives a hand-written `fakeLister`.
Nothing has ever run real client-go against a real apiserver, which means the whole join —
kubeconfig resolution → REST config → shared informer factory → lazy `ensureKind` →
`resources.List` → rendered frame — is untested, and beta gate 3 ("validated beyond one
cluster") is a manual checklist nobody has walked.

The groundwork is already in the tree and unused: `kind/config-1.35.yaml` and
`config-1.36.yaml` (pinned node digests, control-plane + 2 workers, commit `631ae11`,
referenced by nothing), `scripts/kwok-prod-cluster.sh` (a 9-node prod-sim with CRDs, helm
releases and metrics), and `kind 0.32.0` pinned in `mise.toml`.

---

## Approach

Three layers, each testing something the layer below cannot.

1. **Cluster provisioning** (shell). kind is the primary substrate: it has a real kubelet,
   so pod logs (§5b), exec (§10a) and port-forward (§13a) are genuinely exercisable, and it
   runs on GitHub Actions runners. kwok stays the opt-in substrate for the scale row, which
   kind cannot reach.
2. **A headless program harness** (Go). The real `app.NewModel` run through a real
   `tea.Program` with `WithInput`/`WithOutput`/`WithWindowSize`, fed real key bytes and
   asserted on captured frames. No PTY.
3. **Wire-level invariants** (Go, in `package kube`). The lazy-informer rules, asserted
   against a real apiserver by reading the unexported `kindInformers` map, exactly as
   `internal/kube/count_test.go:51` already does against the fake clientset.

### Two rules that keep this from becoming a flaky suite

- **No golden files in E2E.** A real cluster produces nondeterministic AGE, pod-name
  suffixes, IPs and node assignment. Goldens stay the unit-level tool
  (`internal/testutil/goldentest`); E2E asserts substrings and structural predicates.
- **No one-shot assertions.** Informer caches fill asynchronously; every check goes through
  `App.WaitFor(substr, timeout)`, which polls the latest frame against a deadline. A bare
  `strings.Contains` on the first frame is a race by construction.

`lipgloss.SetColorProfile` is a process global, so as with `browse`'s truecolor goldens, no
E2E test that renders may call `t.Parallel`.

---

## Prerequisite refactor (ships alone, first)

`RunWithConfig` (`internal/app/app.go:1173`) builds `tea.NewProgram(model)` with no options
and then wires four goroutines around it — `forwardEvents`, `cluster.Start` +
`CRDsDiscoveredMsg`, `watchForwardManager`, `updateCheckCmd`. The harness must run *that*
code, not a copy that silently drifts from it.

Extract the body into `func run(cfg Config, opts ...tea.ProgramOption) error`, leaving
`RunWithConfig(cfg) { return run(cfg) }`. No behaviour change, no new exported surface.

---

## 1. Cluster provisioning

- **`scripts/e2e-cluster.sh`** — `up` / `down` / `fixtures` / `restricted-kubeconfig`.
  Writes to `$KUTE_E2E_KUBECONFIG`, defaulting to `<repo>/.kube/e2e.config` — deliberately
  *not* `.kube/config`, which `mise.toml` points `KUBECONFIG` at and which holds the
  developer's own `kind-kute-1.35` and `kwok-prod-sim` contexts. `K8S_VERSION` (default
  `1.35`) picks between the two existing `kind/config-*.yaml` files. After `kind create`,
  applies the fixtures, waits for rollouts, and mints a token kubeconfig for the restricted
  ServiceAccount via `kubectl create token`.
- **`scripts/e2e-scale-cluster.sh`** — 5k pods on kwok. Reuses `kwok-prod-cluster.sh`'s
  existing `resolve_kwokctl`/`BIN_DIR` bootstrap rather than reimplementing it; seeds
  Deployments with high replica counts onto fake nodes, which is the whole reason this row
  is kwok and not kind.

## 2. Fixtures — `test/e2e/fixtures/*.yaml`

Namespace `kute-e2e`, everything pinned by image digest so a re-run is not a new cluster.

| Fixture | What it makes testable |
| --- | --- |
| Deployment `api` (busybox, writes to stdout in a loop) | list → detail → **logs** → exec; the rollout-restart verb |
| Deployment `worker` (`exit 1`) | CrashLoopBackOff status derivation, poddetail's termination banner, timeline restarts |
| ConfigMap `app-config` (one short value, one folded multi-line) | §27a — in-place `↵` edit *and* the `e` buffer editor |
| Secret `app-secret` | §27b — masked grid, `ctrl-x`, add/remove key |
| Two `helm.sh/release.v1` Secrets (hand-built, gzip+base64 release JSON, revisions 1 and 2) | §18a history rail and rollback, with no helm binary anywhere |
| Ingress `shop` (2 hosts, 3 paths) | §23a routing table, live backend resolution |
| CRD `widgets.kute.dev` + 2 Widgets (printer columns + a Ready condition) | discovery → kind registry → §14d, the "CRD support is data, not code" invariant |
| ServiceAccount `kute-restricted` + Role (pods list/get only) | every 403 path |

kind ships no metrics-server, so the "CPU/MEM render `–`, never a lie or a crash" row costs
nothing extra.

## 3. Go harness — `test/e2e/`, all files `//go:build e2e`

- **`harness.go`** — `Launch(t, opts...) *App`. Points `XDG_STATE_HOME` and
  `XDG_CONFIG_HOME` at `t.TempDir()` (the same isolation `scripts/record-demo.sh` already
  applies for recordings, and the only way `internal/state` and `internal/config` stay off
  the developer's real files), then calls the newly-parameterised `app.run` with
  `tea.WithInput(pipe)`, `tea.WithOutput(&syncBuf)`, `tea.WithWindowSize(120, 36)`,
  `tea.WithColorProfile(colorprofile.TrueColor)`, `tea.WithContext(ctx)`.
  API: `Press(key)`, `Type(s)`, `Enter()`, `Esc()`, `Frame()`, `WaitFor(substr, timeout)`,
  `WaitGone(substr, timeout)`, `Quit()`. `Frame()` strips ANSI via
  `internal/testutil/goldentest`'s `Plain`. On failure the harness dumps the last frame —
  which is exactly the artifact beta-plan §4's diagnostics work also needs.
- **`flow_test.go`** — the §2 acceptance flow as one walk: list → pod detail → logs →
  events → timeline → a mutating verb (rollout-restart on `api`, inline `y/N` since the
  context is not prod), asserting the result line and that the screen is *still the screen*
  after the commit — the "confirm → execute → refresh → show result → remain" contract.
- **`kinds_test.go`** — one subtest per fixture group (ConfigMap, Secret, Ingress, CRD,
  Helm), each opening the screen from `browse` and asserting fixture data reaches the frame.
  The CRD subtest is the load-bearing one: it proves discovery → printer columns → detail
  with no per-CRD code.
- **`rbac_test.go`** — relaunches against the restricted kubeconfig and visits every screen.
  Asserts the 4b permission card, and — the regression this exists for — that no screen
  spins forever and none claims the cluster has zero of a kind it merely cannot read.
- **`metrics_test.go`** — no metrics-server: `–` in browse's CPU/MEM columns, poddetail's
  bars, nodedetail, and overview's capacity bars; no crash, no zeroes presented as real.
- **`prod_test.go`** — writes `prodContexts: [kind-kute-1.35]` into the temp
  `XDG_CONFIG_HOME` (`internal/config/config.go:19`), then asserts `ctrl-d` yields
  `components.TypeNameModal` rather than the inline card, and that a wrong name does not
  delete.
- **`scale_test.go`** — build tag `e2e_scale`, kwok substrate. Connect-time budget, first
  frame renders, informer heap measured with `runtime.ReadMemStats`. Excluded from the PR
  job.

## 4. Wire invariants — `internal/kube/e2e_lazy_test.go` (`package kube`, `//go:build e2e`)

These need the unexported `kindInformers`, so they live beside `count_test.go` rather than
in `test/e2e`. Against a real apiserver:

- after `Start`, exactly the three `eagerKinds` (`cluster.go:178`) have informers;
- `CountLive` across a dozen kinds still leaves it at three;
- the first `ListRaw(KindSecret)` adds exactly one;
- the Helm release informer is per-namespace and carries the `type=helm.sh/release.v1`
  field selector (the 8.19 MB → 4 MB result recorded in
  [`lazy-informers.md`](lazy-informers.md) §5.5);
- under the restricted SA, a forbidden kind has non-nil `KindError` *and* `KindSynced` true
  — settled-but-errored, the exact pairing CLAUDE.md says must never degrade into a hang or
  a false empty.

## 5. CI

- **`.github/workflows/ci.yml`** — new `e2e` job: mise-action (which already yields the
  pinned kind and go), `scripts/e2e-cluster.sh up`, then
  `go test -tags e2e -count=1 -timeout 15m ./test/e2e/... ./internal/kube/...`. Uploads the
  captured frames on failure. PR runs 1.35 only.
- **`.github/workflows/e2e-nightly.yml`** (new) — the 1.36 leg and the `e2e_scale` kwok run
  on a schedule, keeping PR latency to the single 1.35 job.

## 6. Supporting edits

- `.gitignore` — `.kube/e2e.config`.
- `CLAUDE.md` — a Testing paragraph: what E2E covers, why it is tagged out of
  `go test ./...`, and the two anti-flake rules above.
- `beta-plan.md` — §2's table gains a "covered by" column; the rows this automates stop
  being manual.

---

## Sequencing

1. `refactor(app): let the program be started with caller-supplied tea options` — alone.
2. `test(e2e): launch kute against a real kind cluster and walk the everyday flow` —
   script, fixtures, harness, `flow_test.go`.
3. `test(e2e): cover each resource kind's screen against real cluster fixtures`.
4. `test(e2e): prove the lazy-informer rules hold against a real apiserver`.
5. `test(e2e): cover restricted-RBAC, no-metrics-server and prod-context paths`.
6. `ci(e2e): run the end-to-end suite against kind on every pull request`.
7. `test(e2e): measure connect time and informer memory on a 5k-pod cluster`, plus the
   nightly workflow.

Steps 2–5 each land a working slice; the suite is useful from step 2 on.

---

## Verification

Local, in order:

```sh
scripts/e2e-cluster.sh up                        # kind + fixtures, ~90s cold
go test -tags e2e -count=1 -timeout 15m ./test/e2e/... ./internal/kube/...
K8S_VERSION=1.36 scripts/e2e-cluster.sh recreate && go test -tags e2e ./test/e2e/...
scripts/e2e-scale-cluster.sh up && go test -tags e2e,e2e_scale ./test/e2e/...
scripts/e2e-cluster.sh down
```

Then confirm the suite has not leaked into the normal path:

```sh
go test -race -shuffle=on -count=1 ./...   # must pass with no cluster reachable
go vet ./... && golangci-lint run
```

Two checks that the suite is real rather than merely green:

- Revert `54d5d1e`'s laziness locally (make `Start` register every kind) and confirm
  `e2e_lazy_test.go` fails. A wire-level test that passes both ways proves nothing — the
  lesson `newTypedFactory` in `internal/kube/lazy_test.go` already encodes.
- Point the harness at the restricted kubeconfig with the 403 handling stubbed out and
  confirm `rbac_test.go` fails on the false-empty assertion, not just the card.
