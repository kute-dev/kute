# Go practices review

A modern-Go checklist (Go 1.26 stdlib, concurrency, testing, tooling) applied to the whole tree,
with the evidence for each verdict. Two purposes: record the four real defects it found, and
record what it found *clean*, so the next pass doesn't re-litigate settled ground.

Scope at the time of review: ~77k lines of non-test Go, ~59k lines of test Go, `go 1.26.0`.

## Outcome

Everything below was fixed, in sixteen commits. Four findings turned out worse than the audit
predicted and one turned out better; both are noted inline. The measured results:

| | Before | After |
|---|---|---|
| `resources.List`, 5000-object namespace (per watch-driven reload) | 9.9 ms, 7.2 MB, 170,611 allocs | 3.9 ms, 3.7 MB, 15,004 allocs |
| `resources.List`, 100 objects | 109 µs, 2,007 allocs | 47 µs, 303 allocs |
| Benchmarks in the repo | 0 | 3 |
| Fuzz targets | 1 | 5 (plus 5 checked-in crashers) |
| `sort` package call sites | 41 | 0 |
| `testing/synctest` uses | 0 | 8 |
| `govulncheck` on PRs | no | yes |

The layout fuzz target found three previously-unknown alignment bugs on its first run — see
[Raw terminal bytes](#raw-terminal-bytes-in-a-table-cell) below.

## Verdict

The checklist is mostly already satisfied. Error handling in particular is stronger than typical —
`%w` wrapping is essentially total and the sentinel/typed-error vocabulary is real rather than
aspirational. What the audit found instead was concentrated in four places: two data races on one
field, a goroutine that outlives its caller, a port-forward restart that can orphan a tunnel, and
a concurrency limiter that limits the wrong thing.

Separately, three structural gaps: no benchmarks anywhere, no `testing/synctest`, and
`govulncheck` not gating pull requests.

## Already clean

Recorded so a future sweep doesn't churn them.

**Error handling.** Exactly one `fmt.Errorf(..., err)` in the tree lacks `%w`, and it sits inside a
comment quoting client-go's source. Four sentinels (`resources.ErrTimeZoneUnset`,
`kube.ErrDemoUnavailable`, `kube.ErrManualJobNameConflict`, `app.ErrCrashed`), each matched with
`errors.Is` at the consumer; one typed `*kube.ConfigLookupError` with `Unwrap`; `errors.Join`
already applied at all three accumulate-into-`[]error` loops (`kube/mutate.go:693`,
`browse/bulk.go:205`, `actions/controller.go:558`). No new `errors.Join` opportunities exist.

The three `strings.Contains(err.Error(), …)` classifiers (`kube/execauth.go:236`, `kube/logs.go:92`,
`:109`) are each documented at the site as unavoidable — client-go's exec round-tripper and the
kubelet both return errors with nothing typed to match on. They stay.

**Language-level hygiene.** Zero `x := x` loop-variable shadows (verified by two independent
patterns over all 511 non-vendor files). `math/rand` is never imported; the only `crypto/rand` uses
are `rand.Reader` for x509 test fixtures. No `context.Context` stored in any struct field. No
`time.Tick`. The one `time.NewTicker` is `defer Stop()`ed.

**Locking.** No lock is held across `cache.WaitForCacheSync`. The `c.mu` → `health.mu` nesting is
consistently one-directional with no inversion — `ping()` takes and releases `c.mu` before
`recordPing` takes `h.mu`, never nested.

**Interface assertions.** The two `internal/app` lister decorators carry their full compile-time
assertion blocks (`app.go:440-457`): seven seams for `forwardAwareLister`, eight for
`helmAwareLister`. Every forward implemented on those types has a matching assertion — no drift.
This is CLAUDE.md's load-bearing rule and it is being followed.

**Paths.** `--log-file`, the crash-report directory (filenames are timestamp-derived, not
user-derived), and kubeconfig paths are not traversal risks — each is the invoking user's own path
in a single-user CLI, with no confinement boundary to escape. No path anywhere is derived from
cluster data.

