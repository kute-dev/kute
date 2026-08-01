# Flux CD support — plan and task tracker

Design source: `docs/design/v.0.5.0.dc.html` (§30a, §30b, §31a, §32a).
**This file is the tracker.** Update the status boxes as tasks land.

## Context

Flux clusters already *work* in kute — Kustomization and HelmRelease are CRDs, so they get the generic 14a list and 14d detail for free. 0.5.0 promotes them to curated registry entries with real columns, real status derivation, and real verbs. Two problems make this more than a cosmetic upgrade:

1. **The generic status read lies about Flux.** `conditionStatus` (`internal/resources/crd.go:97-119`) is a literal 3-state read of `Ready`. In Flux, `Ready=False, reason=Progressing` is an object working correctly, `Stalled=True` is the real failure, and a suspended object's `Ready` is frozen at whatever it last said. A healthy GitOps cluster renders as a column of red `✕`.
2. **There is a live name collision.** `helm.toolkit.fluxcd.io/HelmRelease` shares its bare Kind with §18a's synthetic `kube.KindHelmRelease` (Helm-3 release Secrets). `Registry.Register` is last-write-wins on `d.Kind` (`registry.go:45`), so on any Flux cluster **today** the discovered descriptor overwrites the built-in one, `helmAwareLister.ListRaw` (`internal/app/app.go:86-88`) still returns decoded Secrets that `projectCustomResource` can't type-assert (→ NAME+AGE only, every column blank), the Flux informer never starts, and `HelmRelease` appears in two groups at once.

The vision fence from the design holds: **kute never installs or bootstraps Flux.** It reads, reconciles, and suspends — and like Helm-without-the-helm-binary (§18a), every verb is a plain API call, so nothing requires the `flux` binary.

## Decisions

- **§30a + §31a + §32a in this effort. §30b (the source→reconciler tree) is deferred** — recorded in the design README as a follow-up with its rationale intact. It's a bespoke screen that cuts against "kinds are data, not code" and needs a join nothing else in the app builds; it's the one piece that can wait without leaving anything half-built.
- **Flux's HelmRelease gets its own `ResourceKind` key**; no registry-wide `(group, kind)` refactor.
- **Flux kinds get their own `GroupFlux`**, appended only when Flux CRDs are discovered.
- **`r` stays as the design draws it**, against a recorded objection — see T9.

## What changed from the pre-design plan

| | pre-design | design (binding) |
|---|---|---|
| Columns | drop declared READY/STATUS, add derived STATUS | **keep READY**, add RECONCILED; no STATUS column — message moves to a sub-line |
| Suspended | `◈` neutral (cordon treatment) | **`‖` amber/Warn**, READY cell reads `suspended` |
| Condition message | inside the STATUS cell | **verbatim sub-line under the row** |
| Detail | reuse 14d | **new screen §31a** — failure card + chain + inventory |
| Timeline | untouched | **§32a** — revision-applied entries, commit enrichment, `v` |
| Verbs | `s`, `ctrl-r` | **`↵` inventory · `r` reconcile · `s` suspend/resume · `o` source · `ctrl-d` delete** |
| `deleteResource` CRD gap | noted, not fixed | **must fix** — 30a's keybar advertises `ctrl-d` |
| Row layout | plain table rows | sub-lines + `+ N ready` fold, unhealthy-first |

---

# Task tracker

Status: `[ ]` todo · `[~]` in progress · `[x]` done · `[!]` blocked

| # | task | status | depends on |
|---|---|---|---|
| G1 | Commit subject + SHA binding | `[x]` **resolved: via Events, not artifact** | — |
| G2 | Verify Flux declared printer columns | `[x]` **resolved** | — |
| G3 | Verify suspension attribution is recoverable | `[x]` **resolved: not recoverable** | — |
| T1 | `demoDiscoveredKind` API-group fix | `[x]` | — |
| T2 | Registry-kind ↔ API-kind substitution seam | `[x]` | — |
| T3 | Delete + patch a custom resource (dynamic fallback) | `[x]` | — |
| T4 | Health-strip glyph into the descriptor | `[x]` | — |
| T5 | Design-doc sections §30a/§31a/§32a | `[ ]` | G1, G2, G3 |
| T6 | Kind keying, `GroupFlux`, collision fix | `[x]` | T1, T2 |
| T7 | §30a descriptor, projection, status | `[x]` | T4, T6, G2 |
| T8 | §30a row rendering — sub-lines, fold, strip | `[x]` | T7, G3 |
| T9 | Write seam: suspend / resume / reconcile | `[ ]` | T3 |
| T10 | §30a verbs wired into browse | `[ ]` | T8, T9 |
| T11 | §31a Kustomization detail screen | `[ ]` | T10, G1 |
| T12 | §32a timeline learns git | `[ ]` | T2, G1 |
| T13 | E2E coverage | `[ ]` | T10, T11, T12 |

