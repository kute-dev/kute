# 03 — Browse & resource catalog

Scope: the shared `tasks/browse` skeleton (`model`, `update`, `view`, `keys`, `filter`, `sort`,
`selection`, `grouping`, `loading`, `hints`, `auxkinds`, `namespaces`, `follow`, `crddetail`,
`metrics`), `internal/resources` (`registry`, `columns`, `projections`, `crd`, `groups`,
`resources`), and `tasks/objectdetail` (14d). Kind-specific browse files are touched only where
they sit on the shared path (the `m.pods` lookups in `debug.go`/`delete.go`, the `r` key's
precedence).

Verification: `go vet` and `go test` are clean for `browse`, `resources` and `objectdetail`.
Every finding below was traced to the `file:line` given. Findings marked **(proved)** were
reproduced with a throwaway `go test -overlay` test or benchmark. No repo file was changed.
Anything not observed at runtime is marked **unconfirmed**.

## Summary

The skeleton's core contracts mostly hold:

- The epoch guards (`reloadEpoch`/`metricsEpoch`) cover every async reply.
- Every empty-state entry goes through `KindsSynced` and then `KindsError`, aux kinds included.
- `ClusterContext()` parents every read.
- `RoutePaste` precedence mirrors `updateKey` branch for branch.
- `CountLive` keeps scoped mode's hints off the informers.
- Pod metrics are `PodKey`-keyed.

The defects are where the list stops being one namespace or one cluster's worth of small data:

1. **A busy cluster's list never refreshes.** The app's event bridge debounce is trailing-edge
   only, so a kind that changes faster than every 250 ms never gets delivered at all. Measured:
   0 deliveries in 3 s of a 5 Hz Pod stream.
2. **All-namespaces mode resolves pods by bare name.** With same-named pods in two namespaces
   (`postgres-0`, a chart installed twice), `l`/`↵`/`x` act on the **other** namespace's pod.
   For `x` that means exec into the wrong pod.
3. **Discovered CRDs are keyed by bare Kind.** A Knative `Service` CRD replaces the core
   Services descriptor. Istio and Gateway API `Gateway`, and CAPI and CNPG `Cluster`, hide one
   another.
4. **The 10c empty state on Helm Releases reads breadth-first.** It starts one release-Secret
   informer per namespace in the cluster, plus the cluster-wide one. That is exactly the read
   §5.5 exists to prevent.
5. **Render cost grows with every loaded row, not just the visible ones.** One frame costs
   44.5 ms and 42 MB at 5000 pods. The existing `BenchmarkBrowseRender` misses this because it
   measures the loading skeleton.

Counts: **5 high, 6 medium, 12 low.**

## Findings

