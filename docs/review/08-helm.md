# 08 — Helm

Scope: `internal/kube/{helm,helm_inventory,helm_pods,helm_workloads}.go` (and their tests and
fuzz targets), `kube.Mutator.HelmRollback` (`mutate.go`), `internal/helmrepo`,
`tasks/{helmdetail,helmhistory}`, `browse/helm.go` (plus the Helm branches in browse's
`model.go`/`update.go`/`auxkinds.go`), app's `helmAwareLister`, the fake provider's Helm fixtures
and `HelmRollback`, and `test/e2e/mutation_helm_test.go`. Spec: §18a in
`docs/design/README.md` and `docs/lazy-informers.md` §5.5.

Verification: `go vet` and `go test` are clean for `kube`, `helmrepo`, `helmdetail`,
`helmhistory`, `browse` and `app`. Every finding below was traced to the `file:line` given.
Findings marked **(proved)** were reproduced with a throwaway `go test -overlay` test or benchmark.
No repo file was changed. Anything not observed at runtime is marked **unconfirmed**.

Already covered elsewhere, so not repeated here: 03 H4 (an empty Helm list's 10c hints start one
release informer per namespace), 07 H1/M1/M10 (Flux reading the synthetic Helm kind), and 01 L9
(the stale "most recent read" wording in §5.5).

## Summary

The decode layer is careful. It has these properties:

- The gzip read is bounded (`maxHelmReleasePayload`) and fuzzed.
- The per-namespace, `type=`-field-selected release informer matches §5.5, and its sync, error
  and forbidden seams are forwarded through both lister decorators with compile-time assertions.
- History narrows to one release before decoding.
- The release-to-workload link uses the manifest, not labels.
- `helmrepo` never touches the network, and never runs a `helm` binary on a read path.

The defects sit at the edges: the one shell-out, the cost of re-decoding, and the newer 18a
diagnostics screen.

1. **`helm rollback` can run against the wrong cluster.** It is invoked without
   `--kube-context` or `--kubeconfig`, so it acts on the kubeconfig file's current-context, not
   kute's. 04 H1 found the same gap in every kubectl shell-out; Helm's is a mutation behind a
   PROD gate that checks kute's context, not helm's.
2. **The Helm list decodes every revision of every release, twice per load.** Each decode is
   base64, gunzip, JSON, and a YAML re-marshal. **(proved)** 30 releases × 10 revisions with 100 KB
   manifests cost 114 ms and 155 MB per decode, and a load does two. A label pre-filter that
   decodes only the newest revision costs 12 ms and 15 MB. The list also reloads on every
   Deployment, StatefulSet and DaemonSet event (`auxKinds`).
3. **Helm rollback in PROD is a plain y/N.** §18a says "Rollback inherits 8b friction", and the
   sibling `rollout-undo` gets the type-the-name modal. Helm's rollback is explicitly excluded.
4. **`p` then `esc` silently moves the user to all namespaces.** **(proved)** That reopens the Helm
   list cluster-wide, which is the exact read §5.5 exists to prevent.
5. **18a's diagnostics screen re-runs its whole load on every Event**, with no debounce. It also
   lists every kind a chart declares, Secrets included, through cluster-wide caches.

Counts: **2 high, 4 medium, 12 low.**

## Findings