---

## Gates — resolve before the tasks that depend on them

### G1 — Commit subject and revision binding `[x]` RESOLVED 2026-08-01

**`status.artifact` carries no commit metadata** — measured:

```json
{"digest":"sha256:df7055d9…","lastUpdateTime":"2026-07-31T13:31:17Z",
 "path":"gitrepository/flux-system/flux-system/efd398be….tar.gz",
 "revision":"master@sha1:efd398bed98a38348c7702355ecd98fc11ac2bef",
 "size":847813,"url":"http://source-controller…/…tar.gz"}
```

No subject, no author, no `metadata` map. The tarball also excludes `.git`.

**But the subject is on the cluster — in source-controller's Events.** On
storing a new artifact it emits, on the GitRepository, a message of the form
`stored artifact for commit '<subject>'`. The subject is embedded in the
event message. (Not observable on the reference cluster at time of writing:
no commit had landed inside the Event TTL, so only the steady-state variant
`no changes since last reconciliation: observed revision '<rev>'` was
present — same reason code, `GitOperationSucceeded`, different message.
Parse on the message shape, not the reason, which has varied across
source-controller versions between `NewArtifact` and `GitOperationSucceeded`.)

**The revision binding is exact, and needs no parsing.** Kustomization
events carry the revision as an event *annotation*:

```json
{"reason":"ReconciliationSucceeded",
 "message":"Reconciliation finished in 1.40s, next run in 10m0s",
 "metadata":{"annotations":{
   "kustomize.toolkit.fluxcd.io/revision":"master@sha1:efd398bed98a…",
   "kustomize.toolkit.fluxcd.io/commit_status":"update"}}}
```

So §32a reads the SHA from the annotation and only the *subject* from prose.

**Design (§32a):**
1. **Primary** — parse the subject out of source-controller GitRepository
   events already in the watch cache. No new watch: 9b/16a already read Events.
2. **Cache** — persist `(repo, sha) → subject` into per-context state the
   moment it is seen. **Events expire (~1h)**, so without this the subject
   vanishes from timeline rows older than the TTL, which is exactly when a
   timeline is most useful. Schema change ⇒ version bump + migration, per the
   persisted-state invariant.
3. **Degrade** — no event and no cache entry ⇒ render `master@efd398b`
   alone. **Never fetch the git remote; no tokens.**

**Drift stays a boolean.** `−9 commits ahead` needs `git log` and remains
unbuildable: compare the Kustomization's `status.lastAppliedRevision` with
its source's `status.artifact.revision` — equal is in sync, different is
`source ahead`. Never a count.

### G2 — Flux's declared printer columns `[x]` RESOLVED 2026-08-01

Measured on a real Flux cluster (11 CRDs across all five groups). **Every**
Flux kind declares exactly these three, and nothing else:

| column | jsonPath |
|---|---|
| `Ready` | `.status.conditions[?(@.type=="Ready")].status` |
| `Status` | `.status.conditions[?(@.type=="Ready")].message` |
| `Age` | `.metadata.creationTimestamp` |

Sources (`GitRepository`, `OCIRepository`, …) add `URL` = `.spec.url`.
`HelmChart` adds `Chart`, `Version`, `Source Kind`, `Source Name`.

**There is no SUSPENDED column anywhere** — confirming suspension has to ride
in the glyph and the READY cell, as §30a draws it.

**The verbatim condition message is itself a declared column.** §30a's
sub-line is not a kute invention: it renders the CRD's own `Status` column,
moved out of a cell because it is prose (`health check failed after 2m0s:
Deployment/aim-stage/aim-worker status: 'InProgress'` is 80+ characters and
would be ellipsized to uselessness in a table cell). That is the
justification to record in the design section.

REVISION and SOURCE **are** kute-derived, from real fields (below). §14a's
"never guesses a column that isn't there" governs the *generic* descriptor;
§30a is curated, like §18a's Helm columns and §23b's ATTACHED — the design
section carries the reason.

### G3 — Suspension attribution `[x]` RESOLVED 2026-08-01: not recoverable

**Drop `by dana` and drop the duration.** Neither is obtainable:

