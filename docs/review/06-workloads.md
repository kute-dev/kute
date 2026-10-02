# 06 — Workloads: deployments, jobs, cronjobs

Scope:

- `tasks/{cronjobdetail,cronjobschedule,jobattempts}` (36e, 36d, 37b/37d).
- `browse/{deployments,jobs,job_actions,cronjob_actions,cronjob_bulk,scale,setimage,setimage_view,setresources,setresources_view}.go`, plus the CronJob/Job branches of `browse/{model,update}.go` (`loadCronJobRows`, `loadJobRows`, the one-second clock).
- `internal/resources/{rollout,jobs,jobattempts,cronjobs,cronjob_actions,job_actions}.go`.
- `internal/kube/{cronjob_annotations,cronjob_naming,capability}.go`, and the Job/CronJob writers in `kube/mutate.go` (`RetryJob`, `ReplaceJob`, `TriggerCronJob`, `SetCronJobSuspend`, `SetCronJobSchedule`).

Verification:

- `go vet` and `go test` pass for `cronjobdetail`, `cronjobschedule`, `jobattempts`, `browse` and `resources`.
- Findings marked **(proved)** were reproduced with throwaway `go test -overlay` tests. No repo file was changed.
- Anything else is "traced by code". Anything that depends on API-server behaviour this repo can't exercise without a cluster is marked **unconfirmed**.

Already covered elsewhere and not repeated here:

- 02 M9: run-now and rerun stamp the previous context's user after a 7a switch.
- 03 H2: `m.pods` is keyed by bare name. `jobLogsTarget`/`cronJobLogsTarget` (`browse/jobs.go:79`, `:183`) and the detail screens' `podsByName` inherit it in all-namespaces mode.
- 03 L1: the `V` alias for set-resources and the literal `+`/`-` scale keys.
- 03 L5: browse's `CapturingInput` omits the run-now and resume preflights.

## Summary

The data model is sound, and several hard rules are implemented carefully:

- **Associations.** CronJob↔Job association is by owner UID or Kute's own UID/name annotations, never a name prefix. Pods join by owner UID too.
- **Laziness.** The CronJob list reads Job once and aggregates client-side. Job is an aux kind, not eager.
- **Capability gating.** The tri-state timezone capability is a single cached `ServerVersion()` read. It is classified correctly (including a `+` suffix), and `tzEditable` follows the "already-populated field is evidence" rule.
- **Schedule apply.** The apply uses a `resourceVersion` precondition, keeps the screen open and offers a one-step undo.
- **Suspend marker.** The marker is stamped atomically against `generation+1` and is honoured only while the generation still matches.
- **Close-on-commit is fixed.** Set image and set resources no longer close on commit. Both now follow confirm → refresh → remain.

The defects cluster in four places:

1. **The one-second UI clock is fragile everywhere.**
   - In browse, the first watch event on CronJob, Job or Pod kills the chain, and nothing restarts it **(proved)**. NEXT, DURATION and the strip clock then freeze.
   - In cronjobdetail and jobattempts, the clock dies on any push/pop.
   - In all three screens `m.now` is also the `StagedAt` that Kute **writes into the cluster**: the `-manual-HHMM` Job name, `triggered-at` and `suspended-at`. A frozen clock therefore persists wrong timestamps. These become wrong suspended-for durations and missed-run counts later.
2. **25a's live usage never joins.** Both metrics providers key by `PodKey(ns, name)`. `containerUsage` looks up the bare pod name, so every USAGE cell reads "metrics unavailable" on real clusters and in `--demo` **(proved)**. The unit test seeds a bare-name map and hides it. Opening the panel also does a synchronous, timeout-less metrics-server List on the update loop.
3. **Destructive and naming paths of rerun.**
   - `ReplaceJob` deletes with default options. For `batch/v1` Jobs that orphans the pods, and the immediate same-name Create races the orphan finalizer **(unconfirmed at runtime; no e2e covers replace)**.
   - The ledger's create path proposes a name that already exists from the second rerun on.
4. **False claims and lost input.**
   - The CronJob list renders ACT 0 / "no retained runs" and a no-conflict run-now when Job is Forbidden **(proved)**.
   - The schedule editor eats `y`/`u`, so `@daily`, `@hourly`, `@yearly`, `sun`, `tue`, `jun`, `may`… cannot be typed **(proved)**.
   - Any key pressed while a detail screen is still loading pops it **(proved)**.

