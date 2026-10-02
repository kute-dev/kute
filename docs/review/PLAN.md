# Whole-app review plan

A feature-by-feature review of kute. Each feature is reviewed by one subagent, run
sequentially, and produces `docs/review/NN-<feature>.md` with suggested improvements.
This file is the index and the brief every reviewer works from.

## What every review covers

For its feature slice, each reviewer reads the code (not just the docs) and checks:

1. **Correctness** — bugs, races, stale-cache reads, wrong namespace/scope, error paths that
   swallow or mislabel failures, edge cases (empty, Forbidden, offline, huge lists).
2. **Invariant compliance** — the "never violate" list in `CLAUDE.md`: theme tokens only,
   verb registry, destructive-action tiers, pure render, `ClusterProvider` boundary,
   lazy informers (no breadth-first reads, `KindSynced`/`KindError` gating), session
   context parenting, `RoutePaste`, `APIKind()`/`ResourceArg()`, PodKey-keyed metrics.
3. **UX vs. design spec** — divergence from `docs/design/README.md` for that feature's
   sections; missing states (loading/empty/error/permission-denied); key vocabulary.
4. **Performance** — work in render paths, per-event recomputation, over-fetching.
5. **Tests** — gaps in unit/golden/fake-clientset/e2e coverage for the feature.
6. **Simplification** — duplication across packages, dead code, precedents that
   `CLAUDE.md` says not to copy (e.g. close-on-commit `SetResources`).

Each finding: severity (high/medium/low), `file:line`, what's wrong, concrete suggestion.
Findings must be verified against the code — no speculative items without saying so.
Reviewers do not edit code; they only write their doc.

## Features (review order)

| # | Feature | Primary code |
|---|---|---|
| 01 | Data layer & lazy informers | `internal/kube/{watch,cluster,kinds,count,transform,request_gate,discovery,dynamic}.go`, `internal/app` lister decorators, `internal/tui/kindsync.go`, `docs/lazy-informers.md` |
| 02 | Connection, contexts & auth | `internal/kube/{context,execauth,probe,health,capability,client}.go`, `internal/tui/{context,sessionctx,clusterwatch}.go`, `tasks/setup`, `internal/app/session.go` |
| 03 | Browse & resource catalog | `tasks/browse` (core: model/update/view/filter/sort/selection/grouping/loading/hints/auxkinds), `internal/resources` (registry/columns/projections/crd), `tasks/objectdetail` |
| 04 | Pods: detail, logs, exec, debug | `tasks/{poddetail,podlogs,execpicker,debugpanel}`, `internal/kube/{pods,logs,exec,debug,debugcopies}.go` |
| 05 | Nodes, capacity & metrics | `tasks/{nodedetail,overview}`, `browse/nodes.go`, `internal/kube/{noderesources,nodedisk,nodeshell,podrequests,metrics}.go` |
| 06 | Workloads: deployments, jobs, cronjobs | `tasks/{cronjobdetail,cronjobschedule,jobattempts}`, `browse/{deployments,jobs,job_actions,cronjob_*,scale,setimage*,setresources*}.go`, `internal/resources/{rollout,jobs,jobattempts,cronjobs,*_actions}.go`, `internal/kube/cronjob_*.go` |
| 07 | Flux | `internal/kube/flux.go`, `internal/resources/flux.go`, `tasks/{fluxtree,fluxdetail}`, `browse/flux.go`, `internal/tui/fluxsubjects.go` |
| 08 | Helm | `internal/kube/helm*.go`, `internal/helmrepo`, `tasks/{helmdetail,helmhistory}`, `browse/helm.go` |
| 09 | Config, secrets & editors | `tasks/{configmapdata,secretdata,yamlview}`, `browse/{meta,edit}.go`, `internal/tui/metapanel`, `internal/kube/{edit,yaml}.go` |
| 10 | Mutations, verbs & confirm flow | `internal/tui/{actions,verbs}`, `internal/kube/mutate.go`, `components/confirmmodal.go`, `browse/{delete,bulk}.go` |
| 11 | Networking: routes, forwards, certs | `tasks/{routetable,forwardpicker,certchain}`, `browse/{routes,forwards,services,certchain,certmanager}.go`, `internal/kube/forward.go`, `internal/resources/{backend,certmanager}.go` |
| 12 | Events, timeline, RBAC & Argo | `tasks/{events,timeline,whocan}`, `internal/kube/{events,timeline,rbac,access}.go`, `internal/resources/argo.go`, `browse/{argo,whocan}.go` |
| 13 | Shell, navigation, palette & theming | `internal/tui/{model,update,chrome,goto,namespace,help,paste,keycast,layout,theme,styles,glyphs}.go`, `components/*`, `components/palette` |
| 14 | App lifecycle, diagnostics, state & updates | `internal/app`, `internal/diag`, `internal/state`, `internal/config`, `internal/update`, `tasks/update`, `cmd/kute` |

Cross-cutting areas (fake provider completeness, e2e harness, golden tests) are covered
inside each feature's "Tests" section rather than as a separate review.

## Output

- `docs/review/NN-<feature>.md` — one per feature, same structure:
  summary → findings table (severity, location, issue, suggestion) → details for high/medium →
  test gaps → quick wins.
- `docs/review/SUMMARY.md` — written last: top findings across all features, ranked.

## Status

| # | Feature | Doc |
|---|---|---|
| 01 | Data layer & lazy informers | [01-data-layer.md](01-data-layer.md) — 1 high, 6 med, 9 low |
| 02 | Connection, contexts & auth | [02-connection-auth.md](02-connection-auth.md) — 2 high, 9 med, 9 low |
| 03 | Browse & resource catalog | [03-browse-catalog.md](03-browse-catalog.md) — 5 high, 6 med, 12 low |
| 04 | Pods | [04-pods.md](04-pods.md) — 2 high, 9 med, 14 low |
| 05 | Nodes, capacity & metrics | [05-nodes-metrics.md](05-nodes-metrics.md) — 1 high, 4 med, 10 low |
| 06 | Workloads | [06-workloads.md](06-workloads.md) — 3 high, 7 med, 10 low |
| 07 | Flux | [07-flux.md](07-flux.md) — 1 high, 11 med, 12 low |
| 08 | Helm | [08-helm.md](08-helm.md) — 2 high, 4 med, 12 low |
| 09 | Config, secrets & editors | [09-config-secrets-editors.md](09-config-secrets-editors.md) — 3 high, 7 med, 10 low |
| 10 | Mutations, verbs & confirm flow | [10-mutations-verbs.md](10-mutations-verbs.md) — 4 high, 7 med, 8 low |
| 11 | Networking | [11-networking.md](11-networking.md) — 1 high, 9 med, 12 low |
| 12 | Events, timeline, RBAC & Argo | [12-events-timeline-rbac-argo.md](12-events-timeline-rbac-argo.md) — 1 high, 8 med, 16 low |
| 13 | Shell, navigation, palette & theming | [13-shell-navigation-theming.md](13-shell-navigation-theming.md) — 5 high, 6 med, 15 low |
| 14 | App lifecycle, diagnostics, state & updates | [14-app-lifecycle.md](14-app-lifecycle.md) — 1 high, 6 med, 17 low |
