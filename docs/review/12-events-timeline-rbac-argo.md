# 12 — Events, timeline, RBAC & Argo

Scope: `tasks/{events,timeline,whocan}`, `internal/kube/{events,timeline,rbac,access}.go`,
`internal/tui/whocan.go`, `internal/resources/argo.go`, `browse/{argo,whocan}.go`, the fake's
events/RBAC/`CanI` (`kube/fake/fake.go:1142-1510`), `kube.Cluster.RolloutUndo` (driven from 16b's rail),
and `test/e2e/{rbac,argo,inspectors}_test.go`. Design: §9b, §16a/§16b, §22a, §33a/§34a, §41a's access gate.

## Summary

The read side of 9b is in good shape:
- `eventFromObject` handles events.k8s.io `Series` (last-seen and count) and the `EventTime` fallback.
- The dedup key includes namespace and message.
- Sorting is deterministic.
- Both screens gate on `KindsSynced`/`KindsError` for the namespace they actually read, and keep rendered content when a cache only stalls.
- The four RBAC informers are lazy: they come from the `typedKinds` table (`watch.go:218-256`), and who-can's e2e test proves it makes no authorization round trip.
- The cache-local resolver never gates the debug panel's `x`. Only the SAR result reaches `accessState`.

The biggest problem is a write. **16b's `R` rollback strategic-merge-patches the old ReplicaSet's
template onto the Deployment, so it does not roll back.** Containers, env vars, volumes and
annotations added after the target revision survive. The old RS's `pod-template-hash` label is also
injected into the Deployment's template (H1, **proved**). `kubectl rollout undo` strips that label and
replaces `/spec/template` outright.

Who-can's resolver gives wrong answers in four separate ways, all **proved** with
`ResolveWhoCan`:
- Distinct ServiceAccounts with the same name collapse into one row (M1).
- `apiGroups` is ignored, so `metrics.k8s.io` `pods` reads as core `pods` (M2).
- A `resourceNames`-scoped rule is shown as a blanket grant (M3).
- The pinned "(you)" row ignores the implicit `system:authenticated` / `system:serviceaccounts*`
  groups. For any token or exec identity (every managed cluster), it also judges the *kubeconfig
  entry name* rather than the real user. Either way the result is a confident red ✕ for someone the
  API server would allow (M4).

The debug panel's SelfSubjectAccessReview gate rests on a misreading of the API. An RBAC-only
cluster answers a forbidden request with `allowed=false, denied=false`. The panel treats that as
inconclusive and fails open, so on standard clusters the gate never fires. The fake returns
`Denied=true`, so demo mode and real clusters behave in opposite ways (M5).

The timeline dates a rollout by the ReplicaSet's creation time. A rollback therefore appears at the
old revision's original date, and the 16b rail's lifetime windows invert (M6). Argo sync rows stamp
every historical `OperationCompleted` event with the Application's *current* revision and initiator.
A sync kute itself issued carries no `initiatedBy`, so it is labelled "auto-sync". That is a
fabricated answer to §34a's "human or machine?" question (M7). Argo's `u` dashboard link does a
synchronous `ListRaw` on the update loop and assumes `argocd-cm` lives in the Application's own
namespace (M8).

Counts: **1 high, 8 medium, 16 low.**

Already raised elsewhere and not re-counted:
- The 9b `↵` HelmRelease registry-key mix-up, and the Flux timeline commit/revision mismatch (07).
- Unlocked reads of `Cluster.Context` in `WhoCan` (02 L7).
- Debounce starvation in the event bridge (03 H1).

## Findings

| # | Sev | Location | Issue | Suggestion |
|---|---|---|---|---|
| H1 | high | `kube/mutate.go:915-934` (16b `R`, `timeline/update.go:326-331`) | `RolloutUndo` sends `{"spec":{"template":<old RS template>}}` as a **strategic merge patch**. SMP merges lists by key and never removes absent map keys. **(proved)** Rolling `web` back to rev 1 kept the newer `NEW` env var, the `sidecar` container and the `new` annotation. It also wrote `pod-template-hash: abc` into the Deployment's template labels. The confirm says "Roll web back to revision 1", but the result is a hybrid that matches no revision. | Do what kubectl's `DeploymentRollbacker` does: deep-copy the RS template, delete `pod-template-hash` from its labels, and send a JSON patch `[{"op":"replace","path":"/spec/template","value":…}]`. Also copy the RS's annotations that kubectl carries over. Add a fake-clientset test that asserts the template equals the RS's template minus the hash label, plus an e2e test that adds an env var in rev 2 and asserts it is gone after `R`. |
| M1 | medium | `kube/rbac.go:136` | `addRow` dedups on `subj.Kind+"/"+subj.Name`. ServiceAccount subjects are namespaced, so `team-a/default` and `team-b/default` collapse into one row. **(proved)** The second SA and its binding vanish from the answer. The SUBJECT cell (`whocan/view.go:184`) also shows only `Name`. | Key ServiceAccounts on `ns/name` and render `ns/name` (or `system:serviceaccount:ns:name`) in SUBJECT. |
| M2 | medium | `kube/rbac.go:81-91` | `ruleMatches` never looks at `rule.APIGroups`. **(proved)** A `metrics.k8s.io` `pods` get/list rule is reported as granting `list pods`. The same false grant appears for any CRD whose plural collides with a core resource (`events.k8s.io` events, `extensions` ingresses, …). | Add `Group` to `WhoCanQuery`, filled from the descriptor's `APIGroup` (`""` for core). Match `APIGroups` with `*`. Show the group in the strip and in the `auth can-i` line (`deployments.apps`). |
| M3 | medium | `kube/rbac.go:78-83` | A rule with `ResourceNames` counts as a blanket grant. **(proved)** `narrow-user`, who may only `get secret/only-this`, is listed as able to `get secrets`. kube-system is full of such rules (leader-election configmaps/leases), so the list overstates access. | Keep the row but mark it: VIA suffix `· only: only-this` and dim SCOPE. Exclude it entirely for `list`/`watch`/`create`/`deletecollection`, which RBAC never grants by name. |
| M4 | medium | `kube/rbac.go:205-224`; `kube/client.go:262-272`; `kube/context.go:21-30` | The pinned "(you)" verdict uses only `UserName`/`UserGroups` from kubeconfig:<br>• `system:authenticated` is never added, though every authenticated identity holds it. **(proved)** A grant to that group yields `alice … "no bindings grant alice access here"`.<br>• `system:serviceaccounts`/`system:serviceaccounts:<ns>` are never added for an SA token.<br>• For token, OIDC or exec auth (all of GKE, EKS, AKS), `UserName` is the kubeconfig **AuthInfo entry name**, e.g. `arn:aws:eks:…` or `gke_proj_zone_c`, which is never a real RBAC subject. The pinned row is then a guaranteed red ✕ with no grant behind it, and is shown as authoritative. | Issue one `SelfSubjectReview` (`authentication.k8s.io/v1`, GA 1.28) at connect, cached like the server-version read, to get the real username and groups. Add it to CLAUDE.md's one-shot list. Without it, always append `system:authenticated`, and render the pinned row as `? identity unknown` (neutral) instead of ✕ when the name came from an AuthInfo fallback. |
| M5 | medium | `debugpanel/access.go:84-98`; `kube/access.go:25-46`; `kube/fake/fake.go:1496-1509`; `test/e2e/debug_test.go:121-160` | `SubjectAccessReview` runs the API server's **whole** authorizer chain against the caller's **real** identity and groups. `allowed=false, denied=false` therefore means every authorizer declined, and the real request would be 403. That is what `kubectl auth can-i` reports as "no". RBAC never returns `denied=true`, so on any RBAC-only cluster `accessDenied` cannot be reached. The panel never shows the denial, the reason, or `w who-can`. The premise that "another authorizer, another group may change the real answer" (the comment and §41a) does not apply to a SAR. The fake returns `Denied: !granted`, so `--demo` and unit tests show a denial UX that real clusters never produce. The e2e test pins the current behaviour. | Treat `!Allowed` as denied. Use `Reason` when present, otherwise "`create pods/ephemeralcontainers` is not allowed". Fail open only on a transport error or a non-empty `EvaluationError`. Update §41a's wording and flip `TestPodDebugPanelDoesNotInventADenial`. Make the fake return `Allowed`/`Denied=false` to match. |
| M6 | medium | `kube/timeline.go:150`; `timeline/update.go:420-443` | A rollout entry's time is `rs.CreationTimestamp`. `kubectl rollout undo` (and kute's own `R` once H1 is fixed) **reuses** the old ReplicaSet and only bumps its `revision` annotation. The rollback therefore lands in the feed at the old revision's original date, possibly weeks ago, so it is missing from the incident window. The rail sorts by revision number, so `rail[0]` (the newest revision) now has an older `Time` than `rail[1]`. `railSelectionTarget`'s `[start,end)` window then inverts and finds nothing. | Date a revision by the latest of: `creationTimestamp`, the Deployment's `Progressing` condition `lastUpdateTime` when the RS is current, and any `ScalingReplicaSet` event naming the RS. At minimum, sort the rail windows by time rather than by revision number, and label a reused RS with "rolled back to (was rev N)", using `deployment.kubernetes.io/revision-history`. |
| M7 | medium | `timeline/load.go:498-537`; `kube/timeline.go:358-379`; `kube/mutate.go:748-761` | `argoSyncStatusOf` reads the Application's **current** `operationState`, and every `OperationCompleted` event for that app is stamped with it. Each older sync row therefore shows the newest SHA and initiator. Argo's message for this event appears to include the target SHA ("Sync operation to <sha> succeeded", **unconfirmed**), and is not deduped here, so several rows coexist. Separately, `by == ""` is mapped to `"auto-sync"`, but `RequestArgoSync` patches `operation.sync` with no `initiatedBy`. **A human pressing `S` in kute is therefore credited to "auto-sync"**, which is the fabrication §34a forbids. | Attach the status only to the newest `OperationCompleted` per Application. For older rows, parse the SHA from the message if present, otherwise leave REVISION/BY blank. Map `by == "" && !automated` to `"–"`. Have `RequestArgoSync` (and its will-run string) send `"initiatedBy":{"username":"kute"}` or the SSR username from M4. |
| M8 | medium | `browse/argo.go:95-131`; `browse/update.go:849` | `u` runs `argoDashboardBaseURL` → `ListRaw(ConfigMap, row.Namespace)` **synchronously inside `Update`**, with no timeout. That is I/O on the update loop, and the first press can start the ConfigMap informer. A cold cache answers "dashboard url not configured — argocd-cm has no url key" even when it does (**unconfirmed live**; follows from lazy start). `argocd-cm` lives in Argo's control-plane namespace, which is not the Application's namespace under apps-in-any-namespace. | Move the read into a `tea.Cmd` parented on `ClusterContext()` with a timeout. Gate it on `KindSynced(ConfigMap, ns)` and say "loading" rather than "not configured". Look in the control-plane namespace: `argocd` by default, or the namespace of an `argocd-cm` the CountLive/metadata read finds. |
| L1 | low | `tui/whocan.go:71-91`; `browse/whocan.go:28` | The `K` palette and 403-card prefill use `strings.ToLower(desc.Display)` as the RBAC resource. That gives `helm releases` (the real resource is `secrets`) and `flux helmreleases`. Who-can then answers for a resource that doesn't exist: only `*` holders, which is a false "almost nobody". | Use the discovered plural (`GVR.Resource`) and the API group from the descriptor, and map Helm Releases → `secrets`. |
| L2 | low | `kube/rbac.go:157-160` | With `Namespace == ""` (who-can opened from all-namespaces browse), RoleBindings are skipped entirely, so the list shows only cluster-wide grants. The strip doesn't say this. | State "cluster-wide grants only" in the strip, or list RoleBinding grants with their namespace in SCOPE. |
| L3 | low | `kube/rbac.go:248-279` | `closestMiss` can pick a `nonResourceURLs` rule, which has verbs but no resources, and print `grants get on  — not secrets`. `*/scale`-style subresource wildcards aren't matched anywhere. | Skip rules with empty `Resources` in `consider`. Match `*/<sub>` against a `res/sub` query. |
| L4 | low | `whocan/update.go:203-216`, `:287-294` | A pinned *granted* "(you)" row carries no binding, so `↵` on it does nothing, even though `CurrentUserVia` names the binding. | Carry `BindingKind/Namespace/Name` into `WhoCanResult` for the current user's grant. |
| L5 | low | `kube/timeline.go:182` | `MergeTimeline` uses unstable `slices.SortFunc` on `Time` alone. Second-granularity ties are common (restart and BackOff events, rollout and ScalingReplicaSet), and informer list order is map-random, so rows swap on every reload. `sortEventsNewestFirst` fixed exactly this for 9b. | Add a tiebreak: Kind, Object, Reason, Message. |
| L6 | low | `timeline/update.go:53-60`, `:46`; `timeline/model.go` (`loadedMsg`) | Timeline's `loadedMsg` has no epoch. Each Event/Pod/RS change dispatches a full `load()`, and a slow earlier load can land after a newer one and overwrite it. Events and who-can both guard against this. | Copy events' `loadEpoch` guard. |
| L7 | low | `events/view.go:314`; `events/load.go:72-80` | The red "actively failing" check looks up `ns/name` ignoring Kind. A Warning on `Service/redis` turns red because `Pod/redis` is crashlooping. | Apply it only when `kind == "Pod"`. |
| L8 | low | `kube/events.go:80-89`; `timeline/load.go:89-101` | Object-scoped events match on kind and name, not UID. A StatefulSet pod recreated under the same name inherits its predecessor's events. The timeline's Node-scope join matches pod events by bare name across all namespaces, so a same-named pod elsewhere leaks in. | Prefer `involvedObject.uid` when the caller has it. Key the Node join on `ns/name`. |
| L9 | low | `timeline/load.go:478-487` | `splitArgoSyncEvents` diverts **any** event with reason `OperationCompleted`, whatever the object kind. `TimelineFromArgoEvents` then drops the non-Application ones, so another controller's event using that reason disappears from the feed. | Split only when `kind == Application`, so that other events stay plain rows. |
| L10 | low | `timeline/load.go:438-440`, `:495-497` | The comments on `sourceRevisionOf`/`argoSyncStatusOf` claim "reads only kinds already in the cache; an un-started informer yields no pairing". Both call `lister.ListRaw`, which starts the informer. | Fix the comment, or gate on `KindSynced` first if the claim is the intent. |
| L11 | low | `events/model.go:178-188`; `timeline/model.go:238-248`; CLAUDE.md (`events` bullet); design §30b | CLAUDE.md says 9b's `↵` uses `tea.Sequence(BackMsg, GotoResourceMsg)`, and §30b says 16a's `↵` "deliberately pops the screen". Both screens push via `routeGoto` and keep the feed one `esc` away. Node events live in `default`, so `↵` on one also writes `Session.Location.Namespace = "default"` (`model.go:685-690`). | Decide, then align the docs or the code. Pass `""` as namespace for cluster-scoped kinds. |
| L12 | low | `timeline/update.go:326-331`; `timeline/model.go:216-221` | `R` is offered on `rail[0]`, the current revision, which produces a no-op patch behind a "Roll back?" confirm (08 L3 is the Helm twin). | Disable `R` on index 0. |
| L13 | low | `resources/argo.go:114-115`; `browse/argo.go:72-78` | Multi-source Applications (`spec.sources[]`, `status.sync.revisions[]`) render REVISION as `HEAD`, and `S` patches a singular `revision: "HEAD"`. | Read `sources[0]`/`revisions[0]`, or show `N sources`. For multi-source apps, send `revisions` in the patch. |
| L14 | low | `kube/timeline.go:141`; `kube/mutate.go:945`; `timeline/load.go:362-367` | Owner checks use `OwnerReferences[0]` rather than the controller ref. | Use `metav1.GetControllerOf`. |
| L15 | low | `timeline/update.go:88-90` | `isTimelineSource` omits Deployment, Application and the Flux kinds, so `rail[0]`'s live status, the change-cause and sync attribution go stale until an unrelated Pod or Event change arrives. | Add Deployment, and the Argo/Flux kinds when present. |
| L16 | low | `kube/fake/fake.go:1142-1199` | The fake re-implements event projection without the `Series`/`EventTime`/`CreationTimestamp` fallbacks or the deterministic tiebreak. Demo and tests can't exercise the events.k8s.io path. | Export `kube.EventFromObject` and `SortEventsNewestFirst` and call them from the fake. |

