# 04 — Pods: detail, logs, exec, debug

Scope:

- `tasks/poddetail` (5a, 41e).
- `tasks/podlogs` (5b).
- `tasks/execpicker` (10a, 41a).
- `tasks/debugpanel` (41b/41c/41d).
- `internal/kube/{pods,logs,exec,debug,debugcopies}.go`.
- `browse/debug.go`.
- The fake provider's log, shell and access support.
- The `internal/app` wiring for all of the above.

Verification:

- `go vet` and `go test` pass for all four task packages and for `internal/kube`.
- `BenchmarkPodLogsRender` costs a flat ~0.41 ms/frame at both 500 and 5000 entries, so the
  `visibleWindow` fast path in `docs/performance.md` still holds.
- Findings marked **(proved)** were reproduced with a throwaway `go test -overlay` test. No repo
  file was changed.
- Anything not observed at runtime is marked **unconfirmed** or "traced by code".

03's H2 already covers bare-name pod resolution in all-namespaces mode (`m.pods[row.Name]` for
`l`/`↵`/`x`/debug/delete). It isn't repeated here. The variants this feature adds are M9
(poddetail's siblings keep one namespace, and "gone" latches) and L13 (the `"default"` namespace
fallbacks).

## Summary

5a's rendering, the 5b row-index design, the access-review gate and the paste routing are all solid.
The defects are in what happens around a handoff, or when a message arrives late:

1. **Every `kubectl` subprocess runs against the kubeconfig's current-context, not kute's.**
   This covers exec, the shell probe, debug attach, debug copy and node debug. None of them
   passes `--context` (or the `--kubeconfig` flag). After a 7a switch, or simply after kute
   restores the last-used context at startup, `x` opens a shell in, or attaches an ephemeral
   container to, a pod on a *different cluster*. The PROD gate is evaluated against kute's
   context, not the one kubectl will actually hit.
2. **5b hides a crash-looping container's logs.** The pre-connect check parks any `Waiting`
   container as "waiting for container to start", and `CrashLoopBackOff` is a `Waiting`
   state. The kubelet serves the last terminated attempt's logs for that state. Yet the most
   common reason to press `l` shows a spinner, until the container happens to restart while
   the screen is open. **(proved)**
3. **Late async replies are applied without an identity check.** A 5a load issued before a
   `[`/`]` move lands under the new pod's name **(proved)**. A debug copy whose `kubectl` exits
   non-zero is never registered for cleanup. Node debug's CLEAN UP lookup drops a pod created
   in the same wall-clock second as the launch, which is the usual case **(proved)**.
4. **5a's reload is the expensive kind and fires on everything.** It runs a live namespace-wide
   metrics-server list, plus PVC/Service/Ingress/RS/controller scans, on every cluster-wide
   change to ten kinds. On a quiet pod, the CPU/MEM bars therefore never refresh.

Counts: **2 high, 9 medium, 14 low.**

## Findings