**`ensureKind`'s lazy-start guard** (`kube/cluster.go:459-479`) uses a mutex plus a map of started
informers, not `sync.Once`. That is correct and `sync.Once` would be a regression: the key set is
dynamic (kind × namespace), `SwitchContext` must be able to *reset* laziness by nilling the map,
and registration-then-`Start` must be atomic because `SetWatchErrorHandler` fails on an
already-running informer. The doc comment at `:453-458` already says so.

**Dedupe loops.** Seven of them, all order-preserving with an early break at N items.
`slices.Compact` requires sorted input and would change semantics. No action.

## Defects found

### 1. `c.stopCh` read without the lock — `internal/kube/cluster.go:380`, `:394`

**Worse than predicted.** The reproduction test did not merely trip the race detector: losing the
race **panics the process** with a nil-pointer dereference in `startHealthLoop` → `ping`
(`health.go:281`), because `Stop()` leaves `stopCh` nil and the health loop selects on it. This is
a crash on disconnect-during-connect, not a leak.


Both reads happen after `c.mu.Unlock()` at `:379`, while every writer of the field holds `c.mu`
(`SwitchContext` at `:838-841`, `Stop` at `:879-882`). A `SwitchContext` or `Stop` concurrent with
`Start` is a genuine race-detector violation.

The consequence is worse than a torn read. `Stop()` sets `stopCh` to **`nil`** as its
"this cluster is dead" sentinel. If `Stop` wins the race, `cache.WaitForCacheSync(nil, …)` never
returns and `startHealthLoop(nil)` becomes an immortal 2-second ticker pinging a dead cluster for
the life of the process.

### 2. `Start`'s cache-sync goroutine outlives its caller — `internal/kube/cluster.go:391-401`

On the `case <-ctx.Done()` branch, the goroutine spawned at `:393` is still blocked inside
`WaitForCacheSync` and keeps running until `stopCh` closes, still writing the captured `synced`
after `Start` has returned it.

This is not an edge case: it is the normal path whenever `SwitchContext`'s `switchContextTimeout`
(`internal/tui/context.go:306`) or `attemptReconnect`'s `reconnectStartTimeout`
(`internal/app/app.go:798`) expires against a slow cluster.

### 3. `ForwardManager.Restart` can orphan a tunnel — `internal/kube/forward.go:414-426`, `:523-543`

`run` checks `ctx.Err()` at `:397` and again at `:431`, but not between `dialer.Dial` (`:414`) and
the `e.tunnel = tunnel` write (`:417-419`). An old goroutine that was mid-`Dial` when `Restart`
fired will, on success, overwrite the *new* goroutine's tunnel handle with its stale one and set
`State = ForwardActive`. The new tunnel is then unreachable and never `Close()`d: a leaked SPDY
connection and a leaked local listening port for every `Restart` that lands in that window.

Adjacent: `Restart` holds `m.mu` across `entry.tunnel.Close()` (`:533`), which takes
`spdyTunnel.mu`. `Stop` and `StopAll` both release `m.mu` before calling `cancel()`/`Close()`;
`Restart` is the only one that does not.

### 4. A concurrency limit that does not limit concurrency — `internal/tui/counts.go:147-173`

The goto palette's count fan-out does `wg.Add(1)` and then acquires its semaphore *inside* the
goroutine:

```go
wg.Add(1)
go func(kind kube.ResourceKind, scope string) {
    defer wg.Done()
    sem <- struct{}{}          // ← acquired here, not before `go`
    defer func() { <-sem }()
    ...
}(kind, scope)
```

So `countFetchConcurrency` bounds in-flight *requests* but not goroutines. On a cluster with 300
discovered CRDs, opening the palette spawns 300 goroutines at once. The semaphore send is also not
context-aware, so goroutines queued behind it outlive `countFetchTimeout` rather than bailing.

`internal/kube/probe.go:42-54` has the same shape with no bound at all — one goroutine, one
`NewClientForContext` and one TLS handshake per kubeconfig context, so a 50-context corporate
kubeconfig opens 50 concurrent handshakes the instant the context palette is opened.