- **Who** would come from `metadata.managedFields`, which `stripManagedFields`
  (`internal/kube/transform.go`) removes from every object *before it enters
  the informer cache* — deliberately, since it is a third to a half of the
  stored bytes. Recovering it means a live per-object GET, which is exactly
  what a browse path must not do.
- **How long** has no source field at all. Flux records nothing about when
  `spec.suspend` was set. The Ready condition's `lastTransitionTime` is
  frozen from *before* suspension, so reading it would report when the object
  last became ready — a plausible-looking number that is simply the wrong
  fact.

So a suspended row's sub-line carries the drift signal only: `suspended` and,
when the applied revision differs from the source's, `· source ahead`.

### Field shapes the projection reads (measured)

- `spec.suspend` is **absent, not `false`**, on an unsuspended object — read it
  with a defaulting accessor.
- Kustomization: `status.lastAppliedRevision` (`master@sha1:efd398be…`),
  `spec.sourceRef{kind,name}`, `spec.path`, `spec.interval`, `spec.dependsOn`.
- GitRepository: `status.artifact.revision`; it *is* the source, so SOURCE
  reads `–` and its declared URL column carries the identity.
- HelmRelease: `lastAppliedRevision` is **null**; the version lives in
  `status.history[0].chartVersion` (`v1.21.0`) with `lastAttemptedRevision`
  as the fallback. Source is `spec.chart.spec.sourceRef{kind,name}`.
- Conditions in the healthy steady state are just `Ready` (plus `Released` on
  HelmRelease, `ArtifactInStorage` on sources). `Reconciling`/`Stalled` are
  transient and absent on a settled cluster — which is why the precedence
  table must not require them to be present.
- `status.inventory.entries[].id` is `<namespace>_<name>_<group>_<Kind>` with
  the version in a sibling `v` field; a cluster-scoped entry has an empty
  first segment (leading underscore). Confirms T11's parse.

---

## Prerequisites — each ships alone

### T1 — `fix(kube/fake): give demoDiscoveredKind the API group its caller declares` `[x]`

`internal/kube/fake/fixtures.go:539-553` hardcodes `Group: "cert-manager.io"` / `Version: "v1"` yet is called for `monitoring.coreos.com` and `argoproj.io`. Visible in 14a's breadcrumb chip, 14c's goto type label, and `Describe`. Gateway API dodged it with an inline literal (`:1066-1085`).

Add `group, version` params, update the nine call sites, collapse the Gateway API literals onto it. **Required first** — otherwise `IsFluxGroup(dk.Group)` never fires in `--demo`.

### T2 — `refactor(kube): resolve a registry kind's API kind and kubectl arg through one table` `[x]`

The substitution seam, landed **with an empty table** so it's a provable no-op. In `internal/kube/kinds.go`:

```go
type substituted struct{ apiKind, group, resource string }
var substitutedKinds = map[ResourceKind]substituted{}

func (k ResourceKind) APIKind() string     // string(k) unless substituted
func (k ResourceKind) ResourceArg() string // ToLower(string(k)) unless substituted
```
and on `DiscoveredKind` (`internal/kube/discovery.go`): `RegistryKind() ResourceKind`.

Route every call site (all verified):

| site | change | why it matters |
|---|---|---|
| `dynamic.go:222` (`ensureDynamicKindFor`), `:302` (`fetchPrinterColumns`), `count.go:75` (`resourceFor`) | `dk.RegistryKind() == kind` | a substituted key must still resolve to the **real GVR** |
| `dynamic.go:191/194` (`dedupeDiscovered`) | key `seen` on `RegistryKind()` | two groups otherwise collapse |
| `crd.go:40, 155, 294, 308, 384` | `dk.RegistryKind()` | descriptor key, 14b COUNT, 14b `↵` target, group list |
| `events.go:49`, `fake/fake.go:686` | `kind.APIKind()` | `involvedObject.kind` is `HelmRelease` on the wire — otherwise §31a's events and §32a's feed stay permanently empty for Flux HelmReleases |
| `edit.go:12`, `mutate.go:612, :718` | `kind.ResourceArg()` | `kubectl edit fluxhelmrelease/x` doesn't resolve; will-run lines would be wrong |

**Acceptance:** table empty ⇒ whole suite green, every golden byte-identical.
**Tests:** `TestResourceForResolvesSubstitutedKind` (`count_test.go`), `TestEnsureDynamicKindForRegistersSubstitutedKindAgainstRealGVR` (`lazy_test.go`, `dynamicfake` helper exists at `:27-33`), `TestObjectEventsMatchesTheAPIKindNotTheRegistryKind`, `TestEditArgsUsesTheKubectlResourceArg` — all must fail before T6 populates the table.