| # | Sev | Location | Issue | Suggestion |
|---|---|---|---|---|
| H1 | high | `kube/exec.go:114-119` (probe), `:152-163` (`execArgs`); `kube/debug.go:91-104`, `:126-143`, `:166-180`; `kube/edit.go:27`; context source: `app/session.go:105-125`, `kube/client.go:47-55`, `:177-189` | Every `kubectl` argv lacks `--context` and `--kubeconfig`. kute selects its context in-process (`configOverrides.CurrentContext`, restored from `RecentContexts[0]` or `--context`) and never writes it to the kubeconfig. kubectl therefore resolves `$KUBECONFIG`'s current-context, and ignores kute's `--kubeconfig` flag entirely. Exec, the shell probe, debug attach (which mutates a pod permanently), debug copy (which creates a pod) and node debug (a privileged root pod) all target the wrong cluster whenever the two contexts differ. StatefulSet names like `postgres-0` exist in both clusters, so the command succeeds on the wrong one. The "will run" line hides this, because it omits the flag too. Traced by code; no e2e run with mismatched contexts. | Give every kubectl builder a `kubectlTarget{Kubeconfig, Context}` prefix (`--kubeconfig <path> --context <name>`) sourced from `Session.Location.Context` and `kube.explicitKubeconfigPath()`. Render it in the will-run lines. Add one argv test per builder, and an e2e test where the kubeconfig's current-context differs from kute's. `kubectl edit` (09) has the same gap. |
| H2 | high | `podlogs/stream.go:138-158`; `kube/pods.go:335-348` | `checkContainerCmd` parks on any `State == "Waiting"`. `CrashLoopBackOff` is a Waiting state that has a `LastTerminationState`, and the kubelet serves those logs: its log handler checks `lastState.Terminated` before `Waiting`. The screen shows "▲ waiting for container worker to start: CrashLoopBackOff" with no logs. It recovers only if a Pod event lands while the container is briefly `Running`, which may be up to 5 min of backoff away. **(proved)** `checkContainerCmd` returns `containerWaitingMsg{reason:"CrashLoopBackOff"}`. | Park only when there is nothing to read: Waiting **and** `RestartCount == 0`, or no `LastTerminationState`. Carry a `HasPreviousInstance`/`LastTerminated` bit on `ContainerInfo`. Otherwise connect: the history request returns the crash output, and the existing `waitForContainerRestart` path handles the rest. Add an e2e `l` on the `worker` fixture that asserts its crash line. |
| M1 | medium | `poddetail/update.go:129-158`, `:365-394`; `model.go:227-230` | `loadedMsg` carries no name or epoch. The comment says no guard is needed because `moveSibling` updates `m.name` first, but a load issued *before* the move (a watch reload, or an earlier `]`) still lands after it and is applied verbatim. **(proved)** `m.name=bravo`, with alpha's pod, node, banner and events in the body. Then `l` streams alpha (from `m.pod`), while `x`/`D`/`y` target bravo (from `m.name`), and the delete modal shows alpha's owner. Each load includes a metrics-server round trip, so the in-flight window is real. | Stamp `loadedMsg` with `{namespace, name, epoch}`. Bump the epoch in `moveSibling`, and drop mismatches in `applyLoaded` (browse's `reloadEpoch` pattern). |
| M2 | medium | `poddetail/update.go:221-226`, `:597-603`; `poddetail/debug.go:116-118`; `app/app.go:964-990` | poddetail has no `Demo` flag. In `--demo`, the fake `DetectShells` says "bash, sh", and `x` on a single-container pod (or `↵` on an ephemeral row) then runs a **real** `kubectl exec -it` against whatever kubeconfig context is current. That is exactly what `kube.ErrDemoUnavailable`'s doc says must never happen. browse, execpicker and debugpanel all guard; poddetail doesn't. | Add `Config.Demo`, wire it in `openPodDetailFunc`, and short-circuit `execCmd` as browse does. Better: have one `kube.ExecSpec` wrapper own the demo check, so the next screen can't forget it. `editCmd` (09) and nodedetail's exec (05) have the same gap. |
| M3 | medium | `podlogs/stream.go:360-369`; `update.go:83-88`; test `stream_test.go:182` | A container with no log output never gets followed. If both the since-window request and the tail fallback return 0 lines (with since="all", the first empty read alone), `streamContainer` returns, and `runStream` sends `streamEmptyMsg`. Nothing restarts it, so lines written later never appear, despite "follow by default". **(proved)** Two requests, both `Follow:false`, then return. `TestStreamContainerStopsAfterEmptyRecentHistoryFallback` locks this behaviour in. The original fix (842e936) was aimed at the infinite spinner, not at following. | After an empty history, show the empty state, but keep a `Follow:true` connection open (no since, no tail) and leave `StreamEmpty` on the first line. The empty state can't hang, because it renders immediately. |
| M4 | medium | `debugpanel/update.go:379-389` | Copy mode registers the copy (`DebugCopies.Add`) and shows CLEAN UP only when `kubectl debug` exits 0. `kubectl debug --copy-to` creates the pod *before* attaching. An attach timeout, an image pull still in progress, a ctrl-c, or (**unconfirmed**) a shell exiting with its last command's non-zero status all leave a real pod that kute never mentions again. That is the "it is yours to delete" leak §41c exists to stop. | On any exit, check the Pod cache for `copyName` in `m.namespace`. If it exists, register it and show CLEAN UP with the error alongside. |
| M5 | medium | `debugpanel/node.go:86-91`, `update.go:321` | `findNodeDebugPodCmd` drops any pod whose `CreationTimestamp` is `Before(launchedAt)`. The API server stores that timestamp at **second** precision, and `launchedAt` is a local `time.Now()` with nanoseconds. kubectl creates the node-debugger pod within the same second most of the time, so the lookup misses it, retries six times, and pops back with no CLEAN UP. The privileged pod stays orphaned, which is the leak §41d's prompt exists to stop. **(proved)** A pod created 250 ms after launch is stored as `12:00:00Z`, and `launchedAt` `12:00:00.6Z` gives `found=false`. Clock skew makes it worse. The existing test uses `+1s` offsets, which hides it. | Compare against `launchedAt.Truncate(time.Second).Add(-skewAllowance)` (a few seconds). The `node-debugger-<node>-` prefix plus "newest" already disambiguates. |
| M6 | medium | `poddetail/update.go:58-66`; `load.go:345-401` (metrics `:368-375`) | 5a reloads on any `ResourceChangedMsg` for Pod, Event, RS, Service, Ingress, PVC, Deployment, StatefulSet, DaemonSet or Job, cluster-wide (the msg has no namespace). Every reload issues a live `PodMetricsByNamespace` List against metrics-server, plus six cache scans. That breaks "metrics poll on the sync interval only" in both directions. On a busy cluster it is a metrics request per debounced event, and on a quiet namespace the CPU/MEM bars are frozen at open-time forever. | Split metrics into their own `tea.Tick` on the sync interval, as browse does. Keep the watch-driven reload cache-only. Optionally skip reloads for kinds whose change is in another namespace, once the bridge carries one. |
| M7 | medium | `poddetail/load.go:59-63`; `view.go:575-586`; `kube/events.go:45-57` | EVENTS reads `ListRaw(KindEvent, ns)` with no `KindsSynced`/`KindsError` gate. A Forbidden Event list (a common RBAC shape) and a lazy first read both render "no events", which is a claim about the cluster. `eventsErr` only covers a returned error, and the cache path never returns one. This is the poddetail half of 03's M1, though 5a does reload on `KindEvent`, so the unsynced case self-heals. | Before rendering "no events", check `tui.KindsSynced`/`tui.KindsError(lister, ns, KindEvent)` and show the reason, as the invariant requires. |
| M8 | medium | `kube/pods.go:162`, `:395-415`, `:356-372`; `poddetail/load.go:346-361` (`statusClass`) | `containerStatusSummary` and `findLastTermination` scan only `Status.ContainerStatuses`. A pod in `Init:CrashLoopBackOff` (a failing migration, a missing Secret in an init step) shows "▲ PodInitializing" in amber in 5a's title, with **no** termination banner. browse's list says red `Init:CrashLoopBackOff` (`resources/projections.go:96-112`), so the two screens disagree. A crashing native sidecar is also never the banner's subject. | Include `InitContainerStatuses` in both scans. Have `statusClass` reuse the `Init:` derivation from `resources.projectPod` instead of re-deriving it from `kube.Pod.Reason`. |
| M9 | medium | `poddetail/update.go:138-143`, `:365-376`; `kube/cluster.go:1170-1184` | Two paths reach "Pod deleted", and it latches. `applyLoaded` never clears `m.gone` when a later load finds the pod **(proved)**, and every key then goes back. (a) Under `--namespace-scoped`, opening a pod in another namespace from an all-namespaces list or from nodedetail starts a fresh per-namespace Pod cache. `ListRaw` returns empty on that first read, which produces the "deleted" banner. Traced by code. (b) In 6b, `[`/`]` onto a sibling in another namespace (`moveSibling` keeps `m.namespace`) does the same. That is 03's H2/M6 shape, but here it ends in a false deletion claim. | Set `gone` only when `KindSynced(Pod, ns)` is true and `KindError` is nil. Otherwise stay loading and retry via `ScheduleCacheSyncRetry`. Clear `gone` on `found`. Pass `(namespace, name)` siblings. |
| L1 | low | `browse/debug.go:89-110`; `poddetail/debug.go:68-81` | `x` starts the shell probe, which takes up to 2× kubectl exec per container within 5 s, with no in-flight guard or "probing…" feedback. A second `x` starts a second probe, and its result fires a second `tea.ExecProcess` after the first shell exits. | Track `probing` (namespace/name). Ignore repeat `x` while it is set, and show a keybar note. |
| L2 | low | `execpicker/model.go:112-121` | The picker re-probes every container that the router probed a moment earlier. Opening the picker for a 3-container pod costs 6–12 `kubectl exec`s and twice the `pods/exec` audit entries. | Pass `results` from `podShellsProbedMsg` into `execpicker.Config` and probe only the missing ones. |
| L3 | low | `browse/debug.go:141-143`; `poddetail/debug.go:116-118` | The single-container route ignores the detected shell and runs the `sh -c "…bash…"` fallback, so an image with bash but no sh (which `DetectShells` explicitly handles) fails to exec. | Pass `results[0].shells[0]` to `ExecSpec`. |
| L4 | low | `poddetail/view.go:809-811` | `shortAge` calls `time.Since` in render (EVENTS ages). That breaks pure render and makes ages time-dependent in goldens. Same as 03's L2 for 14d. | Stamp `now` in `applyLoaded`. |
| L5 | low | `podlogs/view.go:43-58` | The header badge says green "▶ following" for `StreamEmpty` and `StreamClosed`. With M3 that is a false claim on a stream that has stopped. | Add `StreamEmpty`/`StreamClosed` cases. |
| L6 | low | `podlogs/stream.go:301-306` | The live follow starts at `SinceSeconds=1` after the history read. Lines from the last second are emitted twice at the seam, with no dedup. | Drop leading live lines whose timestamp is ≤ the last history timestamp (they are already parsed). |
| L7 | low | `kube/fake/fake.go:1350-1355`; `kube/fake/fixtures.go` | The fake `StreamPodLogs` ignores `Follow`, `SinceSeconds`, `TailLines` and `Container`. Demo logs therefore print every line twice (the history request, then the "live" one), and every container shows the same logs. The fake also has no ephemeral container fixture, so 41e can't be reached in `--demo`. This falls short of "fake stays feature-complete". | Key logs by container. Honour `Follow` (block until ctx is cancelled, or emit on a slow tick). Seed one pod with an ephemeral container. |
| L8 | low | `debugpanel/update.go:94-98`, `:121-124` | debugpanel never consults `m.conn.Offline()` and never calls `actions.SetOffline`. Launch and cleanup delete stay live while the shell disables every other mutating verb. | Gate `enter`/`ctrl-d` like `verbs.Exec.HiddenWhileOffline`. |
| L9 | low | `debugpanel/keys.go:50-59`, `model.go:116`; `update.go:121-124` | CLEAN UP's `startedAt` is never read, so §41c's "still running · 4m" age is missing. "Still running" is asserted, not checked. Every panel key, including the destructive `ctrl-d`, is a literal `KeyHint` rather than a registry verb. | Render the age from a model-stamped `now`. Register the panel's verbs, or at least route `ctrl-d` through a registered cleanup-delete verb. |
| L10 | low | `kube/debugcopies.go:25` | Registry keys are `namespace/name` with no context, and the registry "survives context switches". A same-named pod on another cluster gets the "⚑ debug copy" tag. | Key by `context/namespace/name`. |
| L11 | low | `poddetail/view.go:779` | The 8b modal says "default grace period applies", although `kube.Pod.GracePeriodSeconds` exists precisely so 8b can say "30s". | Render `pod.GracePeriodSeconds`. |
| L12 | low | `poddetail/update.go:215-226` | §41e's `↵ re-attach` runs `kubectl exec` (a new shell, not a re-attach) without checking the ephemeral container's state. A Terminated ephemeral row produces a kubectl error. | Use `kubectl attach -it -c <name>` for Running, and show "exited" feedback for Terminated. |
| L13 | low | `app/app.go:1631-1637`; `podlogs/model.go:176-178`; `kube/logs.go:36-38` | `openLogsFunc` captures the startup `clusterName`/`namespace`. After a 7a switch, podlogs' scope text and error messages name the old context, and its `""` namespace fallback is the old namespace. Two more silent `"" → "default"` fallbacks stream from the wrong namespace instead of failing. | Read `sess.Location` at call time. Make an empty namespace an error. |
| L14 | low | `kube/debug.go:66-68`; `poddetail/debug.go:72-76`; `kube/pods.go:166-172` | (a) `PodWontStayRunning` treats *any* Waiting container as "won't stay up". An app crashlooping next to a healthy sidecar forces copy mode, so exec into the sidecar and ephemeral attach (both possible on a Running pod) are unreachable. (b) The CPU/MEM bars divide whole-pod usage (sidecars included) by limits summed over `spec.containers` only. With partial or missing limits, the bars overstate. | (a) Use copy mode when the phase isn't Running; for Running pods with a Waiting container, default to attach with copy offered. (b) Show "–" unless every container has a limit, and include native sidecars. |

## Details: high

### H1. kubectl subprocesses ignore kute's active context

kute selects its cluster in-process. `newClientForContext` sets
`configOverrides.CurrentContext` (`kube/client.go:177-189`), and `startupContext`
(`app/session.go:105-125`) launches on `--context` or `RecentContexts[0]` rather than the file's
current-context. 7a switches by rebuilding the in-process client. None of these write the
kubeconfig, and none of them export anything to child processes. The only `os.Setenv` is
`KUBECONFIG` in 4c's path entry (`app/app.go:787`). `--kubeconfig` is held in a package variable
(`kube/client.go:47-55`) that kubectl can't see.

Every kubectl builder emits a bare `kubectl <verb> … -n <ns>`:

- `execArgs`/`shellProbeArgs` (`exec.go:114`, `:152`).
- `podDebugAttachArgs`/`podDebugCopyArgs`/`nodeDebugArgs` (`debug.go:91`, `:126`, `:166`).
- `editArgs` (`edit.go`).

The namespace is explicit; the cluster is not.

Consequences, in increasing order of harm:

- The shell probe answers for the wrong cluster. Usually the result is "pods not found", which
  becomes "unknown", and the exec proceeds anyway.
- `x` on `postgres-0` in staging opens a shell in production's `postgres-0`.
- 41b's attach adds a permanent ephemeral container to that production pod. 41d puts a
  privileged root pod on a production node, if the node names collide (managed node names
  rarely do; kind/k3d `*-control-plane` names do).
- 41b/41d's PROD `y/N` is decided by `config.IsProd(Session.Location.Context)`, the context kute
  shows. kubectl hits a different one.

This needs no unusual setup. Use 7a once, or quit while on a non-default context and relaunch.
The e2e harness launches against the kubeconfig's own current-context, so it can't see the bug.

### H2. 5b's pre-connect check treats CrashLoopBackOff as "not started"

`checkContainerCmd` (`stream.go:138-158`) connects only when
`info.State != "" && info.State != "Waiting"`. `applyContainerStatus` (`pods.go:335-348`)
projects `Waiting{Reason: CrashLoopBackOff}` to `State: "Waiting"`, and `ContainerInfo` has no
field for "has a previous instance".

The kubelet's `validateContainerLogStatus` serves logs from `lastState.Terminated.ContainerID`
whenever one exists. It returns "is waiting to start" only for a container that has never run
(ContainerCreating, ImagePullBackOff, PodInitializing). The defense-in-depth fallback
(`IsContainerNotStartedError`) is correctly narrow. The primary check is broader than the
condition it guards.

So the flow 5a is built to lead into fails: open the crashing pod, read "Last termination ·
exit 1", press `l`. The result is 5b's amber "waiting for container worker to start:
CrashLoopBackOff" over decorative bars, with the strip promising "streams automatically once
running". It does connect if a Pod watch event arrives during one of the brief Running windows,
and it then follows correctly via `waitForContainerRestart`. Until then, the logs that explain
the crash are hidden behind a claim that the container hasn't started.

The worker e2e fixture (`fixtures/10-workloads.yaml:69`) exists for exactly this state, but no
test presses `l` on it.

## Details: medium

- **M1.** The proof drove `load()` for `alpha`, called `moveSibling(1)`, applied `bravo`'s load,
  then applied the stale `alpha` reply. The result was `m.name=bravo` and `m.pod.Name=alpha`;
  the breadcrumb said bravo and the body showed node-a. This persists until the next reload.
  On a quiet pod, that next reload may never come.
- **M3.** The intent in 842e936 was "don't spin forever on an empty since-window", and that part
  is right. The fix also stops the stream, and the test asserts exactly two requests.
  Rendering the empty state immediately and still holding a follow connection meets both goals.
- **M4/M5.** Both are leaks of exactly the objects the design calls out as "yours to delete" and
  "the bug report §41d's CLEAN UP exists to stop repeating". M5 fails most of the time, because
  kubectl creates the pod within ~100–300 ms of launch.
- **M6.** Twelve lines of `load()` run per change event. With 03's H1 bridge fix (max-wait
  flush), a busy namespace would deliver Pod/Event changes about once a second, and each one
  would be a metrics-server List of the whole namespace. Today the bridge's starvation masks
  this.