## Details (high / medium)

### H1. Deployment rollback merges instead of replacing

The proof used a throwaway overlay test against `client-go/kubernetes/fake`, whose tracker applies
SMP the same way the API server does:

```
deployment rev2: app(web:v2, env A,NEW) + sidecar, annotation new=x
RS rev1:         app(web:v1, env A), labels app=web,pod-template-hash=abc
after RolloutUndo(…, 1):
  labels=map[app:web pod-template-hash:abc] annotations=map[new:x]
  container app image=web:v1 env=[{A 1} {NEW 2}]
  container sidecar image=side:v1
```

Only the image moved. On a real cluster the injected `pod-template-hash` label makes the controller
mint a *new* RS instead of re-adopting rev 1's, so the rail then shows a fresh revision whose template
is neither 1 nor 2. kubectl's `rollback.go` explains why it needs both of these: it strips
`pod-template-hash` and replaces `/spec/template` with a JSON patch. The will-run line
(`RolloutUndoCommandString`) claims to be `kubectl rollout undo --to-revision`, so the documented
command and the executed one disagree. The Mutator doc comment calls this "the correct mechanism".
That comment needs correcting together with the patch type. Fix the fake's rollback (fake.go) to
replace the template too, so tests stop agreeing with the bug.

### M1–M4. Who-can resolution