### T3 — `fix(kube): delete a custom resource from its own list` `[x]`

`deleteResource` (`mutate.go:154-155`) ends `default: return fmt.Errorf("delete is not supported for kind %s")` — **`ctrl-d` fails on every discovered CRD row today while the keybar advertises it.** 30a's keybar makes this in-scope. Give it the dynamic fallback `PatchMeta` already has.

While here, extract the shared resolution out of `PatchMeta`'s default branch (`mutate.go:545-556`), which uses `getDynKind` — *started informers only*. `resourceFor` (`count.go:65-80`) also falls back to the discovery snapshot:

```go
func (c *Cluster) dynamicResourceFor(kind, namespace) (dynamic.ResourceInterface, error) // via resourceFor
func (c *Cluster) patchDynamic(ctx, kind, namespace, name string, patch []byte) error
```
Point `PatchMeta`, `deleteResource` and T9's suspend at them. Fixes a second latent bug: 26a's editor on a CRD whose list was never opened.
**Test:** `TestDeleteResourceFallsBackToTheDynamicClient` — fails today.

### T4 — `refactor(resources): move the health-strip glyph override into the kind descriptor` `[x]`

Add `Descriptor.HealthGlyph func(StatusClass) string` + `DefaultHealthGlyph`, defaulted in `Register`/`DefaultRegistry` alongside `Health`/`HealthLabel` (`registry.go:38-46, :80-86`). Move `nodeHealthGlyph` (Neutral→`◈`) and `helmReleaseHealthGlyph` (Warn→`◌`) onto their descriptors; delete the two `if m.kind == …` blocks at `browse/view.go:301-310`.

**Acceptance:** every existing golden byte-identical. That's the whole test.

### T5 — `docs(design): add §30a/§31a/§32a — Flux CD support` `[ ]`

**§29a is taken** — it's `v.0.3.0.dc.html`'s keybar-grammar section, cited in 8 places including `verbs.go:40` and `browse/keys.go:290`. Sections go after §28b; add to `## Files`. Lands before the feature code so every implementation commit can cite it. Each section carries its rationale, per the doc's convention. The load-bearing ones:

- **Why Flux departs from "CRD support is data, not code."** The generic descriptor answers "does Ready say True?", which for Flux is wrong more often than right. Precedent is exact: §23b gave HTTPRoute a bespoke ATTACHED column because "its health question isn't 'is it Ready'". Data-not-code holds for *the long tail* — the operator kute has never heard of. Flux is a fixed, versioned set of five API groups with a documented, stable status vocabulary.
- **Recognition is by API group, never by kind name.** Bare-name matching is the bug §30a exists to prevent, not a pattern to copy. Only the *registry key* is substituted — the informer watches the real GVR, Events still match `HelmRelease`, will-run lines name `helmreleases.helm.toolkit.fluxcd.io`.
- **Suspended is amber, not gray** — a paused Kustomization silently drifting from git is a risk state, not a parked one. The one place Flux departs from the cordoned-node treatment it otherwise resembles.
- **Why `r` survives the bare-letter objection** (see T9) — written here too, since `verbs.go` will point at it.
- **The scope fence**: no install, no bootstrap, no commit-to-git — the same boundary §18a draws with "detecting that an upgrade exists is not performing one". Pausing and requesting a reconciliation are operations on cluster state kute can already see; `flux bootstrap` writes to a Git repository.
- **§30b is deferred, not rejected** — record the tree's rationale (the chain-shaped failure mode, the both-ways-join argument from §23b) and why it waits.

Also amend CLAUDE.md's "CRD support is data, not code" invariant with the exception and its bound.

---

## §30a — Kustomizations list

### T6 — `fix(tui): stop Flux's HelmRelease CRD taking over the Helm releases list` `[x]`