### 5. Non-deterministic majority version — `browse/nodes.go:235-240`, `overview/load.go:229-235`

Two byte-identical copies of an argmax over a `map[string]int` using `if n > bestN`. Ties resolve
in map-iteration order, so on a cluster with an even kubelet-version split the displayed majority
version can change between redraws.

### 6. A classifier run on a reconstructed error — `podlogs/model.go:395`

```go
if kube.IsPermissionError(fmt.Errorf("%s", m.lastError)) {
```

The real `error` was stringified into `m.lastError` at `update.go:78`, then rebuilt as a fresh
error purely to run a classifier over it. The typed `*apierrors.StatusError` is destroyed on the
way, so `IsPermissionError`'s good `apierrors.IsForbidden` path can never fire here — only its
documented substring fallback can. The one site of its kind; the other 21 `.Error()`
stringifications are pure display.

### 7. Low severity

- `internal/helmrepo/helmrepo.go:209` — `filepath.Join(cachePath, repo.Name+"-index.yaml")` where
  `repo.Name` comes from the user's own `repositories.yaml` with no validation beyond `!= ""`.
  `filepath.Join` cleans, it does not confine. Read-only and same-trust-domain, so the exposure is
  small, but a repo named `../../..` escapes the cache dir.
- `internal/kube/execauth.go:80-103` — `stderrCapture`'s `once`+`w` pair is `sync.OnceValue`
  written out longhand; the field exists only to smuggle the closure's result out.
- `internal/kube/execauth.go:173-195` — a `time.Sleep(5ms)` poll loop, uninterruptible, reacquiring
  the mutex each iteration. Its doc argues it never runs on the render goroutine, which holds for
  today's callers — but `CredentialPluginOutput()` is exported, and a future Update-loop caller
  would block the TUI for up to 100ms.

### Raw terminal bytes in a table cell

Not in the original audit — found by the layout fuzz target written for it, on its first run, and
then twice more as each fix exposed the next case. All three break the same invariant: `Pad` must
return **exactly** the requested width, because one mis-measured cell shifts every column to its
right for the whole row.

Cell width is not additive across a concatenation boundary, and three separate things exploit that:

1. **Invalid UTF-8.** `Pad("\xe10", 10)` returned an 11-cell line. A stray lead byte swallows the
   bytes after it, so `StringWidth(a+b) != StringWidth(a) + StringWidth(b)`.
2. **A dangling escape sequence.** `Pad("\x1b", 2)` returned a 0-cell line — the bare ESC consumes
   the padding spaces as its own parameters. A pod that prints a lone ESC to stdout produces this.
3. **A prepending combining mark.** `Pad("\u0600", 18)` returned 17 cells: U+0600 fuses with the
   first pad space and the pair renders as one cell.

`Truncate` had the mirror-image bug — cutting through a malformed escape left a fragment measuring
*wider* than the budget.

The fix normalises invalid UTF-8 at the boundary, pads by measuring rather than calculating, and
falls back to dropping the styling when a value carries an escape sequence that can never be
satisfied. Losing colour on one cell is much cheaper than losing the row's alignment. All five
crashing inputs are checked in as corpus files, so they run on every `go test`. The hardened
version survives 8.8 million executions.

## Structural gaps

### No benchmarks

Zero `func Benchmark`, zero `testing.B`, zero `-benchmem` in source, CI, or docs. For a TUI whose
allocation pressure is per-frame rendering, that was the notable absence — and it made the
checklist's own advice about `unique.Handle`/`weak`/GC tuning ("reach for it with a benchmark in
hand") impossible to follow.

**What changed.** Three benchmarks, using `b.Loop()` and `ReportAllocs`. They are not wired into
CI as a gate — a benchmark that fails a build on a noisy runner teaches people to ignore it.

