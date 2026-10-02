# 01 — Data layer & lazy informers

Scope: `internal/kube/{watch,cluster,kinds,count,transform,request_gate,discovery,dynamic}.go`,
the two lister decorators in `internal/app/app.go`, `internal/tui/kindsync.go`,
`docs/lazy-informers.md`, and the call sites that consume `KindSynced`/`KindError`/
`KindForbidden`/`CountLive`.

Verification: `go vet ./internal/kube/ ./internal/app/` is clean, and `go test ./internal/kube/
./internal/app/ ./internal/tui/` passes. `-race` could not be run because the sandbox ran out of
temp disk. Every finding below was traced by hand to the `file:line` given. Anything not
observed at runtime is marked **unconfirmed**.

## Summary

The core machinery is in good shape:

- `ensureKind` is atomic, and the `typedKinds` table is the single registration path.
- Generation guards cover every watch callback.
- The `allKindsSynced` latch has resolved the `recordWatchError` sweep that
  `go-practices-review.md` deferred.
- Both decorators forward every optional seam that a consumer type-asserts. Each `.(…)`
  assertion on a lister in `tui`/`browse`/`helmhistory` maps to an asserted forward in
  `app.go:440-457`.
- Every `KindsSynced` gate is followed by `KindsError`.

The defects are at the edges of that machinery:

- **The goto palette's breadth-first guard has a hole.** `KindSynced` falls through to "settled"
  for `KindCustomResourceDefinition` before 14b has ever been opened. The first keystroke in
  `g` then starts the full-CRD informer, which brings back the multi-MB read that §5.1 removed.
- **Three paths still race a context switch:**
  - `ListRaw`'s factory snapshot.
  - `Start`'s discovery tail.
  - `notifyScope`'s Reached latch.

  The worst of them leaves an informer running with no watch-error handler. Its spinner can
  then hang, which is the exact thing `KindSynced` promises cannot happen.
- **A clean watch-stream EOF is reported to `health` as a connection failure.** Client-go
  rotates every watch every 5–10 minutes, so each rotation is reported this way.
- **Smaller issues:**
  - The CRD-column fetch wastes the same megabyte-class read on curated kinds that never
    use the columns.
  - A restored CRD kind is lost at launch.
  - The 14b COUNT column shows a made-up `0`.

Counts: **1 high, 6 medium, 9 low.**

## Findings