**`internal/kube/kinds.go`** — one const (following the Gateway API consts' doc-comment shape at `:42-52`), the five Flux group consts + `IsFluxGroup`, `FluxReconcileAnnotation`, and the substitution table entry:

```go
KindFluxHelmRelease ResourceKind = "FluxHelmRelease"
// {apiKind: "HelmRelease", group: FluxGroupHelm, resource: "helmreleases.helm.toolkit.fluxcd.io"}
```
**Only this one const.** No `KindKustomization`/`KindGitRepository` — browse keys off `Descriptor.Flux`, never a kind name.

**`internal/resources/groups.go`** — `GroupFlux GroupID = "Flux"`, icon `⇅` (already `tui.GlyphRollout`; the taxonomy icons `◈ ◇ ⚙ ▤ ⬡ ∿ ◆` are taken).

**`internal/resources/crd.go:372-388`** — dispatch on **group**, and split the group lists so a kind lands in exactly one:

```go
switch {
case kube.IsFluxGroup(dk.Group): registry.Register(fluxDescriptor(dk))
case dk.Kind == string(kube.KindHTTPRoute): registry.Register(httpRouteDescriptor(dk))
default: registry.Register(CustomDescriptor(dk))
}
```
Flux group appended before Custom Resources (the catch-all stays last); Custom now gated on `len(customKinds)`, not `len(discovered)`.

**No new lister decorator, no `app.go` assertion-block entry** — Flux kinds are real API objects on the existing dynamic informer path, and the substitution is what lets them fall through `helmAwareLister`'s `if kind != kube.KindHelmRelease` guard.

**Tests (fail today):** `TestFluxHelmReleaseDoesNotReplaceHelmReleasesList` (Display becomes `"Helmreleases"`, `Custom` true), `TestFluxKindsAppearInExactlyOneGroup`.

### T7 — `feat(tui): show Flux reconcile state with real columns and status` `[x]`

New **`internal/resources/flux.go`**, mirroring `crd.go`'s `httpRouteDescriptor`/`projectHTTPRoute`/`httpRouteAttachedCell` triple. Reuses `evalPrinterColumn` (`crd.go:126`), `metaOf`, `shortAge`.

```
glyph · NAME · READY · REVISION · SOURCE · RECONCILED · AGE
```
- **READY** — `True` / `False` / `suspended`. The word; the glyph column carries the colour.
- **REVISION** — `status.lastAppliedRevision` → `status.artifact.revision` → HelmRelease's `status.history[0].chartVersion`; shortened `main@sha1:8f3c2a1b…` → `main@8f3c2a1`.
- **SOURCE** — `spec.sourceRef.kind/name` shortened to `git/aim-config`, or `spec.chart.spec.sourceRef` for HelmRelease. `–` on the source kinds themselves (their own `URL` printer column survives and shows).
- **RECONCILED** — age of the Ready condition's `lastTransitionTime` (`4m ago`).
- Declared printer columns are **not** appended — this is a curated kind now, not a 14a generic.

**Status precedence** (highest first):

| test | glyph | class | READY cell |
|---|---|---|---|
| `spec.suspend == true` | `‖` | **Warn** | `suspended` |
| `Stalled=True` | `✕` | Fail | `False` |
| `Ready=False` | `✕` | Fail | `False` |
| `Reconciling=True`, Ready absent | `◌` | Warn | `–` |
| `Ready=True` | `●` | OK | `True` |
| no conditions | `▲` | Warn | `–` |

**Suspended is amber/Warn, not neutral** — the design's call, overturning the cordon-shaped `◈`/Neutral treatment. `‖` is a **new glyph**: add `GlyphSuspended = "‖"` to `internal/tui/glyphs.go` with an ASCII substitute per the terminal-degradation rule. `resources` can't import `tui` (`resources.go:7-10`), so it stays a rune literal there, as `crd.go:111-116` and `projections.go:598` already do.

New `Descriptor` fields: `Flux bool`, `StatusSemantics bool`, `HealthGlyph` (T4). New `Row` fields: `Suspended bool` (beside `Cordoned` — browse needs the direction to mutate, outside the display Cells), `SubLine string`, `StatusText string`.

**Test:** `resources/flux_test.go` · `TestProjectFluxResourceStatus`, the 6-row table. Fails today: suspended → `●`/OK off the stale Ready; reconciling → `✕`.

### T8 — `feat(tui): show why a Flux object is failing on the row itself` `[x]`

Two additions to the row model, both with **existing machinery**:

- **Verbatim condition sub-line** under a failing or suspended row (`health check failed after 2m0s: …`; `suspended 6d · source ahead`). Render as a `components.Table` `Row` with `GroupHeader` set — a full-width line `moveSelection` already skips as a non-selectable stop (`browse/grouping.go:35`). It follows the row rather than heading a group, which is a new *use* of the field; widen its doc comment rather than adding a parallel mechanism.
- **`+ 3 ready` fold** — `browse/view.go:856`'s `foldLine` already renders exactly this; 6b's collapsed-summary lines are the precedent for the idiom.

Unhealthy-first ordering: confirm whether `resources.List`'s sort (`resources.go:206-211`, by namespace+name) needs a health-first key for Flux descriptors or whether browse's existing sort covers it.

**Health strip:** the mockup omits it, but every other browse kind has one and dropping it forks the shared skeleton. Keep it, styled off `HealthGlyph`/`HealthLabel` (T4): `● 5 ready · ✕ 1 failed · ‖ 1 suspended`.

Also replace two Custom-only assumptions with the descriptor's own claim:
- `view.go:268` → `m.desc.Custom && !m.desc.StatusSemantics && …` (an all-suspended list would otherwise print "no status semantics · NAME + AGE only", a lie)
- `view.go:938` → same guard (a suspended row's glyph would otherwise render TextFaint)

**Goldens:** `test/golden/browse/flux-{80x24,120x36,120x36-dark,120x36-light}.golden`. Register `"flux"` in `goldenStatePrefixes` (`golden_states_test.go:612-617`) **and `truecolorStatePrefixes`** — the status-colour mapping is the whole point and plain goldens render colourless. Demo fixtures land here too (see "Fixtures" below).

### T9 — `feat(kube): add Flux suspend, resume and reconcile to the write seam` `[ ]`

`Mutator` (`mutate.go:24-92`):
```go
SetFluxSuspend(ctx, kind ResourceKind, namespace, name string, suspend bool) error
RequestFluxReconcile(ctx, kind ResourceKind, namespace, name string) error
```
- `SetFluxSuspend` → `patchDynamic` (T3) with `{"spec":{"suspend":%t}}`.
- `RequestFluxReconcile` → **reuse `PatchMeta`** with `FluxReconcileAnnotation` = `time.Now().UTC().Format(time.RFC3339)`. It's an ordinary metadata write and `PatchMeta` already owns typed-vs-dynamic dispatch.
- `FluxSuspendCommandString` / `FluxReconcileCommandString`, both through `kind.ResourceArg()`, matching the design's line exactly:
  `kubectl annotate kustomization/aim-workers -n flux-system reconcile.fluxcd.io/requestedAt="…" --overwrite`
- `fake.Cluster` impls after its `PatchMeta` (`fake/fake.go:405`): mutate the unstructured in place + `c.notify(kind)`. `var _ kube.Mutator = (*Cluster)(nil)` (`fake_test.go:412`) is the gate.

**Same commit:** the two methods break **eight** test doubles — `actions/controller_test.go:27`, `helmhistory:33`, `secretdata:34`, `poddetail:127`, `objectdetail:205`, `nodedetail:461`, `configmapdata:35`, `browse/browse_nodes_test.go:42`.

**Tests:** `TestSetFluxSuspendPatchesSpecSuspend`, `TestRequestFluxReconcileStampsAnnotation` — assert recorded **dynamic-client** actions (patch type, exact body, GVR, RFC3339-parseable stamp). `newTestCluster` (`mutate_test.go:15`) needs a `dynClient`+`discovered` variant.

### T10 — `feat(tui): reconcile, suspend and jump to source from a Flux row` `[ ]`

`internal/tui/verbs/verbs.go`, all gated on `Descriptor.Flux` (`Kinds` stays nil — the Flux kind set is discovered, not compile-time, so `AppliesTo` has no production caller here):

| verb | key | tier | mechanism |
|---|---|---|---|
| `FluxReconcile` | `r` | TierNone | annotate `reconcile.fluxcd.io/requestedAt` = now |
| `FluxSuspend` | `s` | TierNone | patch `spec.suspend` — one verb two directions, Cordon's exact shape (`Scope.Verb` is `flux-suspend`/`flux-resume`, keybar label flips on `row.Suspended`) |
| `FluxSource` | `o` | — | jump to the `sourceRef` object via `GotoResourceMsg` |
| Delete | `ctrl-d` | existing tiers | needs T3 |

> **`r` is a deliberate exception and must be documented in `verbs.go` and §30a.** `RolloutRestart`'s comment (`verbs.go:155-170`) records that it was moved off bare `r` twice over — because `r` already means retry/re-probe app-wide (offline banner, permission-denied card, Forward's restart), and because it was "the only TierNone mutating verb bound to a single unmodified letter with zero friction." Reconcile keeps `r` anyway on the grounds that it is **not** that class of action: it requests the sync that would have happened on the next interval regardless, so the worst case of an accidental press is an early reconciliation. Record that reasoning, or the next person will "fix" it back.

Key-conflict audit against browse's bindings (`update.go:405-590`): `s` collides only with Node-only `NodeShell`, `o` is unbound, `r` currently reaches the retry path only in error states. Kind-scoped reuse is established (`x`, `y`, `R` all do it). Add a Flux row to `browse/browse_keybar_lint_test.go:40-47`.

`internal/tui/actions/controller.go:292-342` — three cases (`flux-suspend`/`flux-resume`/`flux-reconcile`). No new `TaskScope` fields.

New `internal/tui/tasks/browse/flux.go` per the `routes.go`/`nodes.go`/`helm.go` convention: `beginFluxSuspend`/`beginFluxReconcile` mirroring `beginCordon` (`nodes.go:277-287`), setting `m.execFeedback` to the command string before `Begin` (a TierNone verb has no confirm to hang a will-run line on), plus `fluxKeybarGroup` and the source jump.

**Test:** `browse/browse_flux_test.go` — `s` direction-flips on `row.Suspended`, `r` reconciles, `o` jumps to source, all refuse offline, keybar right-note carries the exact command.

---

## §31a — Kustomization detail

### T11 — `feat(tui): explain a failed Flux reconcile and what it manages` `[ ]`

New task package **`internal/tui/tasks/fluxdetail`**, standard split (`model.go`/`update.go`/`view.go`/`keys.go`/`load.go`). Pushed from 30a's `↵`; pill `KUSTOMIZE`. Closest structural model is `tasks/poddetail` (failure banner → meta grid → sub-table) plus `tasks/objectdetail` for the conditions read.

**Three bands, in the design's order:**

1. **Failure card** — 4a's red-tint banner idiom (as poddetail's termination banner already is; the destructive-red *border* reservation applies to confirms, not this). `RECONCILE FAILED · 4m ago · retry in 3m 12s`, the condition message **verbatim**, then the resolved drill-through: `↵ opens Deployment/aim-worker · its pod is CrashLoopBackOff — exit 137, OOMKilled`.
   - The countdown is a **clock read**, which the render-purity invariant forbids in `View`. Compute `retryAt` in the model from `spec.interval`/`spec.retryInterval` + the condition's `lastTransitionTime`, and tick it as podlogs already ticks.
   - The drill-through is a multi-hop join: Kustomization → not-ready inventory entry → its pods → container termination reason. Reuse `resources.UnsettledWorkloads` (`resources/rollout.go:33-51`) for the "one read serves the whole list" shape, never a read per object.
