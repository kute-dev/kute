# 07 — Flux

Scope: `internal/kube/flux.go`, the Flux parts of `kube/{kinds,mutate,timeline}.go`,
`internal/resources/flux.go`, `tasks/{fluxtree,fluxdetail}`, `browse/flux.go`,
`internal/tui/fluxsubjects.go`, poddetail's `resolveFluxItem` (1b4eda0), the fake Flux
fixtures, and `test/e2e/flux_test.go`. Recent commits e9a39cd (chartRef nesting and
cross-namespace sources), 471cce0 (the inventory "applies" line) and 1b4eda0 (pod to reconciler
link) were read as diffs.

Verification: `go vet` is clean and `go test` passes for `fluxtree`, `fluxdetail`, `resources`
and `timeline`. Findings marked **(proved)** were reproduced with a throwaway
`go test -overlay` test in `fluxdetail`, using a recording `RawLister`. Everything else was
traced at the `file:line` given. Anything not observed at runtime is marked **unconfirmed**.
No repo file was changed.

## Summary

The §30a core is solid:

- Discovery recognises Flux by API group.
- `KindFluxHelmRelease` is substituted, and `APIKind()`/`ResourceArg()` are used on every
  kubectl string and write path.
- `projectFluxResource`'s precedence is right: suspended → stalled → Ready=False+Reconciling
  as progress.
- 30b reads only Flux kinds. It reads HelmChart only when a chartRef needs it, and it gates
  ready/empty on `KindsSynced` + `KindsError`.

The defects sit where Flux data crosses back into the rest of the app.

1. **§31a's inventory resolves objects by bare Kind and lists each one cluster-wide.**
   - A Kustomization that applies Flux HelmReleases is the standard layout. For it, 31a calls
     `ListRaw("HelmRelease", "")`, which is §18a's synthetic Helm kind. That starts the
     cluster-wide release-Secret informer and decodes every release in the cluster. This is
     exactly what §5.5 and the HelmRelease-collision invariant exist to prevent.
   - Any inventory Secret or ConfigMap (SOPS secrets are routine in Flux) starts the
     cluster-wide Secret or ConfigMap informer, purely to render `–`.
   - `↵` on such an entry opens the Helm list, not the Flux object.
2. **The same collision appears in three more places:**
   - `dependsOn` resolution reads the synthetic Helm kind and ignores `dependsOn[].namespace`.
   - 9b/16a `↵` on a helm-controller event jumps to §18a.
   - Browse's `o` drops `sourceRef.namespace`.
3. **31a misreports state:**
   - It ignores `kube.ConnStateMsg`, so its offline gate and header badge are dead.
   - It shows `RECONCILE FAILED · retry in …` on a *suspended* object, contradicting §30a's
     "suspended outranks everything".
   - Its retry countdown reads `retry in now` for any failure older than one interval.
   - It has no cache-sync gate, so a cold cache reads as "no longer exists".
4. **30b fails whole.** One Forbidden Flux kind turns the whole tree into an error. The error
   promises "retrying" but schedules no retry.

Counts: **1 high, 11 medium, 12 low.**

## Findings