- **M9.** `ListRaw`'s own comment (`cluster.go:1172-1176`) says the first read of a new scope
  "returns an empty cache, which is exactly what KindSynced is for". poddetail doesn't consult
  it, and turns that empty read into a terminal "Pod deleted · press any key", which any key
  then confirms.

## Test gaps

- **No test asserts a kubectl argv carries a context** (H1). Add per-builder argv tests and an
  e2e test where kute's context ≠ the file's current-context. `ExecCommandString`'s tests
  currently lock in the context-less shape.
- **No 5b test or e2e covers a crash-looping container** (H2). Use `fakeRestartLister` with
  `Waiting/CrashLoopBackOff` + `restarts>0`, and the e2e `worker` fixture.
- **`TestStreamContainerStopsAfterEmptyRecentHistoryFallback` encodes M3** as the intended
  behaviour. Replace it with "empty history, then a follow request is issued".
- **No poddetail stale-reply test** (M1). Browse has epoch tests; poddetail has none.
- **`TestFindNodeDebugPodCmdFindsNewestMatch` uses `launchedAt.Add(time.Second)`.** That cannot
  catch M5. Use second-truncated timestamps.
- **No `--demo` test for poddetail's `x` and ephemeral `↵`** (M2). execpicker and debugpanel
  each have one.