| # | Sev | Location | Issue | Suggestion |
|---|---|---|---|---|
| H1 | high | `internal/app/app.go:1873-1881` (bridge); `browse/update.go:118-125`, `:421` (compounding) | `forwardEvents` resets a kind's deadline to `now+250ms` on **every** event and has no max-wait. A kind that changes more often than every 250 ms is never delivered while the stream lasts. **(proved)** 0 `ResourceChangedMsg` in 3 s at 5 Hz. Browse then adds its own trailing debounce: every delivered event bumps `reloadEpoch`, which also throws away a load already in flight. A Node list (aux Pod) or a Deployment list (aux RS/HPA/Pod) can therefore keep discarding its own reloads. | Add a max-wait to the bridge: flush a kind at `min(lastEvent+250ms, firstPending+1s)`. In browse, schedule a reload only when none is pending, and don't let an event's epoch bump invalidate a `load()` already dispatched (use a separate "dirty" flag and reload once more after it lands). |
| H2 | high | `browse/model.go:1428-1437`; `update.go:1239`, `:1260`, `:1389`; `debug.go:97`; `delete.go:85`; `selection.go:82-90`; `update.go:1262-1270` | `podsFromObjs` keys `m.pods` by `p.Name`. In all-namespaces (6b) mode, same-named pods overwrite each other, so logs, pod detail, exec, debug and delete's owner/grace act on the wrong pod. **(proved)** Cursor on `aaa/postgres-0` → logs opened for `bbb/postgres-0`. Two related defects: `restoreSelection` and the 5a sibling list also identify rows by name only, so after any reload the cursor can jump to the other namespace's row. The fallback `kube.Pod{Namespace: m.namespace}` uses `""` in this mode. | Key `m.pods` by `kube.PodKey(ns, name)`, the same rule CLAUDE.md sets for metrics. Track the selection as `(namespace, name)`. Pass namespace-qualified siblings, as `CronJobSiblingRef` already does. Use `row.Namespace` in the fallback. |
| H3 | high | `resources/crd.go:378-404`; `kube/dynamic.go:203-214` | `dedupeDiscovered` and `BuildDiscoveredRegistry` key on `RegistryKind()`, which is the bare Kind except for the one substituted Flux kind. **(proved)** A `serving.knative.dev` `Service` CRD replaces the core `Service` descriptor (`Custom=true`, columns `[Name Age]`, APIGroup knative). The core Services list loses TYPE/CLUSTER-IP/PORTS, `↵` routes to 14d against the wrong GVR, and Knative Services can't be reached at all. Istio `Gateway` vs Gateway API `Gateway`, and CAPI `Cluster` vs CNPG `Cluster` (also `Backup`, `Certificate` from ACK ACM), silently hide one another. Which side survives depends on discovery order. `Certificate` and `HTTPRoute` are also recognised by bare Kind (`crd.go:388`, `:390`). | Never let a discovered kind take a built-in key. On any collision (with a built-in or with another group), derive a group-qualified registry key, e.g. `Service.serving.knative.dev`, and record it in a generated substitution table so `APIKind()`/`ResourceArg()` keep working. Recognise HTTPRoute by `gateway.networking.k8s.io`, and Certificate by `cert-manager.io`. |
| H4 | high | `browse/hints.go:106-139`, `:82-96`; `internal/app/app.go:94-118`; `kube/helm.go:592` | When a Helm Releases list is empty, `loadEmptyHints` runs `busiestOtherNamespace`, which calls `resources.Count(KindHelmRelease, ns)` for **every namespace**. Each call goes through `helmAwareLister.ListRaw` → `ensureHelmSecrets(ns)`, which starts a per-namespace release-Secret informer. `allNamespacesCount` then starts the cluster-wide one (8.19 MB on the cluster §5.5 measured). Every release in every namespace is also gunzipped and decoded, and `UnsettledWorkloads` is run per namespace. This is a breadth-first read of the heaviest kind, triggered by a common state. Traced by code; the fake-clientset action count is **unconfirmed**. | Skip `busiestOtherNamespace` and the cluster-wide count for kinds with per-namespace caches (`KindHelmRelease`), or answer them with `CountLive` on `secrets` with the `type=helm.sh/release.v1` field selector. More generally, have `busiestOtherNamespace` skip kinds whose `KindSynced(kind, "")` cache isn't already started, matching the rule `otherKindsIn` follows. Add a `lazy_test`-style action-count test. |
| H5 | high | `browse/view.go:1069-1110` (`tableBody`), `:294` (`Health(m.rows)`), `metricsMax`; `browse/bench_test.go:24` | `tableBody` builds styled `components.Row` cells for **every** `m.display` entry on every frame, but only about 28 rows are ever visible. **(proved)** Ready-state render costs 1.5 ms/0.7 MB at 50 pods, 5.4 ms/4.4 MB at 500, and **44.5 ms/42 MB at 5000** per frame. That cost is paid on every `j/k`, every metrics tick, and every reload. `BenchmarkBrowseRender` reports a constant 0.33 ms because `m.Update(m.Init()())` hands `Update` a `tea.BatchMsg`, so it benchmarks the loading skeleton (state `loading`, 0 rows). | Build cells only for the `[offset, offset+tableDataRows)` window and give `components.Table` the total for its footer. This mirrors podlogs' `visibleWindow`. Fix the benchmark to drain `m.load()()` and add a 5000-row case. Cache `Health`/`metricsMax` per load and per metrics poll. |
| M1 | medium | `objectdetail/load.go:39-42`; `update.go:49-52`, `:120-126` | 14d reads EVENTS through `ObjectEvents` → `ListRaw(KindEvent)`, which lazily starts the Event informer and returns an empty cache on first use. There is no `KindsSynced` gate, and the screen reloads only on `msg.Kind == m.kind`, never on `KindEvent`. The first 14d open in a session can therefore show "no events" until the object itself changes. For an object with no conditions, it **redirects to YAML** even though events exist. This breaks both the empty-state invariant and the aux-kind reload invariant. Traced by code; not run against a live cluster. | Gate the empty/redirect decision on `tui.KindsSynced(lister, ns, KindEvent)` and retry via `ScheduleCacheSyncRetry`. Reload on `KindEvent` changes. (poddetail has the same shape; see review 04.) |
| M2 | medium | `browse/filter.go:22-42`; `grouping.go:121-151` | `applyFilter` returns rows in fuzzy-**score** order. `buildDisplayRows` assumes namespace runs are contiguous and that unhealthy rows form a sorted prefix. **(proved)** With filter `web` in all-namespaces mode, `aaa` renders **twice**: first a collapsed "all running" summary (hiding nothing), then a header plus its crashlooping row. A troubled namespace can therefore be labelled healthy. Score order also overrides a manual `1-9` sort and the unhealthy-first default in an ungrouped filtered list. | Use fuzzy matching only as a predicate and keep the `m.rows` order (stable filter of the already-sorted rows). Keep `MatchedIndexes` for highlighting. |
| M3 | medium | `browse/view.go:150-170` vs `:177-211`; `selection.go:255-262` | `stripLineCount` (which `tableDataRows`/`clampOffset` use) counts 2 lines offline, but `Strips` emits 3: the banner, the `errorBannerRuleLine` and the stale strip. It also never counts `auxKindsDeniedNote` or `followNote`. `components.Table` doesn't keep the selection visible itself, so the cursor scrolls off the bottom. **(proved)** Offline with an aux note: `stripLineCount=2`, `len(Strips)=4`, and the selected `p-40` is not in the rendered frame. The aux note is routine: "still loading" appears on every first Deployments open. | Derive the count from `len(m.Strips(width))`, or share one builder. Add a test that asserts `stripLineCount() == len(Strips())` across states. |
| M4 | medium | `browse/update.go:794-825` | 4a's `r retry now` is listed in the offline keybar and banner, but the `r` switch tries `resourceEditable && Ready`, Flux, Argo, Cert and Forward first. Offline rows stay `Ready`, so on Deployments, StatefulSets, DaemonSets, Flux, Argo, Certificates and Forwards, `r` opens the set-resources panel or begins a mutation (which `actions` then refuses) instead of calling `RetryNow()`. | Move the `m.offline() && m.retrier != nil` case first. |
| M5 | medium | `browse/view.go:869-950`; `hints.go:39-66` | 10c's copy is wrong in all-namespaces mode. **(proved)** It renders "no secrets in " with a blank namespace, "the namespace exists and you can read it", "a all namespaces" (already active), and "this namespace has 1 pod", where the count is cluster-wide. Even in single-namespace mode, "the namespace exists" is never checked, although the Namespace cache is eager. A namespace restored from state that has since been deleted gets that false claim. | Branch on `m.grouped()`: show "no secrets in any namespace", and drop the `n`/`a` lines or reword them. Check the namespace against the Namespace cache, and say "namespace X no longer exists" when it's missing. |
| M6 | medium | `browse/crddetail.go:43-51`; `objectdetail/update.go:260-292`, `:113-118` | 14d's `j/k` siblings are bare names, and `moveSibling` keeps `m.namespace`. In all-namespaces mode, stepping onto a sibling in another namespace finds nothing, sets `gone`, and shows "\<Kind\> deleted · press any key to go back", which is false. The sibling `index` is also chosen by name (first match). | Pass `(namespace, name)` siblings (same fix as H2). Treat "not found" as deleted only when `KindSynced` for that scope. |
| L1 | low | `browse/update.go:734-743`; `objectdetail/update.go:168-171`, `:209`, `:240`; `browse/update.go:1068`, `:1137` | Hand-wired keys bypass the verb registry. `case "V"` is an unregistered alias for set-resources and skips the `Ready` gate that `verbs.SetResources.Key` checks. `"+"`/`"-"` are literals because `Scale.Key` is the display string `"+/−"`. `"C"` force-delete is a literal in four places, and several comments still say `ctrl-k`. objectdetail binds only `j`/`k`, not `↑`/`↓`, contrary to "j/k ≡ ↑↓ everywhere". | Drop `V`. Give `Scale` a key set. Use `verbs.ForceDelete.Key`. Add `up`/`down` to 14d's sibling movement. |
| L2 | low | `objectdetail/view.go:236`, `:272`, `:326` | Render reads the clock: `shortAge` calls `time.Since`. This breaks the pure-render invariant and makes 14d goldens time-dependent. | Stamp `now` on the model in `Update`, as browse does with `m.now`. |
| L3 | low | `objectdetail/update.go:107-125` | `gone` is never cleared when a later load finds the object again (delete + recreate, or a cache that was simply unsynced), so the "deleted" keybar and any-key-backs-out stay latched. `found=false` also conflates "not in the cache yet" and "Forbidden" with "deleted". | Set `m.gone = false` on `found`. Gate "gone" on `KindSynced && KindError == nil`. |
| L4 | low | `objectdetail/view.go:194-195` | The OWNER line renders as a purple `↗` link, but no key follows it. §14d says owner refs are links "reusing the goto machinery". | Bind `↵` or `o` to emit `GotoResourceMsg` for the owner, or drop the `↗`. |
| L5 | low | `browse/keys.go:517-521` | `CapturingInput` omits `pendingCronJobRun`/`pendingCronJobResume`, so `g`/`n`/`c`/`?` act as global shortcuts while those preflights own the keybar. | Add both to the predicate. |
| L6 | low | `browse/model.go:1229-1239`, `:1311-1318` | `rowCache` is keyed by kind+namespace with no context. After a 7a switch, Pods/default briefly shows the **previous cluster's** rows dimmed as "cached", under the new context's header. | Include the context name in `browseCacheKey`. |
| L7 | low | `resources/projections.go:337`, `:565-581`; `backend.go:55-93` | The Ingress BACKENDS projection does a full Service list and a full Pod list scan **per path**, and Ingress reloads on every Pod event (auxKinds). Measured 16.7 ms per projection at 100 ingresses × 3 paths / 5000 pods with a slice-backed lister. Both this and the Deployment previous-RS lookup use `context.Background()` in a read path. | Index Services by name and Pods by selector once per `Project` call (pass a prepared index through a closure). Thread a ctx. |
| L8 | low | `browse/model.go` (rows are static strings); `kube/cluster.go:26` | AGE (and every relative-time cell outside CronJob/Job) is frozen at projection time. It refreshes only on a watch event or the 5-min resync, so a quiet namespace shows `12s` for minutes. | Store `CreatedAt` on `Row` and render age from `m.now` (already ticked), or re-project on a slow tick. |
| L9 | low | `browse/view.go:787`, `:862` | Both the error card and the 403 card hard-code "last successful list: never", even after a successful load (`m.fetchedAt` set). | Render `fetchedAt` when it's non-zero. |
| L10 | low | `browse/auxkinds.go:171-183` | `prefetchAuxKinds` reads every aux kind at `m.namespace` and ignores `auxScope`. For Nodes, it prefetches Pods in the active namespace, while `loadNodeExtras` and the sync gate use `""`. Under `--namespace-scoped` this warms the wrong cache. | Use `m.auxScope(kind)` in the prefetch loop. |
| L11 | low | `browse/keys.go:255`, `:507`; `objectdetail/update.go:322` | The 14a pill is the full uppercase plural (`CERTIFICATES`, `HORIZONTALPODAUTOSCALERS`), but §14a says the short kind name (`CERTS`). `singularDisplay` trims one trailing `s`, which gives "Ingresse", "NetworkPolicie", "Helm Release" (fine) and "Gateway classe". | Use the CRD's `shortNames[0]` (already in discovery) for the pill. Use `DiscoveredKind.Kind` (singular, already known) for confirm labels. |
| L12 | low | `browse/model.go:502-505` | A stale comment says `podMetrics` is "namespace-scoped like rows, so pod names alone key it safely". It is `PodKey`-keyed (`view.go:1478`, `sort.go:373`), and the comment invites exactly H2's bug. | Correct the comment. |