| # | Sev | Location | Issue | Suggestion |
|---|---|---|---|---|
| H1 | high | `fluxdetail/load.go:192-233`; `kube/flux.go:9-15`; `app/app.go:94-118` | `buildInventory` keys entries by `ParseFluxInventoryID`'s bare Kind, which discards the group, then calls `ListRaw(kind, "")` once per kind. **(proved)** An entry `flux-system_podinfo_helm.toolkit.fluxcd.io_HelmRelease` produces `ListRaw("HelmRelease", "")`. `helmAwareLister` routes that to the cluster-wide release-Secret informer, the decode of every release, and `UnsettledWorkloads(…, "")`. A `Secret`/`ConfigMap`/`ServiceAccount` entry starts that kind's cluster-wide informer. `objectReadiness` can only ever answer `–` for those kinds. In `--namespace-scoped` mode every read here is an explicit cluster-wide one. `↵` on the HelmRelease item sends `GotoResourceMsg{Kind:"HelmRelease"}`, which lands on §18a. | Return the group from `ParseFluxInventoryID` and map `(group, Kind)` to a registry kind (`helm.toolkit.fluxcd.io/HelmRelease` → `KindFluxHelmRelease`). Add a `kube` helper next to `substitutedKinds` for this. Only list kinds whose readiness `objectReadiness` can read (workloads, unstructured with Ready/replicas). Render the rest as plain `·` rows with no read. List per namespace from the refs, not `""`. Add a recorded-actions test like `lazy_test.go`. |
| M1 | medium | `fluxdetail/load.go:120-149` | `dependencySuspended` lists `kube.ResourceKind(u.GetKind())`. For a Flux HelmRelease that is the synthetic Helm kind: it starts a per-namespace release-Secret informer, and `findObject` never matches its objects. It also ignores `dependsOn[].namespace`. **(proved)** A dependency `{name: db, namespace: data}` on `apps/app` is read as `HelmRelease@apps`, and `Suspended=false`. So "amber when a dependency is suspended" (§31a) never fires for HelmReleases or cross-namespace dependencies. | Use the object's own registry kind (`m.kind`) and the entry's `namespace` (defaulting to the object's own). |
| M2 | medium | `fluxdetail/update.go:36`; `model.go:112`; `view.go:58` | `Update` matches `case kube.ConnState`, but the root fans out `kube.ConnStateMsg` (`tui/model.go:616`). **(proved)** After `ConnStateMsg{Phase: reconnecting}`, `offline()` is still false. The `r`/`s` gates, the keybar's verb group and the header's `LiveConnBadge` therefore all treat an outage as connected. `actions.SetOffline` is never called. `tasks/certchain/update.go:33` has the same bug. | Match `kube.ConnStateMsg`, set `m.conn = kube.ConnState(msg)`, and call `m.actions.SetOffline`. Add a test that sends the real message type. |
| M3 | medium | `fluxdetail/view.go:95-101`; `load.go:353-385` | `buildFailure` ignores `spec.suspend`, and `failureCard` shows the SUSPENDED card only when `fail == nil`. **(proved)** A suspended Kustomization whose frozen Ready is False renders `RECONCILE FAILED · HealthCheckFailed · 3h ago · retry in now`, while the list shows it `‖ suspended`. The countdown is also false, because a suspended object is never retried. | Pass `suspended` into `buildFailure` (or check it first in `failureCard`). The suspended card wins, and the frozen message can be shown as a muted "last status before suspend" line. |
| M4 | medium | `fluxdetail/load.go:364-370`; `view.go:148-160` | `RetryAt = Ready.lastTransitionTime + interval`. `lastTransitionTime` changes only when Ready's *status* flips, so any failure older than one interval reads `retry in now` forever (proved in the M3 run: 3 h-old failure). That is §31a's headline number. | Project to the next multiple, `since + ceil((now-since)/interval)*interval`, and label it approximate (`~`). Or drop "retry in" once `now > since + interval`. |
| M5 | medium | `browse/flux.go:87-99`, `:104-117`; `resources/flux.go:332-350` | Browse's `o` re-expands the SOURCE cell (`helm/bitnami`). That cell never carried `sourceRef.namespace`, so the jump uses `row.Namespace`. The common layout (a HelmRelease in an app namespace pointing at a HelmRepository in `flux-system`) lands on an empty list in the wrong namespace. e9a39cd fixed this for 31a and 30b only. | Carry the resolved source `(kind, ns, name)` on `resources.Row` (or re-read the object) rather than parsing a display cell. Show `in <ns>` in SOURCE when it differs, as 31a does. |
| M6 | medium | `fluxtree/update.go:144-162` | Any `KindsError` across the Flux kinds turns the whole tree into an error state. For example, a user may be Forbidden from Buckets or ImageRepositories, or (typically) lack cluster-wide list in `--namespace-scoped` mode, since the tree always reads `""`. The message says "— retrying", but nothing is scheduled. Only a later Flux watch event, which a Forbidden kind never produces, re-runs `load`. The `msg.err` path has the same dead end. Traced by code. | Degrade per kind. Drop the failing kind from the join and show a strip note ("Buckets: forbidden"). Fail the screen only when *every* kind errors. Schedule `ScheduleCacheSyncRetry` (or offer `r`) when promising a retry. |
| M7 | medium | `fluxdetail/load.go:37-44`, `:212-232`; `update.go:78-82`; `model.go:213-225` | 31a has no `KindSynced`/`KindError` gate. A cold cache yields `gone`, which renders "\<name\> no longer exists · esc to go back". Cold caches arise when: 1b4eda0's pod-detail link opens a Kustomization whose informer isn't started; scoped mode starts a fresh per-namespace cache; or `ListRaw` returns before the first LIST. The screen self-heals on the kind's first change event (**unconfirmed** for every path). It stays wrong for good when the kind is Forbidden. Unresolved inventory rows are `StatusNeutral`, which `visibleInventory` folds into "+ N ready", so objects kute has no reading on are labelled ready. | Gate `gone` on `tui.KindsSynced(lister, ns, kind)`, and show `KindsError` when non-nil. While inventory caches are filling, show the loading note and `ScheduleCacheSyncRetry`. Don't count `·` rows as ready in the fold. |
| M8 | medium | `kube/timeline.go:284-296`; `timeline/load.go:440-466`; `tui/fluxsubjects.go:45-61` | `FluxCommitSubjects` pairs every "stored artifact for commit '…'" event with the source's **current** artifact revision. Suppose a source fetched two commits within the ~1 h Event TTL. Both subjects then map to the head revision, and the last-iterated one wins, which can be the older commit. The session cache then retains that wrong pairing. That is a fabricated subject on §32a's row, the exact claim `fluxsubjects.go`'s "content-addressed, can't collide" reasoning relies on not happening. Traced by code. | Prefer the event's own `source.toolkit.fluxcd.io/revision` annotation, which `FluxEventRevision` already reads. Source-controller stamps it on NewArtifact events (**unconfirmed** on a live cluster; the fake event lacks it). Fall back to the current-revision pairing only for the newest such event per source. |
| M9 | medium | `browse/flux.go:36-46`; `app/app.go:1023-1037` | Browse's `↵` sends **every** Flux kind to §31a: GitRepository, HelmRepository, OCIRepository, Alert, Provider, ImagePolicy and so on. §30a says detail is 14d, and §30b deliberately sends sources to 14d ("the inventory screen has no question to answer about one"). On a source, 31a shows no chain or inventory, no CONDITIONS and no EVENTS. A failing source gets only a `RECONCILE FAILED` card. | Gate `openSelectedFluxDetail` on `desc.APIGroup` being kustomize or helm, the same split `fluxtree.fluxKinds` uses. Let everything else fall through to `crddetail.go`'s 14d. |
| M10 | medium | `timeline/model.go:238-247`; `events/model.go:197-206`; `kube/events.go:13-28` | 9b/16a `↵` builds `GotoResourceMsg` from the event's `Object` "HelmRelease/x". For every helm-controller event, including §32a's ◆ revision rows, this lands on §18a's Helm list rather than the Flux HelmRelease. `kube.Event` drops `involvedObject.apiVersion`, so the mapping can't be made. | Keep `involvedObject.apiVersion` on `kube.Event`, and resolve `(group, Kind)` to a registry kind through the same helper H1 needs. |
| M11 | medium | `fluxdetail/load.go:372-383`; `model.go:88-92` | §31a's drill-through ("follows the failing health check to the Deployment to its pod and prints the real reason (`exit 137, OOMKilled`)") is not implemented. `Detail` is the replica cell (`3/4`). The `failure` doc comment still promises "CrashLoopBackOff — exit 137, OOMKilled". | Resolve the culprit workload's worst pod (an already-cached Pod read in its namespace) and reuse poddetail's termination-reason derivation. Otherwise, correct the spec and the comment. |
| L1 | low | `kube/flux.go:9-15` | cli-utils encodes `:` in RBAC names as `__` (`_system__aggregate-to-view_rbac.authorization.k8s.io_ClusterRole`). The 4-part split rejects such an entry. **(proved)** It is silently dropped from 31a's inventory and 30b's "applies N objects". | For RBAC GroupKinds, split on the last two separators, and decode `__` back to `:` in the name. |
| L2 | low | `resources/flux.go:193-194` | Flux's ordinary in-progress state is `Ready=Unknown, reason=Progressing` + `Reconciling=True`. It renders `▲ Unknown`, not §30a's `◌`. Only `Ready=False`+Reconciling gets `◌`. | Check `Reconciling=True` before the `hasReady` (Unknown) case. |
| L3 | low | `actions/controller.go:516-527`; `fluxtree/update.go:260-286` | 30b's with-source `r` aborts the reconciler's own annotate when the source annotate fails. That happens on a missing source (the broken chain the tree exists to show) or when there's no RBAC in the source namespace. | Skip with-source when the parent row is `missing`. Run the reconciler annotate even if the source one fails, and report both results. |
| L4 | low | `browse/flux.go:68-72`; `kube/mutate.go:731-734` | The doc comment says the timestamp is "taken once so the will-run line names the same instant the patch carries". The patch stamps its own `time.Now()`. | Pass the instant through `TaskScope` to `RequestFluxReconcile`, or fix the comment. |
| L5 | low | `fluxdetail/model.go:183-189`; `update.go:38-46` | The 1 Hz tick and the 10 Hz spinner run for as long as the screen is open, in every state. While another screen (`y`, `↵`) is on top, the package-local `tickMsg` reaches that task and the chain dies. `Reload()` doesn't restart it, so after `esc` the "Xm ago · retry in" text is frozen. | Tick only while `fail != nil`, spin only while loading, and restart the tick from `Reload()`. |
| L6 | low | `fluxdetail/update.go:49-53` | `actions.ResultMsg` never reaches `m.actions.HandleResult`. Success leaves the will-run command as the "result". A `Begin` refusal (`c.fail`) sets a message the screen never reads. | Mirror fluxtree's `handleResult`. |
| L7 | low | `fluxtree/update.go:117-128`, `:151-176` | Watch-triggered loads carry no epoch, so an older `loadedMsg` landing late overwrites a newer join. The second `KindsSynced` check (`:173-176`) is unreachable. | Stamp loads with an epoch and drop stale ones. Delete the dead branch. |
| L8 | low | `timeline/load.go:433-440` | The comment says `sourceRevisionOf` reads "only kinds already in the cache … rather than starting one". `ListRaw` starts the informer. It's narrow (source kinds named by matching events), but the stated contract is false. | Fix the comment, or check `KindSynced` first. |
| L9 | low | `resources/flux.go:250-370` vs `fluxdetail/load.go:403-453`, `fluxtree/load.go:354-379`, `kube/timeline.go:397-420` | Duplicated logic: `shortRevision` is byte-identical to `kube.ShortFluxRevision`. `shortSourceKind`, `fluxCondition` and `shortAge` each exist two or three times. The sourceRef path list (`sourceRef` / `chart.spec.sourceRef` / `chartRef`) exists three times, and that is how M5's copy lost the namespace. | Move a `kube.FluxSourceRef(obj) (kind, ns, name)` and the condition reader into `kube`, and have all three screens call them. |
| L10 | low | `fluxdetail/view.go:333-360` | `pad`/`wrap` slice by bytes. A condition message containing `…` or curly quotes can be cut mid-rune at the wrap point. | Use `ansi.Truncate`/`lipgloss` width helpers, as other screens do. |
| L11 | low | `kube/kinds.go:110-115` | Non-substituted Flux kinds print as bare `bucket`/`provider` in will-run lines and in the `kubectl edit` handoff. On a Crossplane cluster (`buckets.s3.aws…`, `providers.pkg.crossplane.io`) kubectl may resolve to the other group. **Unconfirmed.** | For every discovered kind (not just substituted ones), have `ResourceArg` emit `<plural>.<group>`. |
| L12 | low | `browse/update.go:765-767`; `browse/flux.go:29` | `o` (navigation only) is gated on `fluxVerbsApply()`, which requires a wired mutator. In read-only wiring the jump disappears. | Gate `o` on `desc.Flux && state == Ready` alone. |

## Details (high / medium)

### H1 — 31a's inventory reads the wrong kinds, cluster-wide

`ParseFluxInventoryID` returns `ResourceKind(parts[3])`, the bare API Kind, and discards
`parts[2]`, the group. `buildInventory` then does `lister.ListRaw(ctx, kind, "")` for each
distinct kind.

The proof test's recording lister saw exactly one call, `HelmRelease@`, for a Kustomization whose
inventory holds `flux-system_podinfo_helm.toolkit.fluxcd.io_HelmRelease`. In the app the lister
is `helmAwareLister`, whose `ListRaw` intercepts `kube.KindHelmRelease` and does three things:

1. It starts the cluster-wide `type=helm.sh/release.v1` Secret informer. CLAUDE.md measured
   that at 8.19 MB on a real cluster, and §5.5 split it per namespace precisely to avoid this.
2. It gunzips and decodes every release.
3. It runs `UnsettledWorkloads` cluster-wide.

All of that happens to render a row whose readiness can't match anyway: the Helm release
object's name is the Flux release's `releaseName` (often `<targetNamespace>-<name>`), and its
namespace is the target namespace.

The same loop lists `Secret`, `ConfigMap`, `ServiceAccount`, `ClusterRole` and others
cluster-wide. `objectReadiness` returns `"–", StatusNeutral` for all of them, so every one of
those informers is pure cost. Inventories with SOPS Secrets are the norm. Opening 31a on
`flux-system` therefore starts the cluster-wide Secret cache, which itself contains every Helm
release Secret.

Navigation is also wrong. Inventory `↵` dispatches `GotoResourceMsg{Kind: it.Kind}`, so a
HelmRelease entry opens §18a.

Fix shape:

- Make `ParseFluxInventoryID` return the group.
- Add `kube.RegistryKindFor(group, kind)` that consults `substitutedKinds` in reverse. M10
  needs the same helper.
- Restrict reads to kinds with a readiness signal, scoped to the namespaces the refs name.
- Pin it with a fake-clientset action test asserting no `secrets` LIST at cluster scope.

### M1 — `dependsOn` resolves through the synthetic Helm kind

`dependencySuspended(ctx, lister, u, namespace, dn)` lists `kube.ResourceKind(u.GetKind())`.
The proof shows that `apps/app` with `dependsOn: [{name: db, namespace: data}]` produces the
single call `HelmRelease@apps`, with `Suspended:false`. Two things are wrong:

- **Wrong kind.** Since the call returns §18a objects, `findObject` (which requires
  `*unstructured.Unstructured`) can't match.
- **Wrong namespace.** Flux allows cross-namespace `dependsOn`, but the entry's `namespace` is
  never read.

The registry kind is already on the model (`m.kind`). Pass it in, and read the entry's
namespace.

### M2 — 31a never hears about outages

`fluxdetail/update.go:36` handles `kube.ConnState`. Nothing sends that type: `kube/health.go:51`
defines `ConnStateMsg`, and the root forwards that (`tui/model.go:616`). fluxtree gets this
right (`fluxtree/update.go:40`).

The proof sends `kube.ConnStateMsg{Phase: reconnecting}` and reads back `offline() == false`.
The consequences:

- The header's `LiveConnBadge` keeps saying "connected".
- `r`/`s` stay advertised and live.
- The controller's offline refusal is never armed, because `SetOffline` is never called, so the
  write fails late with a transport error.

`certchain` has the same copy of the bug.

### M3 / M4 — the failure card on suspended and long-failing objects

The proof builds a suspended Kustomization (`spec.suspend: true`) with
`Ready=False, HealthCheckFailed` that transitioned 3 h ago. It renders:

```
RECONCILE FAILED · HealthCheckFailed · 3h ago · retry in now
boom
```

That shows two defects:

- **Suspended still shows a failure card.** §30a: "Suspended (`‖`) outranks everything for the
  frozen-conditions reason". The list says `‖ suspended`, and the detail says the opposite.
  `suspendedCard` (with its drift note) is unreachable whenever Ready is False, which is the
  common reason someone suspended it.
- **The countdown is anchored to the wrong time.** It uses `lastTransitionTime`, which doesn't
  move while Ready stays False, so every failure older than `retryInterval` reads `retry in now`
  indefinitely.

### M5 — `o` from the list ignores `sourceRef.namespace`

`resources.fluxSource` renders `helm/bitnami` and never includes the ref's namespace.
`openSelectedFluxSource` re-parses that cell and uses `row.Namespace`. For a HelmRelease in
`apps` pointing at `flux-system/bitnami`, `o` navigates to `HelmRepository apps/bitnami`.

e9a39cd fixed the identical issue in 31a's `buildChain` (`" in " + ns`) and 30b's `sourceRefOf`,
but not here. The three-way duplication in L9 is the root cause.

### M6 — one forbidden kind takes 30b down

`applyLoaded` calls `tui.KindsError(m.lister, "", msg.kinds...)` across *all* source and
reconciler kinds. On error it sets `TaskStateError` with "couldn't read Flux objects: … —
retrying" and returns `nil`.

Retries only come from `ResourceChangedMsg` for a Flux kind. A Forbidden kind never emits one,
and on a quiet cluster neither do the others. Least-privilege tenants commonly can list
Kustomizations and GitRepositories but not Buckets or image-automation kinds. Under
`--namespace-scoped`, the tree's explicit `""` reads are cluster-wide by definition (CLAUDE.md),
which is precisely what such a user can't list.

### M7 — no sync gate on 31a

`load()` treats "object not found in `ListRaw(kind, ns)`" as `gone` and renders
"\<name\> no longer exists". The new pod-detail link (1b4eda0) opens 31a for a Kustomization in
`flux-system` that the user may never have listed, so its informer starts inside this very
`ListRaw` and returns empty. In scoped mode, `flux-system`'s per-namespace cache is certainly
cold.

The object's first Add event re-runs `load` (`reloadsOn(m.kind)`). The transient claim breaks the
"an empty state is a claim about the cluster" invariant, and with Forbidden it never clears.
Inventory rows behave the same way: they stay `·`/Neutral until their caches fill, and the fold
counts them as "+ N ready".

### M8 — commit subjects can attach to the wrong revision

`FluxCommitSubjects(events, revisionOf)` sets `out[revisionOf(e.Object)] = subject` for every
stored-artifact event. `revisionOf` (`timeline/load.go:440`) returns the source object's
*current* `status.artifact.revision`.

Take two commits within the TTL: A at 10:00, B at 10:20. Both events key on B's revision, so
B's ◆ row can show A's subject. `RetainFluxCommitSubjects` then keeps that pairing for the rest
of the session.

The event's own revision is available. `fluxRevisionAnnotations` already lists
`source.toolkit.fluxcd.io/revision`, and source-controller annotates its NewArtifact events with
it. That last point is **unconfirmed** here, because the fake fixture's `git-artifact-1` event
carries no annotation, so the existing test can't distinguish the two pairings.

### M9 — browse `↵` on a source opens the reconciler screen

`openSelectedFluxDetail` checks only `m.desc.Flux`, and `openFluxDetailFunc` builds `fluxdetail`
for whatever kind it's given. On a GitRepository, 31a shows:

- no chain (no `sourceRef`) and no inventory;
- the pill reads `FLUX`;
- a failing source gets a `RECONCILE FAILED` card with a fabricated retry countdown.

14d would have shown CONDITIONS verbatim and EVENTS, which is what §30a specifies ("Detail is
14d, unchanged") and what 30b already does for the same objects.

### M10 — event jumps lose the Flux HelmRelease

`splitObject("HelmRelease/podinfo")` yields kind `HelmRelease`, which is
`kube.KindHelmRelease`. Both 9b and 16a hand that to `tui.GotoResource`. Every helm-controller
event, including the §32a ◆ "revision applied" row for a HelmRelease, therefore jumps to the
Helm-3 list, where the Flux object isn't listed.

CLAUDE.md requires `APIKind()` for registry→API comparisons. The reverse direction (API→registry)
has no helper at all, and `kube.Event` lacks the `apiVersion` needed to write one.

### M11 — §31a drill-through is half-built

The spec's "zero digs" line says to follow the health check to the pod and print
`exit 137, OOMKilled`. The code stops at the workload's replica count. Either finish it (one
cached Pod read in the culprit's namespace, reusing poddetail's reason derivation) or amend
§31a.

## Test gaps

- **Fake provider completeness.** `demoFluxFixtures` seeds only Kustomization, HelmRelease,
  GitRepository and HelmRepository. It has:
  - no HelmChart, OCIRepository or Bucket, so the chartRef hop from e9a39cd is unreachable in
    `--demo`;
  - no cross-namespace sourceRef and no `dependsOn`;
  - no inventory entry for a HelmRelease, Secret, or colon-named ClusterRole;
  - no source-controller event carrying `source.toolkit.fluxcd.io/revision`.

  Each of H1, M1, M5, M8 and L1 would have been caught by one of these.
- **No recorded-actions test for 31a.** Nothing asserts which kinds and namespaces `fluxdetail`
  lists. A `lazy_test.go`-style test should fail on `ListRaw(HelmRelease, "")` and on a
  cluster-wide `secrets` LIST.
- **Message types.** No test sends `kube.ConnStateMsg` to fluxdetail (M2), or a
  suspended+failed object to the failure card (M3).
- **30b gaps:** no `KindsError` test for a single forbidden kind (M6), and no stale-load ordering
  test (L7).
- **Browse `o`** has no cross-namespace source case (M5).
- **9b/16a `↵`** has no test on a Flux HelmRelease event (M10).
- **E2E** (`test/e2e/flux_test.go`) covers only Kustomization list, detail, suspend/reconcile and
  tree nesting. Uncovered:
  - Flux HelmRelease suspend/reconcile (the substituted kind's real write path);
  - 30b's with-source `r` (two annotates);
  - 31a's own `r`/`s`;
  - the pod-detail reconciler link from 1b4eda0.
- **No golden for the suspended 31a card.** Both themes need one once M3 is fixed.

## Quick wins

1. M2: change `case kube.ConnState:` to `case kube.ConnStateMsg:` (plus `SetOffline`). Apply the
   same one-line fix in certchain.
2. M3: check `m.suspended` first in `failureCard`, or return `nil` from `buildFailure` when
   suspended.
3. M9: gate `openSelectedFluxDetail` on the kustomize/helm API groups.
4. M1: use `m.kind` and `dependsOn[].namespace` in `dependencySuspended`.
5. L2: test `Reconciling=True` before the `Ready=Unknown` branch.
6. L12: decouple `o` from the mutator gate.
7. L7: delete the unreachable second `KindsSynced` block.
8. L4: fix the comment, or thread the timestamp through.