- **No copy-mode test for a non-zero exit after creation** (M4).
- **No fake-clientset action test for 5a's reload.** One would show the metrics List per Pod
  event (M6).
- **No 5a test for a Forbidden or unsynced Event cache** (M7), or for an `Init:CrashLoopBackOff`
  pod (M8).
- **debugpanel has no golden files in either theme**, even though 41b/41c/41d are three distinct
  layouts with Warn-tinted rows. execpicker, poddetail and podlogs all have dark and light
  truecolor goldens.
- **The fake log streamer can't exercise follow, since or per-container behaviour** (L7). Demo
  and fake-backed tests never see a real follow.

## Quick wins

1. M5: compare `CreationTimestamp` against `launchedAt.Truncate(time.Second).Add(-5*time.Second)`.
   One line.
2. M9: `m.gone = false` in `applyLoaded`'s found path. One line, and it clears the latch.
3. M2: add a `Demo` field to poddetail, wire it in `openPodDetailFunc`, and guard `execCmd`.
   About 6 lines.
4. M1: carry `name` in `loadedMsg` and drop it when `msg.name != m.name`. About 4 lines; an epoch
   is better.
5. L5/L11/L3: the badge cases, the grace-period text, and passing the detected shell. A few
   lines each.
6. H2 (minimal form): in `checkContainerCmd`, connect when `info.Restarts > 0`. A crash-looping
   container always has restarts. This avoids a new `ContainerInfo` field.