## Findings

| # | Sev | Location | Issue | Suggestion |
|---|---|---|---|---|
| H1 | high | `browse/update.go:118-124`, `:221-234`, `:457`, `:509`; `model.go:690-695`, `:1348-1375`; StagedAt users `cronjob_actions.go:75`, `:119`, `:334`, `:347`, `:500`, `jobs.go:285`, `job_actions.go:96` | The CronJob/Job one-second tick chain carries `m.reloadEpoch`, and every reload path bumps that epoch: a watch event for the kind or an aux kind, the KindsSynced gate, the empty-rows retry, and `Reload()` on pop. The in-flight tick is then dropped at `:222`, and only `Init`/`resetAndLoad` ever restart it. **(proved)** After one Pod/Job/CronJob event anywhere, NEXT, DURATION and the "clock HH:MM:SS" strip freeze. `m.now` (updated only by the tick, spinner and ConnState) also goes stale, and it feeds `ManualJobName`, `TriggeredAt` and `SuspendedAt`. | Give the tick its own `clockEpoch`, bumped only by `resetAndLoad`. Re-arm it from `Reload()` when `ticksCronJobClock()`. At staging, stamp `time.Now()` read in `Update` rather than `m.now`. |
| H2 | high | `browse/setresources.go:416-418` vs `kube/cluster.go:1288`, `kube/fake/fake.go:1279/1295`; test `golden_states_test.go:737` | `containerUsage` indexes `metrics[pod.Name]`. Both providers key `ContainerMetricsByNamespace` by `kube.PodKey(ns, name)`, so the join never hits. 25a's USAGE column is always "metrics unavailable", on real clusters and in demo. **(proved)** The golden test seeds a bare-name map, which is the shape no provider returns. | Look up `metrics[kube.PodKey(pod.Namespace, pod.Name)]`. Fix the test fixture to the provider shape. Add a test that feeds `fake.Cluster.ContainerMetricsByNamespace` output straight in. |
| H3 | high | `kube/mutate.go:496-520` (`ReplaceJob`), and `deleteResource` `:338-339` | Replace deletes with an empty `DeleteOptions`. For `batch/v1` Jobs the API server's default GC policy is orphan, so the old attempt pods keep running, now ownerless. The Job also gets an `orphan` finalizer, so the immediate `Create` under the same name can hit AlreadyExists while it still exists. That leaves the user with the original deleted and no replacement: the destructive half of the verb completes without the restorative half. **unconfirmed** at runtime (upstream `job` strategy behaviour); e2e covers only the create path (`test/e2e/mutation_batch_test.go`). The same orphaning applies to `ctrl-d` on a Job. | Delete with `PropagationPolicy: Background` (or Foreground) and a UID precondition. Then poll/watch until the name is gone, bounded, before `Create`. On a Create failure, report "original deleted, recreate failed" with the spec recoverable via `y`. Add an e2e for replace. |
| M1 | medium | `browse/model.go:1504-1512`; `cronjob_actions.go:83`; `update.go:449-458` | `loadCronJobRows` trusts only `ListRaw`'s error, but `Cluster.ListRaw` never errors for a Forbidden or stalled typed cache. cronjobdetail already compensates with `tui.KindsError` (`cronjobdetail/load.go:61-77`); browse doesn't. **(proved)** With Job forbidden, every row reads ACT `0` / LAST RUN `no retained runs`, and run-now's `unknownOverlap` is false, so the card takes the no-conflict path. Only a generic strip note hints otherwise. | Mirror cronjobdetail: `if jobsErr == nil { jobsErr = tui.KindsError(lister, ns, kube.KindJob) }` inside `loadCronJobRows`. |
| M2 | medium | `cronjobschedule/update.go:215-232` | With the schedule buffer focused, bare `y` copies and bare `u` undoes or is swallowed. The comment says no valid schedule contains either letter, but `parseSchedule` accepts `@daily`, `@hourly`, `@yearly`, `@monthly`, `@annually` and lowercase names (`sun`, `tue`, `thu`, `jun`, `jul`, `aug`, `may`). **(proved)** Typing `@daily` leaves `@dail`; `0 2 * * sun` leaves `0 2 * * sn`. Paste works, which hides it. | Move copy/undo to chords (`ctrl+y` is taken by full edit; e.g. `ctrl+u`/`alt+y`), or only honour them when the buffer is unchanged from `accepted`. Add a typed-macro test. |
| M3 | medium | `cronjobdetail/update.go:34-40`, `:75-77`; `jobattempts/update.go:28-35`, `:70-72`; StagedAt `cronjobdetail/cronjob_actions.go:88`, `:236`, `:324`, `jobattempts/rerun.go:72` | Both screens run an unconditional self-rescheduling `tickCmd`. The root routes messages only to the active task (`tui/model.go:870`), so pushing logs, YAML, events or the schedule editor drops the tick. `Reload()` on pop doesn't re-arm it. After any round-trip, relative ages and DURATION freeze, and `m.now` (also the persisted `StagedAt` for run-now, suspend and rerun) goes stale. | Re-arm `tickCmd()` from `Reload()`, guarded by an epoch so a double chain can't form. Same `time.Now()`-at-staging fix as H1. |
| M4 | medium | `browse/setresources.go:212-239`, `:672-690`; `update.go:96-103`, `:742`, `:797` | `beginSetResources` → `buildSetResourcesTarget` → `containerMetricsSnapshot` calls `ContainerMetricsByNamespace`, a live metrics-server List, **synchronously in `Update`**. It runs under `ClusterContext()`, which has no deadline, and no REST timeout is configured. `refreshSetResourcesTarget` repeats it on every watch event of the workload kind, cluster-wide, while a result line is showing. A slow or hung aggregated API freezes the whole TUI. | Start the panel with usage "loading…" and fetch metrics in a `tea.Cmd` with `context.WithTimeout`. Reuse the snapshot on refresh, since only the spec changed. |
| M5 | medium | `browse/setimage.go:758-805` (`crossWorkloadHistory`), called per open and per `tab` from `:214` | Set-image's FROM column lists Deployment, StatefulSet and DaemonSet (and CronJob) with namespace `""`. That is a breadth-first read: it starts informers for kinds that aren't on screen (STS/DS when editing a Deployment; all three apps kinds from the CronJob list). Under `--namespace-scoped` it starts the cluster-wide caches that mode exists to avoid. Namespace-restricted users get Forbidden reflectors. The first open renders against unsynced caches and never reloads. | Limit sightings to kinds whose cache is already started (as the goto palette does). Alternatively, read `CountLive`-style metadata on demand behind an explicit key. At minimum, use the screen's own namespace when scoped. |
| M6 | medium | `jobattempts/rerun.go:32` vs `browse/job_actions.go:44-51` | The ledger's create path seeds `taken` with only `m.name`. Once `x-rerun-1` exists, every further `R ↵` from 37b proposes `x-rerun-1` again, and `RetryJob`'s Create fails with AlreadyExists (it has no conflict mapping, unlike `TriggerCronJob`). Browse's copy checks every listed Job, so the two "identical" entry points already disagree. | Load the namespace's Job names in `load()` (already listed there) and seed `taken` from them. Better, share the staging code (see L6). |
| M7 | medium | `cronjobdetail/update.go:282-289`; `jobattempts/update.go:241-248` | The `!m.found` branch ("deleted · press any key to go back") doesn't check `state == Loading`. `found` is false on first open and after every `[`/`]`. **(proved)** Any key during the initial load, or a second `]` while the sibling loads, sends `BackMsg` and pops the screen. Holding `]` to walk siblings exits detail. | Gate that branch on `m.state == tui.TaskStateReady && !m.found`. While loading, ignore keys other than quit/esc. |
| L1 | low | `cronjobschedule/update.go:56-60`, `:146-170` | Any CronJob event cluster-wide triggers an immediate, undebounced `load()`. `applyLoaded` overwrites `m.accepted` from the cache, even right after a successful apply has set it from the API response. An unrelated event in that window reverts it to the pre-apply schedule/RV, which shows a phantom pending change and makes `u` 409. An external edit also updates `accepted` without `recomputeAll`, so WHAT CHANGES diffs against the old baseline. | Ignore cache snapshots whose RV equals the pre-commit RV. Call `recomputeAll` when `accepted` changes. Debounce like the other detail screens. |
| L2 | low | `browse/setresources.go:251`, `:663`, `:95` (view) | The §25a contract is still partly unmet. `selectSetResourcesContainer` resets `fieldIdx` to 0 after apply/refresh, but the spec says "the field you were on stays selected". The result reads `set resources: <container>` instead of `applied cpu.limit=…, mem.limit=…`. The strip says "from the metrics poll", but it is a separate one-shot read. | Preserve `fieldIdx` across rebuilds, render the changed fields in the result line, and fix the label. |
| L3 | low | `CLAUDE.md` (Task contract paragraph) | It says §25a "still closes the panel on apply … don't copy SetImage/SetResources". Both now remain open (`handleSetImageResult`, `handleSetResourcesResult`). The guidance is stale and steers authors away from code that now follows the contract. | Update the paragraph. |
| L4 | low | `browse/setresources.go:601` | The 25a dry-run uses `context.Background()`. It isn't a committed write, so the actions-controller exemption doesn't apply. It has no timeout and survives a context switch. | Capture `m.session.ClusterContext()` in the cmd prologue and add `WithTimeout`. |
| L5 | low | `jobattempts/keys.go:52-66`; `update.go:251`, `:283`, `:295` | The diff (`d`), failed-only (`f`), logs (`l`) and events (`e`) hints are literals, and `d`/`f` aren't registry verbs. That violates "every verb goes through the command registry". | Register `JobAttemptDiff`/`JobIndexFailedOnly` and render from `verbs.*`. |
| L6 | low | `browse/job_actions.go` ≡ `jobattempts/rerun.go`; `browse/cronjob_actions.go` ≡ `cronjobdetail/cronjob_actions.go` | §37c rerun and §36b/36c staging are duplicated across task packages and have already drifted. M6 is one drift. Browse also lacks the amber `ClassifyJobFailure` diagnostic that §37c says both entry points show. | Move the staging state, keybar and will-run rendering into a shared non-task package (e.g. `internal/tui/batchverbs`), which both task packages may import. |
| L7 | low | `resources/cronjob_actions.go:84-90` | When the missed count truncates at 100, the newest missed run is assumed to be `now`. With `startingDeadlineSeconds` shorter than the schedule period (hourly, deadline 60s, now = :30), resume says "Kubernetes may start one immediately" when none is eligible. | Compute the last firing ≤ now directly (walk back one period, or `NextCronRuns` from `now - period`). |
| L8 | low | `kube/mutate.go:466-480`; `resources/jobs.go:118` | A rerun clones `helm.sh/hook*` annotations. `jobSource` ranks a Helm hook above Kute's manual tag, so a rerun of a hook Job reads `helm/<release> <hook>`, not §37c's `manual · <creator>`. | Strip `helm.sh/hook*` and `meta.helm.sh/*` from the clone, or rank `AnnotationTriggeredBy` above the hook. |
| L9 | low | `resources/rollout.go:71-80` | StatefulSets/DaemonSets with `OnDelete` or a `partition` never reach `Updated >= want`, so 18a's ▸ glyph stays on forever. Cross-ref 08. | Treat `OnDelete` as settled once `observedGeneration` is current, and compare against `replicas - partition`. |
| L10 | low | `browse/setimage.go` `tagOf`/`imageRepo` | `tagOf("repo@sha256:…")` returns `latest`. A digest-pinned workload contributes a bogus `latest` sighting to other panels' history, and picking it sets `repo:latest`, which is a different image. | Return `""` or a digest marker for digest-only refs and skip them in history. |

