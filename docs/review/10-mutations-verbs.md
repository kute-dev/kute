# 10 — Mutations, verbs & confirm flow

Scope: `internal/tui/actions` (`controller.go`, `tier.go`), `internal/tui/verbs`, `internal/kube/mutate.go`
(the `Mutator` interface and `Cluster` implementation), `components/confirmmodal.go` (`ConfirmCard`,
`ConfirmBox`, `TypeNameModal`, `TypeCountModal`), `browse/{delete,bulk}.go`, the fake provider's
`Mutator` (`kube/fake/fake.go:314-660`), and every `actions.Controller.Begin` call site (about 45 across
18 packages), checked for tier resolution.

Verification: `go vet` and `go test` pass for `actions`, `verbs`, `components` and `kube`. Every finding
was traced to the `file:line` given. Findings marked **(proved)** were reproduced with throwaway
`go test -overlay` tests: five in `browse`, one in `actions`, one in `components`. No repo file was
changed except this doc and the PLAN status row. Anything not observed at runtime is marked
**unconfirmed** or "by code".

Already covered elsewhere, so not repeated:

- 05 M3: the drain confirm's pod count includes DaemonSet and mirror pods.
- 05 M4: Node delete is an inline y/N. Folded into H3 here as one instance of a wider pattern.
- 05 L6: browse's cordon and drain skip `TierFor`.
- 06 H3: `ReplaceJob` orphans pods. H4 below covers the same root cause on plain `ctrl-d`/`D`, which 06 handed to this review.
- 08 M1: Helm rollback in PROD skips the typed name.
- 09 H2/M7: ConfigMap `R` captures a typed capital R, and key vocabulary has drifted.
- 04 H1 and 08 H1: kubectl and helm run without `--context`, so the PROD gate and the execution can target different clusters.

## Summary

The core is sound. Every write funnels through `kube.Mutator`. `actions.Controller` is the single
confirm-to-execute state machine, and it gates on OFFLINE in `Begin`. Writes deliberately use
`context.Background()`. The type-the-name modal gates `↵` on an exact match and supports paste
through `RoutePaste`. CRD deletion is forced to `TierModal` in browse. Drain cordons first and skips
DaemonSet and mirror pods.

The problems sit around that core:

1. **Most mutation results are never shown (H1).** `Controller.Message()` has exactly one reader in
   the whole tree: fluxtree. In browse, poddetail, nodedetail and objectdetail, a Forbidden delete, a
   PDB-blocked drain, or a failed rollout restart simply makes the confirm disappear. Nothing on
   screen says it failed **(proved)**.
2. **The destructive-action policy is split across six places, and that split is the systemic cause
   behind most tier bugs found in reviews 05, 08, 09 and here (M1).** `Begin` takes a bare `Tier`.
   Each call site decides whether to call `TierFor`, which `TierForX` resolver to use, and which
   kinds to special-case. "Does `TierModal` mean typing the name?" is answered by a separate list of
   verb strings. As a result:
   - Force delete is a two-key inline `C`, `y` outside PROD, although policy says it is always a
     modal (H2, **proved**).
   - Deleting a namespace, or every namespace at once with `*` `D` `y`, is an inline y/N (H3,
     **proved**).
   - The PROD modal advertises a `ctrl-k` force chord that does nothing (M7, **proved**).
3. **Delete and drain semantics differ from kubectl's.** `ctrl-d` on a Job orphans its pods (H4).
   Drain evicts bare pods and emptyDir pods with no warning, does not retry PDB refusals, and lists
   every pod in the cluster to find one node's pods (M4, M5).
4. **Bulk delete bypasses the controller** (M2). That loses the OFFLINE gate (**proved**) and the
   controller's per-target mark retention, which it already implements for bulk CronJob suspend.
5. **Drain and PROD Helm rollback cards have no red border** (M6, **proved**). Their style uses
   `Foreground` instead of `BorderForeground`, so the color never reaches the border.

## Findings