One result is worth recording because it is *good* news, and load-bearing if anyone later tries to
optimise the render path: **a full browse frame costs the same at 50 rows as at 500** (≈345 µs,
820 allocs, 150 KB), because only the viewport is drawn. Render cost is flat in list size. The
cost that does scale with the namespace is projection and sorting, which is why the comparator
below was the thing worth fixing.

### No `testing/synctest`

Was not imported anywhere. The repo was not merely missing it — it was actively working around its
absence, and said so in eight separate comments:

- `whocan/whocan_test.go:304` — "Deliberately not step(): applyLoaded's own retry cmd is a real
  tea.Tick … draining it would really sleep 250ms per bounce until rbac.synced flips."
- `browse/auxkinds_test.go:184` — "draining that chain recursively would really sleep forever."
- `cronjobschedule/cronjobschedule_test.go:19`, `cronjobdetail/cronjobdetail_test.go:309`,
  `browse/website_assets_test.go:116`, and three more of the same shape.

`browse/browse_more_test.go:374` does drain the real `tea.Tick`, and so really blocks 250ms on
every run. The cost is not mainly wall-clock: `tui.ScheduleCacheSyncRetry`
(`internal/tui/kindsync.go:151`) is only ever tested by injecting `CacheSyncRetryMsg`
synthetically, so **the tick-to-message path itself is untested**, as are `namespace.go:203`,
`keycast.go:58` and `debugpanel/node.go:116`. `forward.go:452`'s 2s→30s reconnect backoff is never
exercised across a real backoff either — only the immediate first flip is.

The model to copy already existed in-tree: `internal/app/update_test.go` tests the 24-hour release
check with zero sleeps by setting `LastChecked` relative to now, and `kube/health.go:282` takes
`time.Now()` as a parameter. Where injection is cleaner than synctest, prefer injection.

**What changed.** Eight bubbles now. The debounce test asserts the timing it is named for
(nothing at 249 ms, the reload at 251 ms) instead of blocking 250 ms and checking only a message
type, and it costs 15 ms. A new `whocan` test drives the retry chain end to end — cache unsynced,
tick fires, cache filled meanwhile, screen leaves loading — which is the half the existing tests
had to skip. `ProbeContexts`' concurrency test asserts an *exact* elapsed time rather than
"faster than serial", a threshold CI load could cross for reasons unrelated to the code. The five
`time.Sleep(50ms)`-then-assert-a-negative sites in the lazy-informer tests became `synctest.Wait()`,
which is not just faster but strictly stronger: it returns when every goroutine is blocked, which
is exactly the "nothing more is going to happen" condition a negative assertion needs.

Two things did *not* convert, deliberately. `watch_test.go:162`'s 50 ms sleep is a stress-test
duration, not a negative assertion — under a fake clock it would pass instantly without the race
it exists to run. And `t.Parallel()` cannot be called inside a bubble, so parallel tests hoist it
outside.

### `govulncheck` does not gate pull requests


`.github/workflows/govulncheck.yml` runs on `schedule: "0 6 * * 1"` and `workflow_dispatch` only —
no `pull_request`, no `push`. A PR introducing a vulnerable dependency merges clean and is caught
up to seven days later. `govulncheck` is already pinned in `mise.toml` and mise setup is already a
step in the `quality` job, so closing this costs one step.

### Linters considered and rejected

`usetesting` and `nolintlint` were added and earned their place immediately: between them they
found 50 test contexts that should have been `t.Context()` and 14 rotted suppressions (12 that
suppressed nothing at all, one naming a linter not in the enabled set, one with no reason given).

**`noctx` was tried and rejected.** Every one of its seven findings here was a suppression waiting
to happen: the three `kubectl` spawns in `kube/debug.go` are interactive tty handoffs, where
`tea.ExecProcess` suspends the TUI and the process runs until the *user* exits it, and the fake's
`net.Listen` sits behind the fixed `ForwardDialer` interface, which has no context to pass. It also
reports inconsistently — the byte-identical `exec.Command("kubectl", …)` calls in `exec.go:175`,
`edit.go:28` and `update/browser.go` are not flagged. A linter whose every finding becomes a
`//nolint` is noise, not coverage. This repo's config is a deliberately reviewed allow-list; it
should stay one.