All four were reproduced with one `ResolveWhoCan` call:

```
secrets: Group system:authenticated via clusterrole/basic ← clusterrolebinding/auth
secrets: ServiceAccount default via clusterrole/reader ← clusterrolebinding/a   (team-b/default lost)
secrets: User narrow-user via clusterrole/one-secret …                          (resourceNames: [only-this])
alice granted=false via="no bindings grant alice access here"                   (alice ∈ system:authenticated)
pods:    User metrics-user via clusterrole/metrics …                            (apiGroups: [metrics.k8s.io])
```

§22a calls this "a security claim". The spec's own CLAUDE.md wording, "a partial answer is as dangerous a security claim as an empty one", applies equally to a wrong
one. M4 matters most in practice. On EKS/GKE/AKS the "(you)" row is *always* a red ✕, with a VIA
explaining a miss for a subject name that no binding could ever contain. A single `SelfSubjectReview`
fixes the identity. It returns `status.userInfo.{username,groups}` and is the authoritative source.
It is a one-shot read in the same class as the server-version read.

### M5. The SAR gate never denies on RBAC clusters

`SubjectAccessReviewStatus` (k8s.io/api `authorization/v1/types.go:255-263`) defines
`allowed=false, denied=false` as "no opinion". At the review level, though, the apiserver's union
authorizer has already consulted every configured authorizer, so "no opinion from all of them" is the
same as forbidden. The real create is rejected with 403 for exactly that reason. The e2e test's own
comment describes the kind cluster answering `allowed=false, denied=false` for a user who cannot
create pods. The current code then shows that user a clean launch button, and the denial, its reason
and `w who-can` (the whole §41a recovery path) only ever appear in `--demo`. Fail open only when the
review itself failed (transport error, `EvaluationError`). The CLAUDE.md rule that the cache-local
resolver must never gate `x` is still satisfied, because the fix uses the server's verdict.