## Details (high / medium)

### H1. The CronJob/Job clock dies on the first watch event

`cronJobTickMsg{epoch}` reuses `m.reloadEpoch` (`model.go:684-695`). These paths increment `reloadEpoch` without re-scheduling the tick:

- every `ResourceChangedMsg` for the kind or an aux kind (`update.go:118-124`; Pod is an aux kind of both CronJob and Job);
- the CronJob `KindsSynced` gate (`:457`);
- the empty-rows retry (`:509`);
- `Reload()` on pop (`:76-78`).

The handler at `:222` then drops the next tick. A grep shows `scheduleCronJobTick` called only from `Init`, `resetAndLoad` and the tick itself. The overlay proof loads a CronJob list, delivers one `ResourceChangedMsg{Job}`, then the in-flight tick, then the debounced reload. The tick returns `nil` and the reload schedules nothing.

Consequences:

1. **Frozen display.** NEXT, DURATION (red-at-deadline included) and the strip clock freeze on every real cluster within seconds, because Pod events are constant.
2. **Wrong persisted values.** `m.now` freezes too. `ManualJobName(row.Name, m.now, …)` previews and creates `-manual-HHMM` with the wrong minute, and `TriggerCronJob` stamps a stale `triggered-at`. `SetCronJobSuspend` stamps a stale `kute.dev/suspended-at`, which §36c then reads back as "suspended 3h" and as an inflated missed-run count.