| # | Sev | Location | Issue | Suggestion |
|---|---|---|---|---|
| H1 | high | `internal/kube/cluster.go:710-730`; `internal/tui/goto.go:468`; `internal/resources/groups.go:60` | `kindSyncedLockedKey` returns `true` for `KindCustomResourceDefinition` when 14b hasn't been opened, so the goto fuzzy corpus treats it as "already started", calls `ListRaw`, and starts the full-CRD dynamic informer (the 2.76 MB+ read §5.1 removed) on the first keystroke in `g`. | Treat `KindCustomResourceDefinition` with no `dynKinds` entry as not synced (return `false`), like typed and discovered kinds already are. |
| M1 | medium | `internal/kube/cluster.go:1177-1184`, `watch.go:308` | `ListRaw` runs `ensureKind` and then snapshots the factory in a *separate* critical section. A `SwitchContext` in between hands `tk.list` the new factory, whose `Lister()` registers an informer with no handlers. The next `factory.Start` runs it, and the later `ensureKind` then fails `SetWatchErrorHandler`, and that error is ignored. Forbidden and stall errors for that kind are never recorded, so `KindSynced` stays false forever. | Have `ensureKind` return the factory it registered against, under the same lock, and list from that. Return empty without touching any factory when `stopCh == nil`. |
| M2 | medium | `cluster.go:478-484`; `dynamic.go:120-132`, `:161`; `capability.go:60` | `Start`'s post-sync tail is not generation-guarded. A `Start` superseded by `SwitchContext` (for example the detached initial `Start`, `app.go:1700`, still inside slow discovery) can write `synced`/`reached`/`discovered` into the new context. `discoverCustomResources` also reads `c.clientset` without the lock, which is a data race with `cluster.go:1100`. `app.go:1704` then sends `CRDsDiscoveredMsg`, and the registry is rebuilt from the wrong cluster's CRDs. | Snapshot `gen`, clientset and metadata client under `c.mu` at the top of `Start`. Re-check `gen` before each write (`synced`/`reached`, `discovered`, `tzCapability`). |
| M3 | medium | `request_gate.go:125-145` → `watch.go:300-304` → `cluster.go:836-842` → `health.go:268-278` | Any end of a WATCH body, including a clean `io.EOF` from the apiserver's routine `timeoutSeconds` expiry, is routed to `recordWatchError` and `health.onWatchError`. EOF is not a timeout, so the connect grace does not apply, and `ConnState` flips to `ConnReconnecting` (`Offline()`) until the replacement WATCH's headers arrive. The reflector asks for a 5–10 min timeout, so every informer does this periodically. Visible flicker is **unconfirmed** at runtime. | In `observedWatchBody`, report `io.EOF` (and a clean `Close`) to `watchUnhealthy` only, never to `health.onWatchError`. Let the reflector's own `WatchErrorHandler` report real failures. |
| M4 | medium | `dynamic.go:282-311`, `:392`; `resources/flux.go:36-55`, `argo.go:42-60`, `certmanager.go:49` | `ListRaw` fetches the *full* CRD object (schema and all) for every dynamic kind's printer columns. That includes Flux, Argo `Application` and `Certificate`, whose curated descriptors never read `PrinterColumns`. The fluxtree screen pays this for every Flux kind. The write-back loop at `:392` also compares `ResourceKind(dk.Kind)` instead of `RegistryKind()`, so `FluxHelmRelease` gets marked fetched but is never updated. | Skip `ensurePrinterColumns` for curated kinds (check `IsFluxGroup`, Argo `Application`, `Certificate`). Use `RegistryKind()` at `:392`. |
| M5 | medium | `internal/app/session.go:44,80-82`; `tui/tasks/browse/model.go:785-799`; `tui/model.go:761-773` | At launch the registry is `DefaultRegistry()` until discovery finishes. A persisted CRD kind (a Flux Kustomization, a Certificate, an HTTPRoute) therefore falls back to Pods, *and* `Location.Kind` is overwritten with `Pod`. The next quit then saves Pod, so the user's kind is lost for good. `CRDsDiscoveredMsg` is swallowed, so nothing re-applies it. | Keep the unresolved kind in `Location` while discovery is pending. On `CRDsDiscoveredMsg`, if it now resolves and the user hasn't navigated, dispatch a `GotoKindMsg`. |
| M6 | medium | `resources/crd.go:290-293`; `kube/cluster.go:1217-1228` | The 14b COUNT column shows `0` when the instance cache hasn't synced yet, is Forbidden (always the case under `--namespace-scoped`, since the read is cluster-wide), or failed. That contradicts §4's "Counts are never fabricated. Unknown renders as `–`". Opening 14b also starts one cluster-wide informer per discovered kind; that cost is accepted in §5.3, but `CountLive` now exists. | Render `–` unless `KindSynced && KindForbidden == nil`. Better: make COUNT an async `CountLive` and drop the per-kind informers. |
| L1 | low | `watch.go:343-351`, `cluster.go:631-635` | `notifyScope` checks the generation and latches `reached` in two separate lock acquisitions. A late event from the old context can therefore latch `Reached()` for a new, unreachable one, which suppresses 4c. | Fold the check and the latch into one critical section (`noteReachedIfGen(gen)`). |
| L2 | low | `count.go:336-351` | `resourceFor` doesn't know `crdGVR`, so `CountLive(KindCustomResourceDefinition)` always errors and the palette always shows `–` for CRDs. | Add `if kind == KindCustomResourceDefinition { return crdGVR, true, true }`. |
| L3 | low | `app.go:479-487`; `kube/fake` | `fake.Cluster` has no `CountLive`, so `--demo` and the goldens run the cache-count fallback (`tui/counts.go:91-95`), never the production path. That is the "fallback invisible in the fake" shape that `lazy-informers.md` §4 warns about. | Give the fake a `CountLive` that counts its seeded map, and assert it. |
| L4 | low | `watch.go:263-284`, `cluster.go:781-788`, `:884-888`, `:987-994` | `registerWatches`, `registerWatchesLocked`'s no-args "every kind" branch, `noteWatchError`, `markKindFailed` and `allStartedKindsSynced` have no production callers (tests only). The no-args branch is a ready-made breadth-first start. | Move them to an `export_test.go` or delete them. At minimum, drop the no-args branch. |
| L5 | low | `tui/namespace.go:135-145`; `browse/model.go:886-910`; `tui/kindsync.go:116-131`; `app.go:276-285` | The same sync-check adapter exists in three copies, plus structural interface mirrors. | Route `namespace.go` and `browse` through `tui.KindsSynced`. |
| L6 | low | `kinds.go:260-262`; `watch.go:353` | `ResourceChangedMsg` carries no scope. Under `--namespace-scoped`, and for Helm's per-namespace caches, an event in namespace A reloads screens showing B. | Add `Namespace` to the message and let screens skip non-matching scopes. |
| L7 | low | `dynamic.go:308`; `tui/model.go:757-760` | Every CRD add/update event, plus the "columns arrived" signal, arrives as `ResourceChangedMsg{KindCustomResourceDefinition}`, and each one makes the root rebuild the whole registry and goto corpus. That is N rebuilds while 14b's informer does its initial sync. | Use a distinct `CRDColumnsMsg` for column arrival, and only rebuild on that or on a discovery change. |
| L8 | low | `cluster.go:1186-1187` | The dynamic `ListRaw` path resolves the scope twice, in separate critical sections. | Fold into the M1 fix: `ensureDynamicKindFor` should return the `dynamicKindInfo`. |
| L9 | low | `docs/lazy-informers.md:136`, `:282`, `:504`; `CLAUDE.md:69` | The docs have drifted from the code: `KindSynced(kind)` is shown with one argument; §5.5 says Helm "answers for the scope of the most recent read" (§5.6 replaced that); and §4 says suppression is needed "at exactly two breadth-first consumers", which H1 breaks. | Update the three passages. |