2. **Chain grid** — `source` (`spec.sourceRef` + `spec.path` + `spec.interval`), `source revision`, `applied revision` with the **`in sync — applied, failing health checks`** distinction (the line that kills the most common Flux misdiagnosis), `depends on` (`spec.dependsOn`, amber when a dependency is suspended).
3. **Inventory** — `status.inventory.entries`, each `id` being `namespace_name_group_kind`. Parse, resolve readiness from the informer caches, render `glyph · NAME · KIND · READY`, unhealthy-first with a `+ 12 ready` fold. `↵` opens the object via the existing `GotoResourceMsg` path.
   - **HelmRelease publishes no `status.inventory`** — it delegates to Helm storage. For that kind either resolve through the release's rendered manifest (`kube.HelmReleaseWorkloads`, `helm_workloads.go:44-70`, already exists) or render `inventory not published by this kind`. Decide during implementation; do not fabricate a list.
   - Inventory spans arbitrary kinds, so this screen **reads caches breadth-first if written naively** — exactly what CLAUDE.md forbids. Resolve only the kinds present in the inventory, gate each on `KindSynced`, and never loop the registry.
   - This screen reads workload kinds it doesn't itself list, so it must **reload on them** (`auxKinds`) or its cells go stale.

**Goldens:** new dir `test/golden/fluxdetail/` (4 states).
**Tests:** failure-card drill-through resolves the not-ready inventory entry; `in sync — applied, failing health checks` renders when applied == source but Ready is False.