The existing test (`browse_cronjob_list_test.go:259`) fires a tick with the *current* epoch, so it can't see this.

### H2. Set Resources usage never resolves

`kube.Cluster.ContainerMetricsByNamespace` (`cluster.go:1288`) and the fake (`fake.go:1279`, `:1295`) both write `out[PodKey(ns, name)]`. CLAUDE.md requires that key since commit 6bc5b4c. `containerUsage` still reads `metrics[pod.Name]` (`setresources.go:418`). The overlay test passes a provider-shaped map and gets `ok == false`.

The result is that 25a's central feature, "the new value is a decision, not a guess", is dark everywhere. The comment in `kube/fake/fixtures.go:65-74` shows someone fixed the container-name half of this join and missed the key half. The 25a golden (`golden_states_test.go:737`) seeds `pod.Name`-keyed data, so it pins the bug.

### H3. Replace Job orphans pods and can fail halfway

`ReplaceJob` does Get → `Delete(…, metav1.DeleteOptions{})` → `Create(same name)`. For `batch/v1` Jobs the API server's default garbage-collection policy is `OrphanDependents`, which is why recent kubectl prints "child pods are preserved by default when jobs are deleted". Two consequences follow:

1. **Orphaned pods.** The original attempt pods survive, ownerless. If the Job was still active, they keep running alongside the replacement's new pods. Kute's ledger joins pods by owner UID, so they also vanish from every Kute view.
2. **Create can fail after the delete.** Orphan propagation adds the `orphan` finalizer, and the Job object lingers until the GC removes it. The immediate `Create` can return AlreadyExists. The user then sees an error after the original Job (status, events, history) is already gone. The verb is staged as the "destructive alternative", and its failure mode is the worst one.