## Details (high / medium)

### H1 — the goto palette starts the full-CRD informer

`kindSyncedLockedKey` (`cluster.go:689-731`) checks a key against, in order:

1. Helm.
2. `kindInformers`.
3. `dynKinds`.
4. `typedKinds`.
5. `discovered`.

Anything that falls through all five returns `true` ("genuinely unknown kind").
`KindCustomResourceDefinition` is in none of these until 14b has been opened, because
`ensureDynamicKindFor` (`dynamic.go:229-236`) only registers it on first read.

`DefaultGroups()` lists it in the Cluster group (`groups.go:60`), so this happens:

1. `gotoResourceItems` (`goto.go:449-494`) runs on every fuzzy keystroke via `gotoFuzzyItems`
   (`goto.go:304`).
2. Its `kindSynced` guard says yes.
3. It calls `resources.List` → `ListRaw(KindCustomResourceDefinition, "")`.
4. That calls `ensureDynamicKind(crdGVR)`, which LISTs and WATCHes every CRD object in full.

`lazy-informers.md` §5.1 measured that read at 2.76 MB on a 48-CRD cluster, and it grows with
operators installed. The cost doesn't stop there: every CRD Add then triggers a registry
rebuild (L7). `otherKindsIn` (`browse/hints.go:151`) escapes only because it skips
cluster-scoped kinds.

The guard exists for exactly this case. CLAUDE.md: *"why the goto palette and empty-state hints
skip un-started kinds"*.

Fix: before the final `return true`, add

```go
if key.kind == KindCustomResourceDefinition {
    return false
}
```

A stronger fix is to invert the default: answer `true` only for kinds that no informer could
ever back.

### M1 — `ListRaw` can register an unwired informer across a context switch

```go
scope := c.ensureKind(kind, namespace)   // lock, register on factory A, unlock
c.mu.Lock()
f := c.factoryForScopeLocked(scope)      // may now be factory B (SwitchContext ran)
c.mu.Unlock()
return tk.list(f, namespace, ...)        // f.X().Lister() → InformerFor registers on B
```

`ListRaw` is called from `tea.Cmd` goroutines, and it ignores its `ctx`, so
`ResetClusterContext` does not stop an in-flight read. `SwitchContext` runs in its own `tea.Cmd`.

When the interleave hits, factory B holds an informer that is not in `kindInformers`, has no
event or error handlers, and has no `watcher.register`. `Start`'s `c.factory.Start(stopCh)`
(`cluster.go:455`) starts it, or the next `ensureKind`'s `f.Start` does.

When something later reads that kind, `registerTypedWatchLocked` attaches event handlers (which
works on a running informer). But `SetWatchErrorHandler` returns `"informer has already
started"` (client-go `shared_informer.go:817`), and that error is discarded at `watch.go:308`.