---

## §32a — Timeline learns git

### T12 — `feat(tui): name the commit behind a change in the incident timeline` `[ ]`

Extends `internal/tui/tasks/timeline` — **no new screen**, one new entry source.

- **`internal/kube/timeline.go`** — new `TimelineRevision` in the `TimelineEntryKind` enum (`:16-21`), and new `TimelineEntry` fields for commit subject/author beside the existing `Revision`/`Image`/`By` (`:26-38`).
- Source: Flux controllers already emit these as ordinary Kubernetes Events, so they arrive through the existing event read — no new watch.
- Render `◆` in accent purple (a **new glyph**, `GlyphRevision`; the only purple marker in the feed), the revision, and the commit subject + author **when G1 says they're available** — otherwise the SHA alone.
- Routine no-change reconciles dedupe into the existing quiet line — 22 identical events would otherwise bury the one revision that mattered. The dedup machinery exists; add the reason to its match set.
- `v` copies the revision (`main@8f3c2a1`).
- **Non-Flux clusters are unchanged**: no Flux, no `◆` rows, zero chrome.

**Test:** a `TimelineRevision` entry renders `◆` and dedupes routine reconciles. One added golden state carrying a `◆` row.

---

## §30b — deferred

The source→reconciler tree. Not built in this effort; recorded in §30a's design section as a follow-up so its rationale isn't lost: Flux's failure mode is chain-shaped (source fetches → reconciler applies → workload rolls out) and no kubectl view shows the chain — the same both-ways-join argument §23b makes for Gateway API. It waits because it's a bespoke screen that cuts against "kinds are data, not code" and needs a join nothing else in the app builds, and because its headline `−9 commits ahead` drift delta is blocked on G1.