This is **unconfirmed** in this repo: the fake clientset doesn't model GC, and `test/e2e/mutation_batch_test.go` exercises only the create path. The general `ctrl-d` on a Job (`mutate.go:338-339`) has the same orphaning; that is cross-ref 10.

### M1. A forbidden Job cache reads as "no history" on the CronJob list

The overlay proof uses a lister whose `KindForbidden(Job)` is non-nil. The result is `state=ready`, cells `… "0" "no retained runs" …`, `cronJobJobsErr=nil`, and run-now `unknownOverlap=false`. The aux-kinds strip note appears, but the table cells and the run-now card still make the definite claims §4.4 point 5 and §36b forbid. cronjobdetail's loader already has the correct one-line fix.

### M2. The schedule editor eats `y` and `u`

The overlay proof types `@daily` and gets `@dail` (the `y` copied the will-run line to the clipboard). It types `0 2 * * sun` and gets `0 2 * * sn`. Every Kubernetes-legal macro except `@midnight` contains `y` or `u`, as do common lowercase day and month names.

### M3. The detail-screen clocks stop after push/pop

`cronjobdetail` and `jobattempts` start `tickCmd()` once, in `Init`. The root delivers messages only to `m.task` (`tui/model.go:870`), so the tick dies the moment `l`, `y`, `e`, `S` or `↵` pushes another screen. `popTask` calls `Reload()`, which schedules only a data reload (`cronjobdetail/update.go:34-40`, `jobattempts/update.go:28-35`). As in H1, the frozen `m.now` becomes the `StagedAt` of the next run-now, suspend or rerun.

### M4. Set Resources blocks the update loop on metrics-server

