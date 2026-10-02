# Whole-app review — summary

Fourteen feature reviews (see [PLAN.md](PLAN.md)), run sequentially, read-only. Many findings
were reproduced with throwaway `go test -overlay` tests and are marked **(proved)** in their
feature doc. No code was changed.

| | High | Medium | Low |
|---|---|---|---|
| Total | 32 | 99 | 166 |

Per-feature: [01](01-data-layer.md) 1/6/9 · [02](02-connection-auth.md) 2/9/9 ·
[03](03-browse-catalog.md) 5/6/12 · [04](04-pods.md) 2/9/14 · [05](05-nodes-metrics.md) 1/4/10 ·
[06](06-workloads.md) 3/7/10 · [07](07-flux.md) 1/11/12 · [08](08-helm.md) 2/4/12 ·
[09](09-config-secrets-editors.md) 3/7/10 · [10](10-mutations-verbs.md) 4/7/8 ·
[11](11-networking.md) 1/9/12 · [12](12-events-timeline-rbac-argo.md) 1/8/16 ·
[13](13-shell-navigation-theming.md) 5/6/15 · [14](14-app-lifecycle.md) 1/6/17

## Top findings, ranked

Ranked by blast radius × likelihood. "Wrong target" and "silent data damage" outrank everything.

| # | Finding | Ref |
|---|---|---|
| 1 | `kubectl` exec/debug/edit and `helm rollback` run without `--context`/`--kubeconfig`; after any in-app context switch they act on the kubeconfig's current-context — possibly another (prod) cluster, behind a non-prod confirm | 04 H1, 08 H1 |
| 2 | All-namespaces mode keys pods by bare name — logs/exec/debug/delete act on a same-named pod in another namespace **(proved)** | 03 H2 |
| 3 | Editing a Secret value flattens newlines / drops binary bytes before any keystroke; second `↵` saves the damage, no confirm outside prod **(proved)** | 09 H1 |
| 4 | Capital `R` typed into any ConfigMap value applies and rollout-restarts every consumer **(proved)** | 09 H2 |
| 5 | Timeline rollback uses a strategic-merge patch — newer env vars/containers/annotations survive, `pod-template-hash` leaks into the template **(proved)** | 12 H1 |
| 6 | Job delete/replace sends empty `DeleteOptions` → orphaned running pods; replace can fail AlreadyExists after the original is gone | 10 H4, 06 H3 |
| 7 | Force-delete and namespace delete are inline `y` outside PROD, against the "always modal" policy; `ForceDelete.Tier` is never read **(proved)** | 10 H2, H3 |
| 8 | A config.yaml parse error silently drops every PROD tag (type-the-name confirms disappear) **(proved)** | 14 H1 |
| 9 | Mutation failures never reach the screen outside fluxtree — a PDB-blocked drain leaves a half-drained node with no message **(proved)** | 10 H1 |
| 10 | YAML view leaks Secret data via quoted keys and `last-applied-configuration` | 09 H3 |
| 11 | Service port-forward dials `port` instead of `targetPort` **(proved)** | 11 H1 |
| 12 | Screens never receive the current connection state on push/pop (and some listen for the wrong message type) — offline screens claim "connected" and keep mutations enabled **(proved)** | 13 H1, 05 H1, 07 M, 11 M |
| 13 | Event forwarder debounce has no max-wait — a kind changing faster than 250 ms never refreshes **(proved)** | 03 H1 |
| 14 | Core kinds can be replaced by same-named CRDs (Knative `Service`, Gateway/Istio `Gateway`) **(proved)** | 03 H3 |
| 15 | Opening the context palette with >8 contexts blocks the UI on credential plugins | 02 H1 |

## Systemic causes

Most high findings collapse into six root causes. Fixing the cause beats fixing each site.

### A. Identity by bare name
Pods by name (03 H2), metrics by name (06 H2), kinds by Kind string (03 H3, 07 H1/M, 13 H5),
ServiceAccounts by name (12 M1), kubectl/helm by implicit context (04 H1, 08 H1).
**Fix:** one rule — every identity carries its scope (`PodKey`, `GroupKind`, namespace, context).
Key the registry on `GroupKind` with a display-name map; route every external command through a
single argv builder that always injects `--context`/`--kubeconfig`.

### B. Confirm tier decided at call sites
Six places pick a tier; `actions.Begin` accepts a bare tier (10 summary). Produces 10 H2/H3,
05 M4 (node delete y/N), 08 M1 (helm rollback in PROD), 09 H2 (bare-letter destructive key in a
text buffer), 14 H1 (PROD silently lost).
**Fix:** `Begin(verb, target)` resolves the tier from the verb registry plus a target-aware
escalation table (Namespace, CRD, Node → modal); PROD config load errors must surface, not reset.

### C. Silent failure / false claims
Unshown mutation errors (10 H1), unreplayed connection state (13 H1), goto capping before
filtering (13 H4), `0d` expired certs and "secret not found" on Forbidden (11 M), 90 s
connect grace on a never-reached cluster (02 H2), CronJob ACT 0 on Forbidden Jobs (06 M).
**Fix:** root-level `activate(task)` that replays `ConnStateMsg`; a shared result-line
component every controller-using screen must render; extend the `KindsSynced`+`KindsError`
gate to every non-list read (certchain, labels editor's Service join).

### D. Lazy-informer regressions
Goto starts the CRD informer (01 H1), Helm empty-state hints and `n` palette start per-namespace
release informers (03 H4, 13 H5), Flux inventory starts cluster-wide caches (07 H1), cert expiry
starts the cluster-wide Secret cache (11 M), node detail lists cluster-wide pod metrics (05 M1).
**Fix:** a fake-clientset action test per screen ("opening X lists only Y"), as
`lazy_test.go` already does for connect.

### E. Ticks and timers dying
CronJob 1 s tick killed by reload epoch (06 H1), parked task timers delivered to the top task
(13 H2), same-kind goto never `Init`s (13 H3), overview reload race (05 M2).
**Fix:** route timer messages to their owning task by ID in the root, and always `Init()` a
freshly built task.

### F. Render cost proportional to data, not screen
Browse styles every row per frame — 44.5 ms at 5000 pods (03 H5); Helm decodes every revision
twice per load (08 H2). Same fix shape as `podlogs`' row index: window first, style second; decode
only the latest revision.

## Suggested fix order

1. **Wrong target & data damage** (A-context argv builder, 03 H2, 09 H1/H2/H3, 12 H1, 10 H4/06 H3, 11 H1) — small, local, highest risk.
2. **Confirm policy centralisation** (B) — one refactor closes ~7 findings.
3. **Root activation + timer routing** (C conn replay, E) — one root change fixes ~30 screens.
4. **Visible mutation results** (10 H1).
5. **Lazy-informer regressions + per-screen fetch tests** (D).
6. **Perf** (F), then **registry keyed on GroupKind** (03 H3 — wider change, needs migration of persisted recents per the versioned-state invariant).
7. Medium/low items per feature doc.

## Documentation drift found

- `CLAUDE.md` references `docs/managed-clusters.md`, which does not exist (02).
- `CLAUDE.md` says SetResources closes on apply; it now stays open (06).
- Drain key: `CLAUDE.md` says `D`, design spec `X`, code `ctrl+d` (05).
- ConfigMap restart chain: spec and `CLAUDE.md` say `ctrl-r`, code uses `R` (09).