From then on, a Forbidden or a persistently failing LIST for that kind never reaches
`recordWatchError`:

- `kindFailed` and `kindStalled` stay empty.
- `KindSynced` returns `false` forever.
- The screen spins.

This is the exact trap that the `typedKinds` comment (`watch.go:39-46`) describes.

The window is narrow but needs no unusual timing, just a reload in flight during a switch.
Fix: change the signature to `ensureKind(kind, ns) (scope string, f informers.SharedInformerFactory)`,
captured under the lock it already holds. Have it return a nil factory when `stopCh == nil`
(and let `ListRaw` return empty), which also stops the post-`Stop` stray registration that
`TestListRawAfterStopDoesNotStartInformers` tolerates today.

### M2 — `Start`'s tail writes into whichever context is current

After `waitForCacheSync`, `Start` does the following with no generation check:

- Sets `c.synced`/`c.reached` (`cluster.go:478-481`).
- Runs `refreshDiscovery`, which assigns `c.discovered = custom` (`dynamic.go:129-131`).
- Runs `probeTimeZoneCapability`, which assigns `c.tzCapability` (`capability.go:65-67`).

Both network calls also read `c.clientset` without the lock (`dynamic.go:161`,
`capability.go:60`) while `SwitchContext` writes it under `c.mu` (`cluster.go:1100`). That is a
race-detector violation.

The realistic trigger:

1. The detached initial `Start` (`app.go:1700-1705`) has synced the eager caches. Pods are on
   screen.
2. It is now in `ServerGroupsAndResources`, which can take seconds when one aggregated API is
   down.
3. The user switches context.
4. If the new `Start`'s discovery finishes first, the old one overwrites `discovered` with the
   old cluster's CRDs.
5. `app.go:1704` then sends `CRDsDiscoveredMsg`, and the root rebuilds the registry and goto
   corpus from them.

Separately, `SwitchContext` never resets `tzCapability`, so a failed probe on the new context
leaves the old context's answer in place. That is out of scope here; see review 02. It does cut
against CLAUDE.md's "never risked as a write that an older API server could silently prune".

Fix:

1. Capture `gen`, `c.clientset` and the metadata client under the lock at the top of `Start`.
2. Pass them into `refreshDiscovery` and `probeTimeZoneCapability`.
3. Gate each write on `c.generation == gen`.

### M3 — a clean watch rotation is reported as an outage

`observedWatchBody.Read` calls `finish(err)` on *any* read error, including `io.EOF`
(`request_gate.go:125-131`). `TestWatchObserverReportsEstablishedBodyEnd` pins exactly that.

The resulting chain:

1. The `ended` callback is `recordWatchError(gen, kind, scope, io.EOF)` (`watch.go:302-304`).
2. The kind is synced and the error is not a permission error, so it sets
   `watchUnhealthy[key]` (`cluster.go:836-841`).
3. It then calls `health.onWatchError(EOF, synced=false, now)`.
4. `synced` is false because `watchUnhealthy` is now non-empty.
5. `EOF` is not `isTimeout`, so `inConnectGrace` returns `false`.
6. `onWatchError` sets `ConnReconnecting` (`health.go:271-278`).

Recovery comes only when the reflector's replacement WATCH returns headers:
`recordWatchEstablished` → `retryNow` → ping → `Connected`.

Client-go's reflector requests `timeoutSeconds` between 5 and 10 minutes, randomized. So in
steady state each started informer produces one `Offline()` blip per rotation. With ten
informers that is roughly one a minute. Each blip can briefly:

- show 4a's banner,
- pause metrics polling,
- bump `Attempt`.

Whether that is visible depends on RTT (**unconfirmed** live). The design comment
(`request_gate.go:112-118`) wants body-end detection for the HTTP-410/streaming-relist case, and
that is still served if EOF marks `watchUnhealthy` without calling `health.onWatchError`. The
`/livez` gate in `recordPing` (`health.go:406`) already keeps the header off green until the
replacement WATCH is up.

### M4 — wasted full-CRD GETs for curated kinds

`ensurePrinterColumns` (`dynamic.go:282`) runs on every first `ListRaw` of any dynamic kind. It
does a full `Get` of that CRD, which is the object §5.1 avoids listing because it carries every
version's OpenAPI schema.