## Details: high

### H1. The event bridge never flushes a steady stream

`forwardEvents` (`internal/app/app.go:1873-1881`) does `pending[ev.Kind] = now.Add(250ms)` for
every event and flushes only when `due <= now`. Any kind whose events arrive less than 250 ms
apart is pushed forward indefinitely. That is the normal state for Pods in a cluster of a few
thousand pods, where kubelet status updates, probes and controller churn are continuous.

The existing test (`event_bridge_test.go`) sends a burst and then goes quiet, so it never covers
this. The overlay test sent one Pod event every 200 ms for 3 s and received **zero** deliveries.

Browse's own debounce compounds it. `update.go:118-125` bumps `reloadEpoch` per delivered event,
and `applyRowsLoaded` (`:421`) drops any reply with an old epoch. Interleaved flushes of a primary
kind and an aux kind keep cancelling each other's in-flight loads.

The bridge lives in 14's scope, but its effect is browse showing a frozen snapshot with a green
"connected" badge, so it is reported here.

Fix: add a max-wait. The usual shape is `due = min(lastSeen+250ms, firstSeen+1s)`. In browse,
separate "a reload is pending" from the epoch that guards against kind/namespace/context
switches. Only `resetAndLoad` should invalidate in-flight loads, and an event during a load should
just set a dirty bit that triggers one more load after it lands.