### Smaller tooling notes

- Three Go versions in three places: `go.mod` said `1.26.0`, `mise.toml` pinned `1.26.6`, and
  `ci.yml` hardcoded `1.26.5` for the Windows job. The Windows job now reads the version out of
  `mise.toml` like every other job, so there are two numbers instead of three and only one of them
  is a pin. (The `goreleaser` version is still duplicated by hand across `ci.yml` twice and
  `release.yml`.)
- `go.mod`'s `godebug`, `tool` and `ignore` directives were all considered and are **no-ops for
  this repo**: nothing anywhere sets a `GODEBUG` or `GOFIPS140` value, every tool is pinned through
  mise rather than run via `go run`, and there is no vendored asset tree large enough for `ignore`
  to matter. Adding them with nothing to express would be cargo cult.
- Two dead `//nolint` directives: `helm_fuzz_test.go:54` suppresses `gosec`, which is not an
  enabled linter, and `forward.go:30` is the only bare one with no reason given. `nolintlint`
  would have caught both.
- One fuzz target exists (`FuzzDecodeHelmReleases`, well-built, seven seeds across base64 → gzip →
  JSON) but no `testdata/fuzz/` corpus is checked in, so any crash it ever found is unpinned.
- No `-race` on the Windows job (documented: no gcc on the runners) or on the e2e job; no coverage
  instrumentation anywhere.

## Pre-generics idioms

Volume, not risk. `slices` was imported in 16 files but used only as `Contains`/`Equal`/
`SortFunc`/`DeleteFunc`; `maps` only as `Clone`/`Copy`; `cmp` and `iter` were imported zero times.
All of the below is now converted — the `sort` package has no call sites left anywhere in the tree.

Two notes for anyone repeating this exercise:

- **`slices.LastIndexFunc` does not exist in the standard library.** It is a `golang.org/x/exp`
  function, and it is easy to reach for by name. The stdlib idiom for a reverse scan is
  `for i, v := range slices.Backward(s)`.
- **`for i := range n` is not a drop-in for `for i := 0; i < n; i++`.** The range form gives a
  fresh index per iteration, so a loop whose body *advances* the index to skip ahead silently stops
  skipping. `yamlview/fold.go` does exactly that to jump past a folded block; converting it broke a
  test, and it keeps its C-style loop with a comment saying why.

- **44 `sort.Slice`/`SliceStable`/`Strings` call sites** across 28 non-test files. Thirteen are
  multi-key comparators, several byte-for-byte duplicates of the same "namespace, then lowercased
  name" ordering — `cmp.Or` territory, and an argument for extracting the shared comparator once.
- **A hand-written insertion sort** at `poddetail/view.go:665-676`. The whole twelve-line function
  is `slices.Sorted(maps.Keys(m))`.
- **A literal reimplementation of `slices.Index`**, generics and all, at
  `components/palette/palette.go:365-372`.
- 18 contains loops, 8 index-of loops, 6 manual map-key collections, 2 reverse scans that are
  `slices.LastIndexFunc`, 3 `make`+`copy` clones, 6 C-style counting loops.
- `internal/tui/help.go:174` uses **subtraction as a comparator** (`natural[a] - natural[b]`),
  which is overflow-unsafe.

One of these was a performance issue rather than a style one: `resources/resources.go:353` called
`strings.ToLower` on **both** operands inside the sort comparator — n·log n allocations per list
rather than n — and `resources.List` runs on every watch-driven reload from `browse/model.go:1009`.
The same `strings.Compare(strings.ToLower(a), strings.ToLower(b)) < 0` shape appeared at eight
sites (the `strings.Compare` wrapper is itself redundant: `a < b` says the same thing).

Lowering each key once into a projected row, and sorting with `slices.SortStableFunc` + `cmp.Or`,
takes a 5000-object namespace from 9.9 ms / 7.2 MB / 170,611 allocations per reload to
3.9 ms / 3.7 MB / 15,004 — 2.6× faster, 91% fewer allocations. At the far more common 100 objects
it is 2.3× faster for 15% of the allocations.