| # | Sev | Location | Issue | Suggestion |
|---|---|---|---|---|
| H1 | high | `kube/helm.go:468-477`; `kube/mutate.go:715-717`; `kube/helm.go:457-462` (will-run string); context source `app/session.go:105-125`, `kube/client.go:60-71` | `HelmRollback` runs `helm rollback <name> [rev] -n <ns>` with no `--kube-context` and no `--kubeconfig`. kute selects its context in-process and never writes it to the kubeconfig. Its sources are `--context`, `RecentContexts[0]`, or a 7a switch. kute also has a `--kubeconfig` flag that `helm` never sees. Helm therefore rolls back the same-named release on whatever cluster the file's current-context names. Release names like `ingress-nginx` and `cert-manager` exist in every cluster. The PROD tier is decided from kute's context (`isProd()` → `Session.Location.Context`), so a non-prod y/N can roll back a prod release. The "will run" line hides it, because it omits the flags too. Traced by code; not run with mismatched contexts. Same root cause as 04 H1, but this call site isn't listed there. | Pass `--kube-context <Session.Location.Context>` and, when set, `--kubeconfig <kube.explicitKubeconfigPath()>`. Thread both through `Mutator.HelmRollback`, or give `Cluster` the target it already knows (`c.Context.ContextName`). Render both in `HelmRollbackCommandString`. Add an argv unit test, and an e2e test where kute's context differs from the file's. |
| H2 | high | `app/app.go:94-118`; `browse/model.go:1049` (`resources.List`) and `:1077-1078` → `:1472-1484` (`helmReleasesByName`); `browse/auxkinds.go:64-72` | `helmAwareLister.ListRaw(KindHelmRelease)` decodes **every** revision Secret before `LatestHelmReleases` throws 9 in 10 away. Helm keeps 10 revisions by default. Browse's `load()` calls it **twice**: once for rows, and again in `helmReleasesByName`. Each call also re-runs `UnsettledWorkloads` (three workload lists). **(proved)** The proof showed 2 `ListRaw(HelmRelease@default)` per load. A benchmark of 30 releases × 10 revisions × 100 KB manifests measured 113.6 ms / 155 MB / 79k allocs per decode, against 12.4 ms / 15.5 MB when only the newest revision is decoded. Because the list reloads on every Deployment, StatefulSet and DaemonSet change in scope (`auxKinds`), a rollout anywhere in the namespace (or the cluster, in all-namespaces mode) costs about 230 ms of CPU and about 310 MB of garbage per reload. | Pre-select before decoding: group Secrets by the `name` label (falling back to the object name, as `HelmReleaseSecretsFor` does) and decode only the highest `version` label per release. Fall back to decoding all revisions only when labels are missing. Have `load()` derive `helmReleases` from the same `ListRaw` result the rows were projected from: return the objects from `resources.List`, or project in-branch as the CronJob path does. Add a benchmark next to `BenchmarkPodLogsRender`. |
| M1 | medium | `actions/controller.go:225-254` (`RequiresTypedName` excludes `"rollback"`); `verbs/verbs.go:446-454`; `browse/helm.go:209-224`; `helmhistory/update.go:149-157`, `:201-215` | §18a: "Rollback inherits 8b friction". In 8b, PROD friction is the type-the-name modal. `TierFor` does escalate Rollback to `TierModal`, but `Confirm` skips the name check for verb `"rollback"`, and helmhistory's confirm router only knows `y`/`n`/`esc`. A Helm rollback in PROD is therefore one `y`. Its sibling, 16b's `rollout-undo` (a less sweeping change: one Deployment's template), was deliberately given the type-the-name modal. The comment at `verbs.go:455-460` records the asymmetry as intentional, but the spec says otherwise. | Add `"rollback"` to `RequiresTypedName`. Route `TypeRune`/`Backspace`/`enter`/paste in helmhistory (via `tui.RoutePaste`), and in browse's TierModal path for Helm, the way delete does. Render `components.TypeNameModal` with the will-run line. If the y/N is actually wanted, change §18a's wording instead. |
| M2 | medium | `browse/helm.go:127-133`; `browse/deployments.go:94-97`; `browse/model.go:1302-1318` | `openReleaseObjects` sets `m.namespace = ""` **and** `m.session.Location.Namespace = ""` when any manifest pod-source names another namespace. `backToOrigin` restores only the kind. **(proved)** After `p` then `esc`, the model is back on Helm Releases with `m.namespace=""` and `session ns=""`. Three consequences: the list re-reads through the cluster-wide release-Secret informer (§5.5's 8 MB read that never syncs over a VPN); the user's namespace is lost for the rest of the session (and persisted through `PerContext`; **unconfirmed** whether it is written on this path); and `pendingSelect` by bare name can now land on a same-named release in another namespace. | Remember the pre-widen namespace in `originNamespace` and restore it in `backToOrigin`, or don't widen at all: list Pods in each source's own namespace (one read per distinct namespace) without touching `Location`. |
| M3 | medium | `helmdetail/model.go:347-351`, `:159`, `:143`, `:180-198` | `Update` calls `m.load()` immediately on every `ResourceChangedMsg` whose kind is relevant. `relevant` always includes `KindEvent` and `KindHelmRelease`, plus every declared kind. There is no debounce. Each load does all of the following: a full `ListRaw(KindHelmRelease, ns)` (H2's decode of every revision in the namespace, plus `UnsettledWorkloads`), a `ListRaw` per declared (kind, ns), and a `NamespaceEvents` scan. Events are the chattiest stream there is, so 18a's detail screen re-decodes the namespace's releases on every event flush. Epoch bumps also discard the in-flight load each time, so under a steady stream the screen can fail to land a fresh load at all (compare 03 H1). | Use browse's pattern: bump `reloadEpoch`, schedule a 250 ms `reloadDueMsg`, and only load on its arrival. Read the one release through `ListHelmReleaseSecrets` + `HelmReleaseSecretsFor`, as helmhistory does, instead of the decorated full-list `ListRaw`. |
| M4 | medium | `helmdetail/model.go:156-188` | The screen lists every registry kind the manifest and hooks declare, at the ref's namespace. In default mode every typed cache is cluster-wide, so a chart that ships a Secret (most do) starts the **shared cluster-wide Secret informer**. That is the 12.3 MB read §5.2 and `auxkinds.go:76-85` go out of their way to avoid, and it is paid just to print "present". The same happens for ConfigMap, ServiceAccount, Role/ClusterRole, and so on. The design's "only kinds actually named by this release" is honoured literally, but for a release that names ten kinds the screen costs ten cluster-wide LISTs. | For kinds whose informer isn't already started, answer presence with a metadata-only GET per object, or one metadata LIST per (kind, ns) via the metadata client `CountLive` already uses. Keep the informer read for kinds that are already warm. Secrets at minimum should never take the shared cache. Add a recorded-actions test, as `helm_secrets_test.go` does. |
| L1 | low | `helmhistory/view.go:337` | `railBody` calls `time.Since(rev.Updated)`. Render reads the clock, which breaks the pure-render invariant. The model already carries `m.now` for exactly this purpose (`model.go:194-198`). | Use `m.now.Sub(rev.Updated)`, and tick `now` on a slow timer while ready. |
| L2 | low | `browse/update.go:789-793`; `browse/helm.go:210-211` | `R` on the Helm list has no `m.state == Ready` gate and no check that the row is in `m.helmReleases`. During a cached-view load, or before `helmReleases` lands, `release` is the zero value, so `toRevision = -1`: the label reads "Rollback x to revision -1?" while the command runs helm's default. On revision 1 the label says "revision 0" and helm fails with "release has no 0 version". | Gate on Ready, on the map hit, and on `Revision > 1`. Otherwise show an inline "nothing to roll back to". |
| L3 | low | `helmhistory/update.go:139-144`; `browse/helm.go:215` | `R` is offered on the current revision (index 0), where helm creates a new revision identical to the live one. Neither confirm says what the target *is*. `Revision-1` is often the failed revision that prompted the first rollback, and helm's default picks it blindly. | Disable `R` on index 0. Include the target's status in the label ("to revision 4 · failed"), and warn when it isn't `superseded`/`deployed`. |
| L4 | low | `helmrepo/helmrepo.go:161-169`, `:272-288`; `helmrepo/cache.go:451-472` | The comment says yaml.v3 "never materializes" an index's unread fields. In fact yaml.v3 builds the whole node tree before decoding. **(proved)** One 6.3 MB index costs 126 ms, 110 MB and 2.2 M allocs. This happens inside `Cache.Index()` **under the mutex**, on the first Helm-list load and after every `helm repo update`, so helmhistory's load blocks behind it. It also ignores the load's context and timeout. | Stream the index with a `yaml.Decoder` token walk (or `yaml.Node` with early skip), or parse off the hot path: kick a background refresh and serve the previous index meanwhile. Fix the comment. |
| L5 | low | `kube/helm.go:592-641`, `:647-662` | Per-namespace release informers are never stopped. Every namespace visited on the Helm list (and every `h`/`↵` into one) keeps a watch and a cache of full gzipped revisions until the context switches. A namespaced read also starts its own informer even when the cluster-wide `""` cache is already synced and holds the same objects, which means a second LIST of data already in memory. | Serve a namespaced read from a synced `""` cache (`listNamespaced` already filters). Optionally stop per-namespace informers that are idle for N minutes. Document the accumulation in §5.5. |
| L6 | low | `helmdetail/model.go:381-407` | Keys are hand-wired string literals (`"enter"`, `"e"`, `"y"`) while the keybar renders `verbs.Open/Events/YAML.Hint()`. The two can drift. This breaks the verb-registry invariant in letter. | Switch on `verbs.Events.Key`, `verbs.YAML.Key`, and so on. |
| L7 | low | `helmdetail/model.go:365-367`, `:373-375` | A reload that finds a cache unsynced flips a ready screen back to `loading`, but doesn't restart the spinner tick chain, which stopped when the screen became ready. The spinner freezes. helmhistory handles exactly this case (`update.go:92-100`). The flip also replaces the populated table with a bare loading body. | Restart `m.spinner.Tick` when `wasReady`. Better, keep rendering the rows with a "refreshing" strip, since the rows aren't wrong. |
| L8 | low | `helmdetail/model.go:192`, `:295`, `:313`, `:326-329` | Evidence comes from `NamespaceEvents(release.Namespace)` and matches on `Kind/Name` only. Hooks or objects rendered into another namespace always read "not present". `hookState` prints `HH:MM` with no date, so a hook stuck since three days ago reads "Running · since 14:02". | Fetch events per distinct ref namespace. Render hook times as relative ages from `m.now`. |
| L9 | low | `kube/helm.go:408`; `helmhistory/model.go:273`; `helmhistory/view.go:412-415` | Stale comments. "The release cache is cluster-wide" (both) predates §5.5. `rollbackCommand` claims to avoid importing `kube`, but the package already imports it (`model.go:143`), so the duplicate of `kube.HelmRollbackCommandString` has no reason to exist and will drift once H1 adds flags. | Fix the comments, and call `kube.HelmRollbackCommandString`. |
| L10 | low | `kube/fake/fake.go:997-1040` | The fake's rollback differs from real helm in three ways. It leaves `StatusReason` empty (helm writes "Rollback to N"). It marks only the current revision superseded (helm supersedes every `deployed` one). It doesn't apply the target's manifest, so demo workloads never change. Demo and tests can't show the description helm puts on the new revision. | Set `StatusReason = fmt.Sprintf("Rollback to %d", target.Revision)`, and supersede every deployed revision. |
| L11 | low | `kube/helm.go:563`, `:592-641`; `app/app.go:266-271` | Only the default `secret` storage driver is read. A cluster using `HELM_DRIVER=configmap` (labelled `owner=helm` ConfigMaps) or `sql` shows an empty list. Once the cache syncs, 10c's empty state asserts there are no releases. | When the release cache is settled and empty, note "secret driver only" in the empty state. Optionally probe for `owner=helm` ConfigMaps with `CountLive` and say so. |
| L12 | low | `helmdetail/view.go:95-99` | `Body` wraps and styles **every** diagnostic row on each frame, then renders only the visible window. A chart with several hundred objects pays O(n) lipgloss work on every `j/k` (compare 03 H5). | Wrap only from `start` until the budget is filled. Cache row heights per width. |

## Details: high

### H1. `helm rollback` targets the kubeconfig's context, not kute's

`kube/helm.go:472-477` builds the argv `rollback <name> [rev] -n <ns>` and runs it with
`exec.CommandContext`. There is no `--kube-context` and no `--kubeconfig`, and kute never exports
`HELM_KUBECONTEXT` (grep: no hits). kute's own context comes from these sources:

- `--context`, or `RecentContexts[0]` (`app/session.go:113-125`), both applied through
  `configOverrides.CurrentContext` (`kube/client.go:189`), never written to disk.
- A 7a switch, which rebuilds the in-process client.
- `--kubeconfig`, which is a package var helm can't see (`kube/client.go:36-71`).

Helm reads `$KUBECONFIG` (or `~/.kube/config`) and that file's `current-context`. Whenever the two
differ, the confirm names one cluster and helm acts on another. Release names repeat across
clusters by design: `ingress-nginx`, `cert-manager`, `kube-prometheus-stack`. Helm will therefore
usually *succeed*, rolling back a release the user never looked at.

PROD tiering makes it worse. `isProd()` reads `Session.Location.Context`
(`helmhistory/model.go:313-318`, browse's `delete.go`). A user browsing a non-prod context while
the file still points at prod gets the inline y/N and rolls back prod.

The fix is mechanical, because `Cluster` already knows its context name (`c.Context.ContextName`),
and `explicitKubeconfigPath()` returns the flag or env path. Prepend
`--kube-context <name>` (and `--kubeconfig <path>` when non-empty) in `HelmRollback`, and the same
in `HelmRollbackCommandString`, so the will-run line shows the target. The e2e test
(`mutation_helm_test.go`) launches against the file's current-context, so it can't see this. It
needs a variant that launches with `--context` pointing at a second context entry, aimed at the
same kind cluster but with a different name, and asserts on the argv or on the cluster that changed.

### H2. Every Helm list load decodes all revisions, twice

`helmAwareLister.ListRaw` (`app/app.go:98-102`) does
`LatestHelmReleases(DecodeHelmReleases(secrets))`. Every revision Secret is base64-decoded,
gunzipped (up to 32 MiB each), JSON-unmarshalled, and its `config` map re-marshalled to YAML.
Only then are all but the newest discarded. Helm labels every revision Secret with `name` and
`version` (`EncodeHelmReleaseSecret` reproduces this at `helm.go:547-552`), so the newest revision
per release can be chosen from metadata alone.

Browse then asks twice per load:

1. `resources.List` → `ListRaw` for the rows (`browse/model.go:1049`).
2. `helmReleasesByName` → `ListRaw` again for the `v`/`h`/`R`/`↵`/`p` map (`model.go:1077-1078`,
   `:1472-1484`).

Each pass also re-runs `resources.UnsettledWorkloads`, which lists Deployments, StatefulSets and
DaemonSets.

Proof (throwaway overlays):

- A counting lister recorded `map[HelmRelease@default:2]` for one `m.load()()`.
- `BenchmarkProofDecodeAllRevisions` (30 releases × 10 revisions × 100 KB manifest, small values):
  **113.6 ms/op, 155 MB/op, 79,037 allocs/op**.
- `BenchmarkProofDecodeLatestOnly` (the same 30 newest revisions): **12.4 ms/op, 15.5 MB/op**.

`auxKinds[KindHelmRelease]` (`browse/auxkinds.go:64-72`) reloads the list on every workload change.
That is correct, because the `▸` glyph needs it. But during any rollout in scope, each debounced
flush costs about 230 ms of CPU and about 310 MB of allocation. In all-namespaces mode that applies
to every rollout in the cluster. helmdetail (M3) pays the same decode on every Event flush.

Fix order:

1. A label pre-filter in `DecodeHelmReleases`' caller (or a new `LatestHelmReleaseSecrets`), with
   the decode-everything path kept only for unlabelled Secrets.
2. A single `ListRaw` per load.
3. A benchmark to pin it.

## Details: medium

### M1. PROD Helm rollback is a one-key confirm

`verbs.Rollback` is `TierInline`, and `TierFor` lifts it to `TierModal` in PROD. But
`Controller.Confirm` only demands the typed name when `RequiresTypedName(verb)`, and `"rollback"`
is deliberately absent (`controller.go:241-253`). helmhistory's `updateConfirmKey` has no
type-ahead routing at all. The PROD path renders `components.ConfirmCard` (`helmhistory/view.go:252-286`,
`browse/nodes.go:315`).

§18a's "Rollback inherits 8b friction" and CLAUDE.md's tiering policy point the other way. So does
`rollout-undo`: the same `R` key and the same "go back a revision" semantics on a Deployment get the
type-the-name modal (`verbs.go:455-464`). Either align the code or change the spec. As it stands,
the more sweeping of the two rollbacks has the lighter gate.

### M2. `p` → `esc` leaves the session in all-namespaces mode

**(proved)** The overlay test opened `p` on a release in `apps` whose Deployment declares
`namespace: other`, then called `backToOrigin`. Result:
`kind=HelmRelease m.namespace="" session ns=""`.

`openReleaseObjects` (`browse/helm.go:127-133`) writes both the model's and the session's
namespace. `backToOrigin` (`deployments.go:94-97`) only switches kind. The Helm list reloads with
namespace `""`, which `ensureHelmSecrets("")` serves from the cluster-wide release informer. That
is the read §5.5 measured never syncing over a VPN. The fix is to save and restore the namespace,
or better, to not widen `Location` at all: list Pods per source namespace inside the release-pods
load.

### M3 and M4. The diagnostics screen re-runs everything on every Event and lists through cluster-wide caches

`helmdetail.Update` (`model.go:347-351`) has no debounce. `relevant` always contains `KindEvent`
(`:159`), so each Event flush re-runs the whole `load()`:

- the decorated `ListRaw(KindHelmRelease, ns)`, which is H2's full decode plus `UnsettledWorkloads`;
- a `ListRaw` per declared (kind, ns) (`:180-188`);
- `NamespaceEvents`.

Separately, those per-kind lists start each declared kind's informer, cluster-wide in default
mode. A chart's Secret alone pulls the shared Secret cache that the Helm list's own design avoids.

Suggested shape:

- Debounce the reload with an epoch and a `reloadDueMsg`.
- Read the one release via `ListHelmReleaseSecrets` + `HelmReleaseSecretsFor` (helmhistory's
  pattern).
- Answer "present?" for cold kinds with metadata-only reads.

## Test gaps

- **No argv test for `HelmRollback`.** `TestHelmRollbackCommandString` checks only the display
  string. An argv-builder test would pin the context and kubeconfig flags (H1). The e2e test runs
  on the file's current-context, so it can't catch H1.
- **No benchmark for the Helm list's decode path.** No test asserts one `ListRaw(KindHelmRelease)`
  per browse load, or that only the newest revision is decoded (H2).
- **No browse test for `p` → `esc` namespace restoration** with a cross-namespace manifest source
  (M2).
- **No PROD-tier tests** for browse's or helmhistory's rollback confirm, either the current y/N
  behaviour or the spec's type-the-name (M1).
- **helmdetail's lazy-read test** (`TestDetailReadsOnlyKindsDeclaredByRelease`) uses a recording
  lister. It can't see that a declared Secret starts the cluster-wide shared cache. A
  fake-clientset recorded-actions test in `kube` would (M4). There is also no test for reload
  debouncing on Event storms (M3).
- **The fake `HelmRollback` is untested** for `toRevision == 0` on revision 1, and the browse `R`
  path has no test for a missing map entry (L2).
- **`helmrepo` has no benchmark or size guard** for a large index (L4).

## Quick wins

1. Add `--kube-context` (+ `--kubeconfig`) to `HelmRollback` and to its will-run string (H1). This
   is a few lines and removes the worst failure mode.
2. Drop the second `ListRaw` in browse's Helm load: build `helmReleases` from the objects the rows
   came from (half of H2).
3. Restore the namespace in `backToOrigin` after `p` (M2).
4. Gate browse's `R` on Ready, on a map hit, and on `Revision > 1` (L2).
5. Replace `time.Since` in `helmhistory/view.go:337` with `m.now` (L1).
6. Switch helmdetail's key cases to `verbs.*.Key` (L6), and restart its spinner on re-entering
   loading (L7).
7. Fix the three stale comments, and delete helmhistory's duplicate `rollbackCommand` (L9).