### H2. All-namespaces mode identifies rows by name

`podsFromObjs` builds `map[name]kube.Pod` (`model.go:1434`). Every Pod verb looks up
`m.pods[row.Name]`: logs `:1239`, 5a `:1260`, exec `:1389`, debug `debug.go:97`, and delete's
owner/grace `delete.go:85`.

In 6b, the user is triaging across namespaces, and StatefulSet ordinals and twice-installed
charts guarantee collisions. The overlay test placed `postgres-0` in `aaa` and `bbb` and moved the
cursor to each in turn. Both opened `bbb/postgres-0`. For `x`, `openSelectedExec` builds the
`kubectl exec` from `pod.Namespace`/`pod.Name`, so the shell lands in the other namespace's pod,
which may be a PROD tenant's.

The same name-only identity appears in three more places:

- `restoreSelection` (`selection.go:85`): every reload re-selects the *first* same-named row.
- Sibling hand-off for 5a and 14d (`update.go:1262-1270`, `crddetail.go:43-51`).
- `goToResource`'s `pendingSelect`.

The fix is mechanical: use `kube.PodKey` for the map, `(namespace, name)` for selection, and
namespace-qualified siblings (`CronJobSiblingRef` already shows the shape).

### H3. A discovered Kind can shadow a built-in kind or another CRD