### Three `iter.Seq` conversions worth making

`kube/logs.go:58` `ScanLogLines(ctx, reader, emit func(string) bool) error` and
`podlogs/stream.go:217` `streamContainer(…, emit func(LogEntry) bool) error` are already the
`iter.Seq` protocol written by hand — `emit` returns `bool`, and `false` stops the walk.

The one with a real payoff is `kube/helm_workloads.go:44` `HelmReleaseWorkloads`. Its only
non-test caller, `app.go:130` `rolloutPending`, ranges the result once and returns on the first
hit, but the function has already split the whole manifest into a `[]string` and unmarshalled every
workload document by then. It now has an `iter.Seq` form (`HelmReleaseWorkloadsSeq`) that
`rolloutPending` ranges, so the early exit actually stops the work; splitting is lazy too, so a
large manifest no longer becomes a slice of strings up front.

**Better than the audit claimed**, in fairness to the existing code: it already pre-filtered on a
cheap `topLevelKind` string scan and only unmarshalled documents that were workloads. The saving is
the later *workload* documents plus the split, not "every document".

The two log-streaming push callbacks (`kube/logs.go:58` `ScanLogLines`, `podlogs/stream.go:217`
`streamContainer`) were **left alone**. They are already the `iter.Seq` protocol written by hand,
so converting them is pure style with no payoff, and they sit on the live log path — not a good
trade.

Counterexample worth not touching: `kube/helm.go:363` `HelmReleaseSecretsFor` builds a filtered
slice *specifically* to avoid decoding work, as its doc comment explains. Materializing is the point.

## Deferred, with reasons

| Item | Why |
|---|---|
| `log/slog` over `diag.Sink` | The sink's primary contract is being an `io.Writer` for klog's pre-formatted lines, which slog does not want. Only three `Logf` call sites, all in the crash/startup path. Real work, low payoff. |
| `encoding/json/v2` | Nothing is on a per-frame path. The hottest site is one Helm release decode per revision per list load, already capped by an `io.LimitReader`. |
| `unique.Handle` / `weak` | No measured need. The advice is to reach for these with a benchmark in hand, and there were none — see the benchmarks gap above. |
| `os.Root` | The one candidate (`helmrepo.go:209`) is read-only and same-trust-domain; a name-validation guard is proportionate, an `os.Root` refactor is not. |
| `c.mu` held across the O(all-informers) `HasSynced` sweep in `recordWatchError` (`cluster.go:680-687`) | Real UI-jank source under a flapping connection with many lazy informers — every reflector's watch error contends with the render loop's own `ListRaw → ensureKind`. But it touches the lazy-informer invariants and deserves its own change with its own evidence. |
| A session-scoped `context.Context` on `tui.Session` | Would let the ~35 `context.WithTimeout(context.Background(), …)` sites inside `tea.Cmd`s derive rather than root, so a quit or context-switch cancels them, and would let `ListRaw` and `probeTimeZoneCapability` stop discarding the ctx they accept. Correct, but a cross-cutting refactor of every task package. |
| The remaining `sort`-era comparators' *semantics* | The sweep changed how sorting is spelled, not what it orders. Seven of the eight `strings.ToLower` comparators still lower per comparison; only `resources.List`'s — the one on the reload path, and the one with a benchmark — was restructured. |

## If you continue this

The next two things worth doing, in order:

1. **Shrink the `c.mu` hold in `recordWatchError`** (`cluster.go:680-687`). Every reflector's watch
   error takes the same lock the render loop needs via `ListRaw → ensureKind`, and holds it across
   an O(all-informers) `HasSynced()` sweep. Under a flapping connection with thirty lazy informers
   started, that is UI jank by construction. It needs its own change because it touches the
   lazy-informer invariants in `docs/lazy-informers.md`.
2. **Give `tui.Session` a context.** `app.go` already builds exactly the right one and never makes
   it reachable, so nothing cancels a task's in-flight reads on quit or context switch.