| # | Sev | Location | Issue | Suggestion |
|---|---|---|---|---|
| H1 | high | `actions/controller.go:343-356`, `:130`; only reader `fluxtree/update.go:113`; drop sites `browse/update.go:305-383` (esp. `:374-383`), `poddetail/update.go:79-97`, `nodedetail/update.go:113-131`, `objectdetail/update.go:65-80` | `HandleResult` writes "Failed to run …: \<err\>" into `c.message`, but no screen except fluxtree reads `Message()`. Browse only surfaces errors for rollback, cronjob and the meta/set-image/set-resources panels. Pod detail, node detail and object detail surface none. A Forbidden delete, a drain that hit a PDB (node left cordoned, pods half-evicted), a cordon, rollout restart, argo sync, cert renew or job replace all fail silently: the confirm vanishes, as on success. **(proved)**: delete with a Forbidden error gives `state=error`, `msg="Failed to run Delete Pod api-0?: … forbidden"`, `execFeedback=""`, and the error appears nowhere in `Render()`. `Begin`'s own refusals ("disabled while offline", "missing target metadata") are also never shown. | Give the controller a render path: a shared `actions.FeedbackLine(c)` that every confirm-capable screen appends to its keybar `RightNote` (or `execFeedback`) whenever `State()` is success or error, cleared on the next key. Then delete the per-prefix error branches in browse. Add one table test per screen asserting a failed `ResultMsg` reaches `Render()`. |
| H2 | high | `verbs/verbs.go:202-205` (`ForceDelete.Tier: TierModal`, never read); `actions/controller.go:218-221`, `:321-329`; `browse/update.go:1064-1080`; `poddetail/update.go:306-316`; `objectdetail/update.go:207-215`; `browse/keys.go:126-141` | Policy (CLAUDE.md; spec §560) says drain and force-delete always get the modal. Outside PROD, force delete is `D` → `C` (`ArmForceDelete`) → `y`, an inline y/N with grace period 0. `verbs.ForceDelete.Tier` is `TierModal`, but no `Begin` call ever reads it, because force is a sub-state of a `TierInline` delete. Grace-0 deletion of a StatefulSet pod on an unreachable node is the documented split-brain hazard. **(proved)**: non-prod `D`,`C`,`y` gives `forceDeleted=[api-0]` with `Tier()==TierInline`. The inline path is not recorded as a deviation anywhere in `docs/`. | Make `C` (or the spec's `ctrl-k`) on an inline Pod delete re-`Begin` at `TierModal` with verb `force-delete`, using the type-the-name modal that `Escalate` already uses in PROD. Delete `ArmForceDelete`, `DisarmForceDelete`, `ForceArmed` and the three FORCE DELETE keybar branches. If the inline path is intended, record it in the spec and CLAUDE.md and set `ForceDelete.Tier` to match. |
| H3 | high | `browse/delete.go:90-93`; `browse/bulk.go:144-147`; `browse/update.go:962-970`; `objectdetail/update.go:315`; `verbs/verbs.go:198-201` | The "deletes more than the row" escalation is a `kind == KindCustomResourceDefinition` check copied into two browse functions. Namespace has a larger blast radius than a CRD, but it gets the ordinary `TierFor(Delete)`. On the Namespaces list, `*` `D` `y` deletes every listed namespace outside PROD. Only the API server's immortal-namespace guard spares `default`, `kube-system` and `kube-public`. **(proved)**: `pendingBulkDelete.tier == TierInline`, `deleted=[team-a team-b]`. Node has the same shape (05 M4). | Put a cascade-scope kind set (`Namespace`, `CustomResourceDefinition`, `Node`) in the verb policy (see M1), not at call sites, so single, bulk, poddetail and objectdetail delete all inherit it. Add `Namespace` to the CRD rule in the spec and CLAUDE.md. |
| H4 | high | `kube/mutate.go:299-302`, `:338-339` (also `ReplaceJob` `:496-520`, 06 H3) | `DeleteResource` sends an empty `DeleteOptions`. For `batch/v1` Jobs the API server's default GC policy is `OrphanDependents`. That is why kubectl passes `--cascade=background`, and why recent servers warn "child pods are preserved by default when jobs are deleted". Deleting a running Job to stop it leaves its pods running, ownerless, and invisible to kute's Job views (they join by owner UID). **Unconfirmed at runtime**: the fake clientset doesn't model GC, and no e2e deletes a Job. | Pass `PropagationPolicy: Background` on every delete (kubectl's default), forced or not. It is harmless for kinds whose default is already background. Add a fake-clientset action assertion on the `DeleteOptions` and an e2e that deletes an active Job and waits for its pods to go. |
| M1 | medium | `actions/controller.go:165` (`Begin(tier Tier, …)`), `:250-254` (`RequiresTypedName` string list); `verbs/verbs.go:660-785` (`TierFor` + 7 `TierForX`); "nominal" `Tier` fields at `:226`, `:292`, `:315`, `:334`, `:484`, `:493`, `:504`, `:513`, `:546`; call sites passing raw tiers: `browse/nodes.go:284`, `:295`, `secretdata/update.go:342`, `configmapdata/update.go:419`, `metapanel/metapanel.go:699`, `:718`, `certchain/update.go:171`, `browse/certmanager.go:35` | **Systemic cause.** Six inputs decide the confirm policy:<br>(1) `Verb.Tier`, which the comments call "nominal" for nine verbs;<br>(2) `TierFor`'s Inline→Modal rule;<br>(3) seven `TierForX` resolvers;<br>(4) per-call-site kind overrides (CRD);<br>(5) per-call-site choice whether to resolve at all;<br>(6) `RequiresTypedName`'s verb-string list, which decides whether `TierModal` means typing the name or a y/N card.<br>Nothing ties a `Begin` to its verb, kind or PROD state. This is the root of 05 M4 and 05 L6, 08 M1, H2, H3 and M7, and of inconsistencies such as ConfigMap `R` (N rollout restarts) being `TierNone` outside PROD while one rollout restart is `TierInline`. `ArgoSync` in PROD is `TierModal`, but because it is absent from the list it renders as a plain y/N card. | Make the controller resolve policy. Move `Tier` into a leaf package (or move the verb table under `actions`) to break the import cycle. Add `TypedName bool` and per-direction tiers to `Verb`, and use one `Resolve(verb, kind, isProd, dir) Policy`. Change the API to `Begin(verb, kind, action)` so call sites cannot pass a raw tier. Add one table test over `verbs.All × {kinds} × {prod, non-prod}` that pins the whole policy. |
| M2 | medium | `browse/bulk.go:139-213`; `browse/update.go:272-278`; vs `actions/controller.go:403-454`, `:363-380` and `browse/update.go:280-300` | Bulk delete is a second confirm-to-execute machine (`pendingBulkDelete` plus a direct `m.mutator.DeleteResource` loop) beside the controller's own `BulkTargets`/`HandleBulkResult` path. That causes three defects. (a) **No OFFLINE gate**: `beginBulkDelete` never consults `offline()`, so marks + `D` + `y` deletes while reconnecting **(proved)**: `deleted=[a b]`, while single delete is refused. (b) On any failure **every** mark is kept, including rows that were deleted. The strip says "2 marked" and the keybar "delete 2" while one row exists, so the PROD type-the-count target (1) disagrees with the keybar. StatefulSet pods recreated under the same name come back already marked, and a retry deletes them again. The e2e `TestBulkDeletePartialFailureKeepsMarks` only asserts "marked" appears, not the count. (c) A joined error string instead of per-target results. | Route bulk delete through `Begin` with `Scope.BulkTargets` and handle `BulkResultMsg`, which already keeps only failed marks and goes through the OFFLINE gate. Move the type-the-count check into the controller (see L5). Delete `bulkDeleteResultMsg` and `executeBulkDelete`. |
| M3 | medium | `actions/controller.go:343-356`, `:363-368` | `HandleResult`/`HandleBulkResult` ignore `ActionID` and unconditionally clear `pending`/`tier`/`state`. A result from an earlier in-flight action (a TierNone cordon, or a slow drain) landing while a newer confirm is open wipes that confirm: the y/N disappears and the next `y` falls through to the `y` YAML key. **(proved)** in `actions`: Begin cordon (in flight), Begin delete (Active), then deliver the cordon result → `active=false pending=nil state=success`. | Track an in-flight action ID set. Apply a result to `state`/`message` only if it matches, and never clear a pending confirm whose ID differs. |
| M4 | medium | `kube/mutate.go:677-710`; `actions/controller.go:544-545` | `Drain` is not `kubectl drain`. (a) Bare pods with no controller owner are evicted and gone for good; kubectl refuses without `--force`. (b) Pods with `emptyDir` volumes are evicted with their data; kubectl refuses without `--delete-emptydir-data`. (c) A PDB refusal (429) is recorded once and drain moves on; kubectl retries every 5 s until `--timeout`. (d) It returns once evictions are *requested*, not when pods are gone. (e) The evicted count is discarded (`_, err = mutator.Drain`). Together with H1, a PDB-blocked drain leaves a cordoned, half-drained node and shows no message. | Pre-flight the pod list into evictable / unmanaged / emptyDir / blocked and show it in the drain card, the way kubectl's error lists them. Refuse (or require an explicit second confirm) for unmanaged and emptyDir pods. Retry 429 with backoff under a bounded deadline. Report "N evicted, M blocked by PDB" as the result. |
| M5 | medium | `kube/mutate.go:685-689` | Drain finds one node's pods with an unpaginated, cluster-wide `Pods("").List` with no field selector, justified by the fake clientset ignoring field selectors. On a 10k-pod cluster that is the entire pod set over the wire for one node, with no deadline (see L6). | Send `FieldSelector: "spec.nodeName=" + node` and keep the Go-side filter. The fake still passes and the real server does the work. |
| M6 | medium | `browse/nodes.go:319`; `nodedetail/view.go:763`; `helmhistory/view.go:279`; `components/confirmmodal.go:56`; also `tui/model.go:1549` | `ConfirmBox` draws its border from `styles.Border.Border(...)`. Lipgloss v2 colors borders only from `BorderForeground` (`borders.go:425`, `styleBorder`). These three callers set `.Foreground(theme.ConfirmBorder)`, so the drain card and the PROD Helm-rollback card render with an uncolored border. The intended "destructive = red" signal is missing on two of the most disruptive confirms. **(proved)**: `Foreground` gives top line `"╭────…╮"` with no SGR; `BorderForeground` gives `"\x1b[38;2;217;138;138m╭…"`. The quit confirm's "neutral `BorderPalette`" border is the same no-op. | Use `BorderForeground` at all four sites. Better, have `ConfirmBox` take a `Destructive bool` and apply `Theme.ConfirmBorder` itself, so a caller can't get it wrong. Add a forced-truecolor golden for the drain card. |
| M7 | medium | `browse/delete.go:168` ("ctrl-k force delete"); `poddetail/view.go:779` ("C force delete"); handlers `browse/update.go:1149`, `poddetail/update.go:343-344`, `objectdetail/update.go:240-241` (literal `"C"`) | The PROD type-the-name modal on browse and objectdetail tells the user `ctrl-k` force-deletes (as spec §8b says). The handler matches literal `"C"`, so `ctrl-k` falls through to `HandleTypeKey` and the textfield's `DeleteAfterCursor`. **(proved)**: after `ctrl+k`, the verb is still `"delete"`. Pod detail's copy of the same modal says `C`. The literal `"C"` is intercepted before typing, so a capital C can't be entered into the name buffer (moot for DNS-1123 names, but the modal also serves non-delete verbs). | Render the chord from `verbs.ForceDelete.Hint()` and match on `verbs.ForceDelete.Key` (resolving `C` vs `ctrl-k` against the spec once). Use a modifier chord inside a text-entry modal so it never collides with typed input. |
| L1 | low | `kube/fake/fake.go:629-653`, `:334-336`, `:614-627`, `:164-181` | The fake `Drain` swallows every delete error and always returns `nil`, so partial-drain UI is untestable in `--demo` and unit tests. `DeleteResourceForced` is indistinguishable from `DeleteResource`. Fake mutators write in place through pointers that `ListRaw` hands to readers outside `c.mu` (e.g. `Cordon` writes `n.Spec.Unschedulable`), a data race by construction in `--demo`. **Unconfirmed** under `-race`. | Return a joined error from fake `Drain`. Record forced deletes separately. Deep-copy in `ListRaw`, or replace objects instead of mutating them. |
| L2 | low | `kube/mutate.go:404-417` | `RolloutRestart`'s `default:` branch patches a **Deployment** of the same name for any kind other than StatefulSet or DaemonSet. Today only those three reach it (`configmapdata/model.go:331`), but a future caller passing CronJob would silently restart the wrong object. | Make `KindDeployment` explicit and return an error for any other kind. |
| L3 | low | `actions/controller.go:348-355`, `:383-395`; labels e.g. `browse/delete.go:96` | Labels are phrased as questions, so messages read "Done: Delete Pod x?." and "Failed to run Delete Pod x?: …". `verb := "run"` is a dead variable. `Prompt()` prints the raw registry kind (`Kind` string, not `APIKind()`). These were invisible until H1 is fixed. | Give `TaskAction` a separate past-tense `Summary`, or strip the trailing `?`. Drop `verb`. |
| L4 | low | `verbs/verbs.go:676-785` | `TierForEdit`, `TierForDebug`, `TierForSetImage`, `TierForSetResources`, `TierForAddSecretKey` and `TierForConfigMapData` have identical bodies, and `TierForCronJobSuspend` ≡ `TierForJobSuspend`. The "nominal" `Verb.Tier` values they sit beside are misleading because nothing reads them. | Fold into M1's single resolver. Short of that, one `TierProdInline(isProd)` and one `TierSuspend(dir, isProd)`. |
| L5 | low | `browse/update.go:1064-1160`; `poddetail/update.go:300-350`; `objectdetail/update.go:200-245`; `actions/controller.go:214`, `:307-313` | The y/`C`/n/esc inline router and the esc/enter/`C` modal router are copied in three screens, with literal keys and hand-built `{Key:"y",Label:"confirm"}` hints, which is how M7's drift happened. `Confirm` skips the typed gate whenever `BulkTargets` is non-empty and trusts browse's `bulkCronJobSuspendCountMatches` to have checked the count. `Escalate` has no state or tier guard. | Add `actions.RouteConfirmKey(c, msg)` and `c.KeybarHints()`. Move the count gate into `Confirm` (`TypedName == strconv.Itoa(len(BulkTargets))`). Guard `Escalate` on `Active() && tier == TierModal`. |
| L6 | low | `actions/controller.go:413`, `:484-578`; `kube/mutate.go:677-710` | Writes rightly avoid the session context, but they have **no deadline at all**, and no REST timeout is configured (06 M4). A hung API call, or a long drain, leaves the controller in `Loading` indefinitely with no progress shown. Its eventual result then hits M3. | Keep `Background()` but add a generous per-verb deadline (drain: minutes, with progress), or at minimum show "still running…" from `Loading`. |
| L7 | low | `config/config.go:76-78`; `CLAUDE.md` invariant text; spec `README.md:112` | PROD is an exact match on the context *name* in `~/.config/kute/config.yaml`, a documented deviation (mvp-plan decision #2). It is not a heuristic, but two kubeconfigs that share a context name (`default`, `kubernetes-admin@kubernetes`) share PROD status. CLAUDE.md's invariant still says "kubeconfig annotation". | Key on (cluster server URL + context name), or also honour a kubeconfig `extensions` entry. Update the CLAUDE.md wording to match the decision. |
| L8 | low | `verbs/verbs.go:198-208`, `:443-446`; `browse/deployments.go:2`; CLAUDE.md "ctrl-d delete" / "ctrl-r rollout-restart"; spec §9a/§14/§20a `ctrl-d delete`, §8b `ctrl-k` | Key vocabulary for the mutating verbs disagrees three ways. Code: Delete `D`, ForceDelete `C`, Drain `ctrl+d`, RolloutRestart `R`. CLAUDE.md and spec: ctrl-d delete, ctrl-k force, ctrl-r restart. File headers say yet another. This extends 09 M7 to the core verbs. | Decide once, update `verbs.go`, then the spec and CLAUDE.md from it. Add a doc-lint test asserting CLAUDE.md's key mentions match `verbs.All`. |

## Details (high and medium)

### H1. Mutation failures are invisible

`Controller.HandleResult` (`controller.go:343-356`) sets `state=error` and a message. The only code in
the tree that reads `Message()` is `fluxtree/update.go:113`. Browse's `ResultMsg` branch
(`update.go:305-383`) handles meta, set-image, set-resources and cronjob by ActionID prefix. Then:

```go
if msg.Err == nil {
    return m, m.load()
}
if strings.HasPrefix(msg.ActionID, "rollback-") { m.execFeedback = "rollback failed: " + … }
```

The comment above this code admits that "actions.Controller's own error message has no render path in
browse today". Every other error is dropped: delete, force-delete, cordon, drain, rollout-restart,
argo-sync/refresh, flux, cert-renew, job-replace and job-suspend. Pod detail, node detail and object
detail have the same `if msg.Err == nil { return m, m.load() }` with nothing after it.

Drain makes this worst. `Cluster.Drain` cordons, evicts what it can, and returns a joined error for
PDB-blocked pods. The user sees the card close as it does on success, the node shows as cordoned,
and nothing says the drain is incomplete.

### H2. Force delete is inline outside PROD

`verbs.ForceDelete` declares `TierModal`, and `TierFor`'s doc says "Drain/ForceDelete are always
modal regardless of prod". No `Begin` call ever uses `ForceDelete`. Force delete is reached by
mutating a pending `TierInline` delete: `ArmForceDelete` sets `forceArmed`, and `Confirm` rewrites the
verb to `force-delete` (`controller.go:218-221`). The keybar does switch to a "FORCE DELETE" pill,
but the gate is still one `y`. Escalating to the type-the-name modal that `Escalate` already drives
in PROD would close the gap with less code than the armed sub-state now costs.

### H3. Cascade-scope deletes take the row-level tier

The CRD exception exists because deleting one row deletes far more than the row. Namespace (and Node,
05 M4) fit that rule better than CRD does, yet the exception is a `kind ==` check copied into
`beginDelete` and `beginBulkDelete`. It does not reach `objectdetail`/`poddetail`'s own
`TierFor(verbs.Delete, …)`. With `*` (mark every filtered row) this becomes a three-key wipe of a
non-prod cluster's namespaces.

### H4. Deleting a Job orphans its pods

See 06 H3 for the GC mechanics. The `ctrl-d`/`D` path hits it with no rerun involved. The fix is one
field on the shared `deleteResource` options, which also fixes `ReplaceJob`'s first step.

### M1. One policy, six inputs

Every tier bug found so far has the same shape: a call site picked the wrong input.

| Bug | Wrong input |
|---|---|
| 05 L6 | skipped `TierFor` |
| 05 M4 / H3 | missed a kind override |
| 08 M1 | verb absent from `RequiresTypedName` |
| H2 | `ForceDelete.Tier` never read |
| ConfigMap `R` vs `rollout-restart` | different `TierForX` |
| `ArgoSync` PROD | `TierModal` without the typed name |

Because `Begin(tier, action)` accepts any `Tier`, the compiler can't help. A `Begin(verb, kind, action)`
signature, a single `Resolve`, and one table test covering every (verb, kind, prod) combination would
make the policy auditable in one file. It would also make CLAUDE.md's "every verb goes through the
command registry" true for tiers, not just for keybar labels.

### M2. Bulk delete is a parallel controller

`actions.Controller` already runs `BulkTargets` per target and reports `TargetResult`s. Browse's own
`BulkResultMsg` handler (`update.go:280-300`) already keeps only failed marks. Bulk delete predates
that work and still uses its own machine. Moving it over fixes the OFFLINE hole, the
ghost-mark/count mismatch and the joined-error UX in one change.

### M3. Late results clobber a live confirm

`ResultMsg.ActionID` exists precisely so a screen can "ignore results for actions it no longer cares
about" (`controller.go:24-26`), but `HandleResult` never compares it. Begin a TierNone action, then
open any confirm before its result lands, and the confirm is erased.

### M4 and M5. Drain

kubectl drain's refusals (unmanaged pods, emptyDir, PDB with retry) exist because each is a way to
lose work or availability without noticing. Kute's drain is a TierModal operation, so the confirm
card is the right place to state what will happen: "4 evictable · 1 unmanaged (will be lost) · 1
emptyDir". The node's pod list should come from a field-selected List, not the whole cluster's pods.

### M6. The red border never renders on drain and Helm-rollback cards

`Foreground` vs `BorderForeground` is a silent mistake. Only a forced-truecolor golden of a
`ConfirmCard` would have caught it, and none exists (the truecolor goldens cover `browse`'s list,
not its confirm states).

### M7. The advertised force chord is dead in the PROD modal

The browse and objectdetail modals print "ctrl-k force delete". The router only knows `"C"`. Pod
detail prints `C`. Nobody reading the browse or objectdetail modal can find the real key.

## Test gaps

- **No test renders a failed `ResultMsg`** in browse, poddetail, nodedetail or objectdetail (H1). One
  per screen: set `fakeMutator.err`, run `D` `y`, and assert the error text is in `Render()`.
- **No policy table test.** Nothing enumerates `verbs.All` × kinds × prod and asserts the resolved tier
  and typed-name requirement. H2, H3, 05 M4 and 08 M1 would each have been one failing row.
- **No OFFLINE test for bulk delete** (M2a), and no test that a partial bulk failure keeps exactly the
  failed marks. The e2e asserts only that "marked" appears.
- **No stale-result test** for the controller (M3).
- **Drain has no e2e**, although the kind cluster has a dedicated worker
  (`TestCordonAndUncordonDedicatedWorker` already uses it). There is no PDB-blocked eviction case, no
  unmanaged-pod case, no emptyDir case, and the fake can't express partial failure (L1).
  `TestDrainCordonsAndSkipsDaemonSetAndMirrorPods` covers only the skip rules.
- **No fake-clientset assertion on `DeleteOptions`** (propagation policy, grace period for non-force),
  and no e2e deleting an active Job (H4).
- **No forced-truecolor golden for `ConfirmCard`/`TypeNameModal`**, so border color is unpinned (M6).
- **No test that the force-delete hint matches the handled key** (M7).

## Quick wins

1. `PropagationPolicy: Background` in `deleteResource` (H4, one line, also fixes 06 H3 step 1).
2. `BorderForeground` at the four `ConfirmBox` callers (M6).
3. Render `actions.Message()` on error in browse, poddetail, nodedetail and objectdetail (H1, minimal
   form: copy it into `execFeedback`).
4. Add `if m.offline() { return nil }` to `beginBulkDelete` (M2a), until bulk delete moves onto the
   controller.
5. Add `FieldSelector: "spec.nodeName="+node` to drain's List (M5).
6. Render the force chord from `verbs.ForceDelete` in both modals, and match on `verbs.ForceDelete.Key` (M7).
7. Add `KindNamespace` beside the CRD override at both browse sites (H3 stop-gap, until M1).
8. Compare `ActionID` in `HandleResult` (M3).