`customKindsFrom` → `dedupeDiscovered` keeps the first `RegistryKind()` it sees, and
`RegistryKind()` returns the bare Kind for everything except the one Flux substitution
(`kube/discovery.go:69-76`).

`BuildDiscoveredRegistry` then calls `registry.Register(CustomDescriptor(dk))`, which
last-write-wins over the built-ins. The overlay test fed a Knative `Service` and read back the
registry's `Service` descriptor as `Custom=true`, `Columns=[Name Age]`,
`APIGroup=serving.knative.dev`.

`ListRaw(KindService)` still takes the typed core path, so the Services list shows core Services
projected by the generic CRD projector:

- Only NAME/AGE render.
- The breadcrumb claims `serving.knative.dev/v1`.
- `↵` opens 14d.
- The Knative objects themselves are unreachable.

Between two CRDs, the effect is a silent hide: Istio and Gateway API `Gateway`, CAPI and CNPG
`Cluster`, Velero and CNPG `Backup`, cert-manager and ACK-ACM `Certificate`. The invariant
CLAUDE.md wrote for Flux ("recognised by API group, never by bare kind name … last-write-wins
Register silently replaced the Helm list") applies to every discovered kind.

Fix: generate the substitution for any collision instead of hand-listing it. The registry key
becomes `<Kind>.<group>` whenever another discovered or built-in kind already owns `<Kind>`, and
`substitutedKinds` becomes data populated at discovery, so `APIKind()`/`ResourceArg()` keep
working.

### H4. Empty Helm list → breadth-first release informers

`busiestOtherNamespace` guards only `--namespace-scoped` (`hints.go:107`). In the default mode it
lists Namespaces and calls `resources.Count(kind, ns)` for each. For most kinds this is a cheap
read of one cluster-wide cache.

`KindHelmRelease` is the one kind that is deliberately cached **per namespace** in default mode
(`helm.go:566-591`). Each `Count` goes through `helmAwareLister.ListRaw` (`app.go:94-118`), which
calls `ensureHelmSecrets(ns)` (a new filtered informer: LIST+WATCH carrying gzipped manifests),
decodes every release, and runs `UnsettledWorkloads`. `allNamespacesCount` then reads `""`, which
starts the cluster-wide release cache §5.5 measured at 8.19 MB.

The net effect is that landing on a namespace with no releases downloads every release in the
cluster twice, purely to decorate a hint. This is the scenario `otherKindsIn`'s doc comment
already calls "the launch stampede, relocated".

### H5. Per-frame render scales with total rows

`tableBody` loops over all of `m.display`, calling `rowCells` (per-cell lipgloss styles, fuzzy
highlight, metric bars), before handing them to `components.Table`, which shows only `Offset..`
`Offset+rows`. The real ready-state benchmark (overlay) shows linear growth:

| pods | ns/frame | B/frame | allocs/frame |
|---:|---:|---:|---:|
| 50 | 1.48 ms | 0.71 MB | 6.4 k |
| 500 | 5.44 ms | 4.45 MB | 24.9 k |
| 5000 | 44.5 ms | 41.8 MB | 209 k |

The checked-in `BenchmarkBrowseRender` never sees this. `benchModel` passes `m.Init()()`, a
`tea.BatchMsg`, to `Update`, which ignores it, so the benchmark renders the loading skeleton (state
`loading`, `len(rows)=0`) and reports a flat 0.33 ms for every size. That is the same false
reassurance `docs/performance.md` warns about for podlogs.

## Details: medium

- **M1 (14d events).** `objectdetail.load` → `ObjectEvents` → `ListRaw(KindEvent)` starts the
  lazy Event informer and returns immediately. `applyLoaded` treats `len(events)==0` as "no events"
  and, with no conditions, redirects to YAML. Nothing reloads on `KindEvent`, so the false empty
  state persists until the object itself changes. The fix is a `KindsSynced(KindEvent)` gate plus
  a sync retry, and a reload on `KindEvent` (scoped to the object's namespace).
- **M2 (filter order).** `fuzzy.Find` sorts by score. Grouping assumes contiguous namespace runs,
  and the fold assumes an unhealthy prefix. The reproduction rendered
  `[collapsed aaa] [header bbb] bbb/web-1 [header aaa] aaa/web-crashing-worker`.
- **M3 (strip budget).** `stripLineCount` is a hand-kept mirror of `Strips` that has drifted in
  three places. The table's own `Height` comes from `Frame` (correct), but `clampOffset` uses the
  stale count, so the cursor can sit below the last rendered row.
- **M4 (4a `r`).** The fix is a one-line reorder. The keybar and banner already promise retry.
- **M5 (10c copy).** In 6b's "all namespaces" scope, the body's "in \<ns\>", "the namespace
  exists" and "this namespace has" are all wrong. The single-namespace "exists" claim should be
  checked against the eager Namespace cache.
- **M6 (14d siblings).** Same root cause as H2, surfacing as a false "deleted" banner.

## Test gaps

- **Event bridge.** No test sends a *sustained* stream. Add one: 5 Hz for 2 s should yield ≥1
  delivery, with a fake clock or `synctest`.
- **All-namespaces identity.** No browse test has two same-named rows in different namespaces.
  Add cases for `l`/`↵`/`x`/delete targets, selection restore after reload, and 5a/14d siblings.
- **Filter × grouping.** There is no test of a filter in 6b mode or of a filter over a manual sort.
  Assert each namespace header appears at most once and an unhealthy row is never inside a
  collapsed summary.
- **Strip budget.** No invariant test that `stripLineCount() == len(Strips(w))` across
  ready/offline/aux-note/follow-note/filter combinations.
- **Discovery collisions.** `crd_test.go` never feeds a CRD whose Kind matches a built-in or
  another group's CRD. Add Knative `Service`, Istio vs Gateway API `Gateway`, and CAPI vs CNPG
  `Cluster`.
- **Empty-state reads.** Nothing asserts *what* `loadEmptyHints` fetches. Add a
  `lazy_test`-style fake-clientset action count for an empty Helm list (no per-namespace secret
  LISTs, no cluster-wide one).
- **14d sync.** No test opens 14d against a lister whose Event kind is unsynced. The fake is
  always synced, so this needs a `KindSyncChecker` test double, as browse's sync tests have.
- **Benchmark.** `BenchmarkBrowseRender` benchmarks the skeleton. Fix it (drain `m.load()()`)
  and add a 5000-row and an all-namespaces case before fixing H5, so the improvement is measured.
- **Goldens.** There are no 14d goldens for the all-namespaces empty state or the offline-with-aux-note strip
  stack. `objectdetail` goldens depend on `time.Since` (L2).

## Quick wins

1. Move `case m.offline() && m.retrier != nil` to the top of the `r` switch (M4).
2. Replace `stripLineCount`'s switch with `len(m.Strips(m.width))` (M3).
3. Key `m.pods` by `kube.PodKey` and fix the `m.namespace` fallback to `row.Namespace` (the core
   of H2, about 8 call sites).
4. Make `applyFilter` a stable predicate over the already-sorted rows (M2).
5. Fix `benchModel` to drain `m.load()()` (H5's measurement).
6. Clear `m.gone` on a found reload (L3). Drop the `V` alias (L1). Add the two missing
   `CapturingInput` gates (L5).
7. Correct the stale `podMetrics` comment (L12) and the `ctrl-k` comments, since the key is `C`.