---

## Fixtures (land with T8)

`demoFluxFixtures(c, age)` in `fake/fixtures.go`, called from `NewDemo` (`:159`) before `demoHelmReleaseFixtures`, so `--demo` demonstrates both HelmRelease lists coexisting — the whole point of T6. Seeds CRDs via `demoCRD`, `SeedDiscovered(demoDiscoveredKind(...))` with real groups/versions (needs T1), and instances — **the Flux HelmRelease under `kube.KindFluxHelmRelease`**, since `fake.Cluster.ListRaw`/`CountInstances` key on the map (`fake.go:102`, `discovery.go:29`) rather than resolving by group; comment that divergence.

One instance per status branch, all visible in one screenshot: ready, reconciling, stalled, and a suspended one carrying a **stale `Ready=True`** — the fixture that proves suspension outranks a frozen condition. Plus a Kustomization with `status.inventory` and a failing health check, for T11. Helpers `fluxCondition`, `demoFluxObject` beside `readyCondition` (`:523-537`).

---

## T13 — `test(e2e): cover the Flux list, the Helm-release name collision, suspend and reconcile` `[ ]`

Fixtures `52-flux-crds.yaml` + `53-flux-objects.yaml` with hand-written `spec.suspend`/`status.conditions`/`status.inventory`. **No Flux controller** — nothing reconciles, which is exactly what makes assertions durable under the "never assert on transient state" rule. Extend `scripts/e2e-cluster.sh` `apply_fixtures` (`:66-76`) skip list + a `wait --for=condition=Established`.

Assert:
- **`g "hel"` still lands on the Helm-3 list and shows `shop` while the Flux CRD is installed** — the test the whole design exists for.
- the suspended row renders `suspended`; `s` flips it (durable state, safe to poll).
- `r`'s annotation verified via `kubectl get -o jsonpath` outside the TUI, not through 8a.

Follow `gotoKind`'s discipline — wait on the destination list's *title*, never a row name both screens carry. No `t.Parallel`.

---

## Manual verification

`go run ./cmd/kute --demo`:
- `g "kust"` — failing / suspended / ready rows with sub-lines and the fold in one screenshot, **both themes**
- `s`, `r`, `o` on a row; `ctrl-d` on a Flux row (T3)
- `↵` into §31a — failure card, chain grid, inventory
- `t` for the timeline's `◆`
- `g "hel"` still shows Helm-3 releases

Full gate: `go vet ./...`, `go test ./...`, `go test -race ./internal/kube`.

---

## Commit sequence

`feat`/`fix` subjects are written as the user-visible effect — they flow verbatim into the changelog and 28b.

1. T1 · `fix(kube/fake): give demoDiscoveredKind the API group its caller declares`
2. T2 · `refactor(kube): resolve a registry kind's API kind and kubectl arg through one table`
3. T3 · `fix(kube): delete a custom resource from its own list`
4. T4 · `refactor(resources): move the health-strip glyph override into the kind descriptor`
5. T5 · `docs(design): add §30a/§31a/§32a — Flux CD support`
6. T6 · `fix(tui): stop Flux's HelmRelease CRD taking over the Helm releases list`
7. T7 · `feat(tui): show Flux reconcile state with real columns and status`
8. T8 · `feat(tui): show why a Flux object is failing on the row itself`
9. T9 · `feat(kube): add Flux suspend, resume and reconcile to the write seam`
10. T10 · `feat(tui): reconcile, suspend and jump to source from a Flux row`
11. T11 · `feat(tui): explain a failed Flux reconcile and what it manages`
12. T12 · `feat(tui): name the commit behind a change in the incident timeline`
13. T13 · `test(e2e): cover the Flux list, the Helm-release name collision, suspend and reconcile`

T1–T4 are independent of each other and of Flux, and worth landing on their own regardless.