### M6. Rollback timing

The rail sorts by `Revision` (`load.go:330`) while the lifetime windows use `Time`
(`update.go:420-443`). After any rollback (kubectl, ArgoCD/Flux revert to a template that already has
an RS, or kute's own `R`), `rail[0].Time < rail[1].Time`. `railSelectionTarget` then scans
`[old, older)`, finds nothing and falls back to the ROLLOUT row, which sits weeks back. The feed
widens the window to "all time" (`syncFeedToRailSelection`) to reach it. The incident's actual
rollout row therefore never shows up in the 1h window.

### M7. Argo sync attribution

`RequestArgoSync` sends `{"operation":{"sync":{"revision":…}}}`. The application-controller records
`initiatedBy` from what the operation carries; the argocd CLI/API fills `username`. kute fills
nothing, `argoSyncStatusOf` hits `automated || by == ""`, and the row reads `· auto-sync` for a sync a
human just confirmed through a y/N in kute. Separately, all `OperationCompleted` events in the feed
share one `statusOf(object)` answer, so an incident window with two syncs shows the same SHA twice.

### M8. Argo dashboard URL

`copySelectedArgoDashboardURL` is called directly from `browse.Update` (`update.go:849`). It runs
`ListRaw` with `m.session.ClusterContext()` and no timeout. Every other read in browse is a
`tea.Cmd`. Even ignoring the blocking, the first `u` on a session that hasn't read ConfigMaps in that
namespace reads an empty cache and reports the URL as unconfigured.

## Test gaps

- **No test asserts the whole template `RolloutUndo` writes.**
  `TestRolloutUndoPatchesTemplateToTargetRevision` (`kube/mutate_test.go:678-718`) checks only the
  container count and image, from a fixture where the newer revision adds nothing. No e2e test drives
  16b's `R` at all. Add a fake-clientset assertion on the whole template, plus an e2e test with an env
  var added in rev 2.
- `rbac_test.go` (unit) has no case for ServiceAccount namespaces, `apiGroups`, `resourceNames`,
  `nonResourceURLs`, `system:authenticated` or `*/subresource`. Each finding above is a three-line
  table row.
- No test for the SAR `allowed=false, denied=false` → denied mapping. The e2e test pins the
  opposite (see M5).
- No timeline test covers a reused ReplicaSet, where the revision is bumped but `creationTimestamp`
  is old, or same-timestamp tie ordering.
- `argo_timeline_test.go` doesn't cover more than one `OperationCompleted` event per app, or a
  missing `initiatedBy`.
- 9b's `↵` push-versus-pop contract (L11) has no root-level test that asserts the stack depth after
  the jump.
- The fake's event projection (L16) means no unit or demo coverage of `Series`-only events reaching
  9b through the fake.

## Quick wins

1. H1: JSON-patch `replace /spec/template` with `pod-template-hash` stripped. This is about 10 lines plus a test.
2. M1: add the SA namespace to the dedup key and the SUBJECT cell.
3. M4 (partial): always append `system:authenticated` to `currentUserGroups`.
4. M5: `case !msg.result.Allowed && msg.result.EvaluationError == "": accessDenied`.
5. M7: send `initiatedBy.username` in `RequestArgoSync`. Map an empty `by` to `–`, not `auto-sync`.
6. L5: add a tiebreak to `MergeTimeline`.
7. L7: check `Pod/` before applying the red failing colour.
8. L12: disable `R` on `rail[0]`.