`fluxDescriptor`, `argoDescriptor` and `certificateDescriptor` use fixed columns and never read
`dk.PrinterColumns`, so for those kinds the fetch is pure cost. Opening the Flux tree reads every
Flux kind, which means one full Flux CRD per kind (Flux's are among the largest in common use).
Each successful fetch also triggers `notify(KindCustomResourceDefinition)`, which makes the root
rebuild the whole registry.

There is also a second bug. `fetchPrinterColumns` resolves the CRD name via `RegistryKind()`
(`:355`) but writes back by `ResourceKind(dk.Kind)` (`:392`). For `KindFluxHelmRelease` the
write never matches, yet `crdColumnsFetched[kind] = true` was already set at `:390`. The fetch
happened, the data was dropped, and it is never retried.

### M5 — launch loses a persisted CRD kind

1. `BuildSession` seeds `Registry: resources.DefaultRegistry()` (`session.go:44`) and restores
   `Location.Kind = pc.Kind` (`:80-82`).
2. Discovery runs only at the tail of the detached `Start`.
3. `browse.New` can't resolve the kind, so it falls back to Pod and *mirrors Pod back into
   `Location.Kind`* (`browse/model.go:785-799`).
4. `CRDsDiscoveredMsg` rebuilds the registry but is "swallowed (not forwarded to the task)"
   (`tui/model.go:761-773`).

A Flux, Gateway or cert-manager user who quits on their usual list reopens kute on Pods, and the
next quit persists `Pod`. The context-switch path doesn't have this problem, because it runs
discovery before restoring the kind (`tui/context.go:295`).

### M6 — 14b COUNT fabricates zero

`projectCRD` calls `counter.CountInstances(...)`, which returns `len(cache)` with no sync check
(`cluster.go:1217-1228`), and renders `strconv.Itoa(count)`. As a result:

- The first projection of every row shows `0` while each instance informer is still doing its
  initial LIST.
- Forbidden kinds show `0` permanently. Under `--namespace-scoped` that is all of them, since the
  read is cluster-wide.

`lazy-informers.md` §4 states "Counts are never fabricated. Unknown renders as `–`, not `0`", and
§5.3 already names `CountLive` as the right tool. At minimum, render `–` unless `KindSynced` is
true and `KindForbidden` is nil.

## Test gaps

- **H1:** no test drives `gotoResourceItems`/`gotoFuzzyItems` against a real `*kube.Cluster`
  (typed and dynamic fakes). `goto_test.go` uses `countOnlyLister`, a double. Add a
  recorded-actions test: open the goto palette, type one character, and assert
  `crdListActions(dyn) == 0`.
- **M1:** `TestLazyReadIsRaceFree` (`lazy_test.go:166`) doesn't interleave with
  `SwitchContext`. Add an invariant test: after a switch with concurrent `ListRaw`s, every
  informer in `c.factory` is also in `kindInformers`. The current API gives no way to enumerate
  the factory, so assert instead that a Forbidden reactor on the re-read kind ends up in
  `KindForbidden`.
- **M2:** no test that a `Start`/`refreshDiscovery` from a superseded generation leaves
  `discovered` untouched. `fetchPrinterColumns` has this guard and a test for it; discovery has
  neither.
- **M3:** nothing exercises `ended(io.EOF)` through `recordWatchError` into `ConnState`. Add a
  cluster-level test: synced Pod cache, deliver EOF through the observer, and assert
  `ConnState().Phase` stays `ConnConnected`.
- **M4:** no test for `fetchPrinterColumns` on a substituted kind, and none asserting that
  curated kinds issue no CRD `get`.
- **M5:** no launch test with a persisted CRD `pc.Kind`.
- **M6:** no projection test for COUNT on an unsynced or forbidden kind.
- **Fake completeness:** `fake.Cluster` has no `CountLive` (L3), so no golden covers the
  production palette-count path.

## Quick wins

1. **H1:** one `if` in `kindSyncedLockedKey`, plus the recorded-actions test.
2. **M4:** `RegistryKind()` at `dynamic.go:392`, and an early return in `ensurePrinterColumns`
   for curated groups.
3. **L2:** a `crdGVR` case in `resourceFor`.
4. **L1:** merge the generation check and the `reached` latch in `notifyScope`.
5. **M6:** render `–` from `projectCRD` when the instance cache isn't settled.
6. **L9:** fix the three stale doc passages.