`r` runs `ContainerMetricsByNamespace` inside `Update`. The parent context is `ClusterContext()`, which can be cancelled but has no deadline, and the REST config sets no `Timeout`. An unavailable `metrics.k8s.io` APIService that hangs rather than returning 503 freezes input until the API server gives up.

While the post-apply result line is showing, every `ResourceChangedMsg` whose kind matches the workload (e.g. *any* Deployment in the cluster) calls `refreshSetResourcesTarget`, which repeats the synchronous call (`update.go:96-103`). Together with 04 M6 and 05 M1, this is the third place metrics are read outside the poll.

### M5. Set Image's history reads three or four kinds cluster-wide

`crossWorkloadHistory` loops `ListRaw(ctx, k, "")` over Deployment, StatefulSet and DaemonSet (plus CronJob) every time the panel opens or `tab` switches container. This is exactly the "loop over kinds" pattern CLAUDE.md calls out:

- **Default mode.** It starts the STS and DS informers when the user edits a Deployment.
- **CronJob list.** It starts all three apps informers.
- **`--namespace-scoped`.** `""` explicitly means the cluster-wide cache, which is the multi-MB read that mode was built to avoid.

Because the first read returns an empty cache and the panel never reloads its history, the "promote what prod runs" row is usually missing on the first open anyway.

### M6. The ledger's rerun name collides from the second rerun

`taken := map[string]bool{m.name: true}` (`jobattempts/rerun.go:32`). `NextRerunName` always returns `-rerun-1` unless the source itself is named that. `RetryJob` doesn't translate AlreadyExists, so the user gets a raw `jobs.batch "x-rerun-1" already exists` after committing. Browse's identical verb consults every listed Job (`job_actions.go:44-51`).

### M7. A keypress during load pops the detail screens

The overlay proof builds a `cronjobdetail` model in `Loading`, sends `]`, and gets `tui.BackMsg`. The same shape exists in jobattempts. Because `moveSibling` sets `found = false` before the reload lands, a quick `]]` (or `j` right after `↵`) leaves the screen entirely.

## Test gaps

- **Clock survival.** No test that the CronJob/Job tick survives a `ResourceChangedMsg`, a sync-gate retry, or a push/pop (browse, cronjobdetail, jobattempts). All existing tick tests fire with the current epoch.
- **StagedAt.** No assertion that `StagedAt`, `-manual-HHMM` or `suspended-at` reflect wall time at staging rather than `m.now`.
- **25a usage join.** The 25a golden seeds bare-name metrics. No test joins against real provider output (`fake.Cluster.ContainerMetricsByNamespace`).
- **Replace Job.** Neither the fake clientset nor e2e covers `ReplaceJob`: propagation, finalizer race, partial failure. An e2e on the kind cluster is the only way to pin the GC behaviour.
- **Typed schedules.** The schedule editor tests type only digits, `*`, `/` and space. Add a typed `@daily`/`sun` case.
- **Rerun naming.** No rerun-twice test from jobattempts.
- **Loading-time keys.** No key-during-loading test for cronjobdetail/jobattempts.
- **Forbidden Job cache.** The CronJob list has a test for an unsynced Job cache (`TestCronJobsPresentButJobCacheUnsyncedStaysLoading`) but none for a Forbidden/erroring one.
- **Lazy-read assertions.** No fake-clientset "what gets listed" test for set-image's history read (M5), in the style of `internal/kube/lazy_test.go`.

## Quick wins

1. `setresources.go:418`: `metrics[kube.PodKey(pod.Namespace, pod.Name)]`, and fix the golden fixture (H2).
2. `loadCronJobRows`: add the same `tui.KindsError` fallback cronjobdetail uses (M1).
3. Gate the `!m.found` "any key goes back" branch on `state == Ready` in both detail screens (M7).
4. `Reload()` in browse/cronjobdetail/jobattempts: re-arm the clock. Give browse's tick its own epoch (H1/M3).
5. `jobattempts/rerun.go:32`: seed `taken` from the namespace's Job names (M6).
6. Stop intercepting bare `y`/`u` while the schedule buffer has focus (M2).
7. `ReplaceJob`/`deleteResource(KindJob)`: `PropagationPolicy: Background` (H3, first step).
8. Refresh the CLAUDE.md §25a paragraph (L3).
