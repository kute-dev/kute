# 05 — Nodes, capacity & metrics

Scope:

- `tasks/nodedetail` (11b).
- `tasks/overview` (19a).
- `browse/{nodes,metrics}.go` (11a), plus the Node branches of `browse/update.go`.
- `internal/kube/{noderesources,nodedisk,nodeshell,podrequests,metrics}.go`, plus
  `Cluster.{NodeMetrics,PodMetricsByNamespace}` in `cluster.go`.
- The node screens' cordon/drain call sites. The `Mutator` implementation belongs to 10.
- The fake provider's node, metrics and disk support (`kube/fake/fake.go`).

Verification:

- `go vet` and `go test` pass for `nodedetail`, `overview`, `browse` and `internal/kube`.
- Findings marked **(proved)** were reproduced with throwaway `go test -overlay` tests. No repo
  file was changed.
- Anything else is "traced by code", or **unconfirmed** where runtime behaviour outside this
  repo is assumed.

Already covered elsewhere and not repeated here:

- 04 M6: poddetail's metrics-on-every-reload. M1 below is the node-screen instance of the same
  pattern, and is worse because both of its reads are cluster-wide.
- 04 H1: kubectl without `--context`. It applies equally to nodedetail's `x` exec and `s` node
  debug.

## Summary

The core arithmetic is right, and it matches CLAUDE.md everywhere:

- **Denominators.** CPU/MEM usage bars divide by `NodeCapacity`, in all three of 11a, 11b and
  19a. Request bars and PODS divide by `NodeAllocatable`. Both are derived only in
  `kube/noderesources.go`.
- **Requests.** Sums go through `PodEffectiveRequests`, which matches upstream
  `resourcehelper.PodRequests` for sidecars, init peaks and overhead.
- **Metrics keys.** Pod-metrics maps are `PodKey`-keyed everywhere.
- **Disk reads.** The kubelet summary read is per node, runs every 15th tick (30s), and
  latches off on Forbidden.
- **Cache gating.** `KindsSynced`/`KindsError` gate nodedetail's and overview's
  empty/not-found states correctly.

The defects are about *when* things are read and *what a screen believes*:

1. **A pushed screen doesn't know the connection state.** nodedetail and overview start with a
   zero `ConnState`, which reads as online. The root never replays its state to a new task. If
   the screen is opened during a `ConnUnauthenticated` outage, the health loop stays silent
   for 24h, so the screen:
   - shows "connected";
   - enables cordon, drain, exec, debug and edit;
   - polls metrics-server twice every 2s, which re-runs the failed exec credential plugin.

   This is cross-cutting: about 25 task packages hold their own `conn`.
2. **nodedetail's metrics traffic scales with the cluster, not the node.** Every 2s poll, and
   every reload triggered by *any* Pod or Node event cluster-wide, issues a cluster-wide
   `PodMetrics` List. The reload also issues a `NodeMetrics` List. This feeds one node's
   ~30–110 rows. **(proved)**
3. **Overview reloads without a debounce or an epoch.** It makes one live `NodeMetrics` call
   per watch event, and an older reply can overwrite a newer one **(proved)**. It has no
   metrics poll, so its CAPACITY cpu/mem freeze on a quiet cluster.
4. **The drain confirm overstates its count.** It includes DaemonSet and mirror pods, which
   drain skips **(proved)**. Separately, `D` on a Nodes row deletes the Node object behind an
   inline y/N in non-prod, while drain is always modal. The docs give three different keys for
   drain.

Counts: **1 high, 4 medium, 10 low.**

## Findings

| # | Sev | Location | Issue | Suggestion |
|---|---|---|---|---|
| H1 | high | `nodedetail/model.go:310-330` (zero `conn`), `load.go:224-229` (`pollsMetrics`), `update.go:73-81`, `view.go:70`; `overview/view.go:66`; root push `tui/model.go:870-876` (no conn replay); `kube/health.go:339-343` (24h unauthenticated wait) | Pushed tasks start with `conn == ConnState{}`, and `Offline()` is false for it. The root records `m.conn` but never sends it to a newly pushed task. Browse allows `↵` on a Nodes row while offline (the "browse snapshot" keybar), so the pushed nodedetail shows "● connected" and arms its 2s poll. It then calls `NodeMetrics` + `PodMetricsByNamespace("")` every tick, plus `NodeDiskUsage` every 30s. Cordon, drain, exec, `s` node-debug, `E` edit and `m` meta are all enabled, because `actions.SetOffline` is never called. During `Reconnecting` this lasts until the next backoff ping (≤30s). During `ConnUnauthenticated` the probe sleeps 24h and no `ConnStateMsg` ever arrives, so the poll keeps re-invoking the failed credential plugin indefinitely. CLAUDE.md says that "is not a retry strategy", and `browse.pollsMetrics` exists specifically to prevent it. **(proved)** Without a `ConnStateMsg`, the pill is `NODE` (not `OFFLINE`) and `pollsMetrics()==true`. Plugin re-invocation per request is **unconfirmed** at runtime. | Have the root send `kube.ConnStateMsg(m.conn)` to a task right after pushing it and after `popTask`. Alternatively, keep the last state on `Session` and seed `conn` from it in each `New`. Fixing it once in the root covers every screen (13's area). Add a root-level test: push while offline → the task's keybar shows OFFLINE. |
| M1 | medium | `nodedetail/load.go:76-83` (load), `:236-257` (poll); `update.go:63-71` (reload on any Pod/Node event), `:177-185` (250ms retry while unsynced) | Each 2s poll lists **cluster-wide** pod metrics to fill one node's rows. Every debounced Pod or Node event anywhere in the cluster also reruns `load()`, which repeats that list and adds a `NodeMetrics` list. The 250ms `KindsSynced` retry loop does the same on every retry. **(proved)** 5 pod events produced 5 `PodMetricsByNamespace("")` lists and 6 `NodeMetrics` lists, on top of the poll. On a 5k-pod cluster that is several MB from metrics-server every 2s, plus more per event, for a table of ≤110 rows. | Make `load()` cache-only: keep the last poll's `podMetrics`/`used` on the model and re-apply them to the rebuilt rows. Restrict the poll to the namespaces actually present on the node (usually a handful of per-namespace Lists), or poll pod metrics less often than the node's own usage. Add a counting-seam test like the proof. |
| M2 | medium | `overview/update.go:16-19`, `:24-27`, `:61-63`; `load.go:45-58`, `:96-115` | `ResourceChangedMsg` for Node, Pod, Namespace, ReplicaSet or HelmRelease calls `m.load()` directly, with no debounce and no epoch bump. **Effect 1: metrics on every event.** Each load re-projects every node, pod and ReplicaSet, and makes a live `NodeMetrics` round trip. **Effect 2: stale replies win.** All in-flight loads share one epoch, so the slowest reply is applied last, even if it's the oldest. **(proved)** The newer reply showed 1 node, then the stale one restored 3. **Effect 3: frozen usage.** There is no metrics tick, so cpu/mem on a quiet cluster are frozen at open time. That is the inverse of "metrics poll on the sync interval only". | Copy nodedetail's shape: bump `reloadEpoch` per event, schedule a 250ms `reloadDueMsg`, and drop mismatched replies. Move `NodeMetrics` onto its own 2s tick with its own epoch, gated on `!conn.Offline()`, and keep `loadOverview` cache-only. |
| M3 | medium | `browse/nodes.go:293-299`; `nodedetail/update.go:641-652`; vs `kube/mutate.go:697`, `kube/fake/fake.go:643` | The drain confirm says "Drain X? N pods will be evicted", where N is every non-terminal pod on the node (`podCountByNode` / `len(allPods)`). `Drain` skips DaemonSet-owned and mirror pods. On a typical node that's 3–8 pods: kube-proxy, CNI, log shipper, node-exporter, and static control-plane pods. §11a explicitly requires the confirm to show "how many pods will be evicted". **(proved)** With 1 plain + 1 DaemonSet + 1 mirror pod, the label says "3 pods will be evicted", but Drain evicts 1. | Export the predicate (for example `kube.DrainEvicts(*corev1.Pod) bool`) and use it in `Drain`, the fake, `loadNodeExtras` and nodedetail. Optionally add "(+K daemonset/static pods stay)". Extend `TestDKeyConfirmsThenDrains` with DS and mirror fixtures. |
| M4 | medium | `browse/update.go:962-970`, `delete.go:90-93`; `verbs/verbs.go:198-201`, `:443-446`; `CLAUDE.md` (Task-contract paragraph); `docs/design/README.md` §11a/§11b | On the Nodes list, `D` (Delete) applies to the Node object with tier `TierFor(Delete)`, which is inline y/N outside PROD. Deleting a Node is strictly more disruptive than draining it: PodGC removes every pod bound to it, DaemonSets included. Drain is always modal, and CRD delete is already forced to `TierModal`. The key vocabulary is also inconsistent: CLAUDE.md says "`D` drain … `ctrl-d` delete", §11a/§11b say "`X` drain", and the code has Delete=`D`, Drain=`ctrl+d`. A user going by CLAUDE.md, or by the near-universal "D = drain" convention, gets "Delete node X? y/N". | Force `TierModal` (type-the-name) for `KindNode` delete, as for CRDs. Reconcile CLAUDE.md and §11a/§11b with `verbs.go`. The registry is the source of truth, so the docs should change. |
| L1 | low | `nodedetail/load.go:160-174`, `update.go:228-232`, `view.go:365-374`; `overview/load.go:98-115`, `view.go:221-228` | Any failed `NodeMetrics` call (timeout, Forbidden, a transient 503) flips USED/CAPACITY to "cpu / mem — no metrics-server installed" until the next poll. The disk row keeps the last good figure on a later failure, but cpu/mem don't, and "installed" is false for Forbidden. | Keep the last good reading on error. Distinguish "no metrics-server" (404/NotFound on the API group) from "metrics unavailable: <reason>". |
| L2 | low | `kube/podrequests.go:30-65` | `PodEffectiveRequests` ignores pod-level `spec.resources` (PodLevelResources; the k8s.io/api v0.37 field doc still says alpha, upstream beta and default-on reportedly in 1.34, **unconfirmed**). It also ignores in-place-resize status (`status.containerStatuses[].allocatedResources`/`resources`). Upstream `resourcehelper.PodRequests` uses both, so REQUESTED reads low for such pods compared with `kubectl describe node`. | When `pod.Spec.Resources.Requests` is set, use it, plus overhead. Take the max of the spec and status resources per container. Add table cases. |
| L3 | low | `kube/nodedisk.go:20-27`, `:118-128` | The disk row uses `node.fs.usedBytes`, which is statfs total−free and so excludes root-reserved blocks. The img row uses capacity−available. On ext4's default 5% reservation, the disk bar reads about 5 points below the `nodefs.available` signal the comment says it tracks. This relies on cadvisor's `Usage = total − free` semantics, **unconfirmed** at runtime. | Use capacity−available for both rows, as for imagefs. |
| L4 | low | `browse/nodes.go:255-275`; `overview/load.go:84-115` | Cluster cpu/mem % sums Capacity over *all* nodes but usage only over nodes that have metrics. A NotReady or just-joined node understates the percentage, and overview sets `metricsAvailable` if any single node reports. | Sum capacity only over nodes present in the metrics map, or append "(n/m nodes)". |
| L5 | low | `overview/load.go:129-133` | CAPACITY's pods bar counts unscheduled Pending pods (no `spec.nodeName`) against the sum of nodes' Allocatable pods. 11a/11b count only scheduled pods. | Count only `p.Spec.NodeName != ""` non-terminal pods. |
| L6 | low | `browse/nodes.go:283`, `:295` | Browse's `beginCordon`/`beginDrain` pass `verbs.Cordon.Tier`/`verbs.Drain.Tier` directly. nodedetail routes through `verbs.TierFor` (`update.go:609-617`), and its comment says skipping it is how rollout-restart's PROD escalation regressed. | Use `verbs.TierFor(verb, m.isProd())` at both browse call sites. |
| L7 | low | `nodedetail/update.go:311-312` vs `:439-440`; `browse/update.go:668-669` vs `:1187-1188` | `ctrl+u` pages up in normal mode, but its natural pair `ctrl+d` is Drain there, and only pages down while a filter is open. Paging down a node's pods opens a drain confirm. | Bind half-page-down to something that works in both modes (`pgdown` already exists), or move Drain off `ctrl+d`. |
| L8 | low | `nodedetail/update.go:257-280` | `restoreSelection` re-finds the cursor by `pod.Name` alone, but a node's pods span namespaces (`postgres-0` in two namespaces on one node), so after a reload the cursor can jump rows. Actions still use the row's full `kube.Pod`, so they hit whatever row is selected. This is a cousin of 03 H2. | Track the selection as `PodKey(ns, name)`. |
| L9 | low | `nodedetail/update.go:162-217`; `load.go:57-115` | nodedetail's `loadedMsg` carries no epoch. A slower load landing after a newer one wins, and `load()`'s own `used/usedOK` can overwrite a fresher poll's. The 250ms debounce bounds this, unlike M2. | Stamp `loadedMsg` with `reloadEpoch` and drop mismatches. Stop setting `used` from `load()` (see M1). |
| L10 | low | `overview/load.go:102-104`; `overview/view.go:223` | A stale comment says demo mode always reports `n/a` node metrics, but `fake.NodeMetrics` (`fake.go:1326-1344`) returns real values. CAPACITY cpu prints raw millicores ("45210m / 96000m"), where 11b uses `formatMilli`. | Fix the comment. Share `formatMilli`. |

## Details: high

### H1. Pushed screens never learn the connection state

`nodedetail.New` leaves `conn` zero (`model.go:310-330`). Everything that gates on the connection
reads it:

- `pollsMetrics` (`load.go:224-229`);
- the keybar's offline pill and verb hiding (`keys.go:74`, `:100`);
- the header badge (`view.go:70`, via `LiveConnBadge`);
- `actions.SetOffline`, which only runs in the `ConnStateMsg` case (`update.go:73-81`).

The root shell records every `ConnStateMsg` in `m.conn` (`tui/model.go:616`) but forwards it only
to the task that is active at that moment. A task created later by `Update` returning a new
instance (`model.go:870-876`) gets nothing.

How often this bites depends on the outage phase:

- **Connected.** Harmless. A successful `/livez` ping re-sends `ConnConnected` every 2s
  (`health.go:421`), so a fresh screen converges.
- **Reconnecting/Failed.** The next state arrives on the backoff schedule (`backoffDelay`, capped
  at 30s). Until then the new screen claims "connected", and its mutating verbs start real
  requests against a dead link.
- **ConnUnauthenticated.** The probe timer waits 24h (`health.go:339-343`, "without repeatedly
  invoking an expired credential plugin"), so no message ever arrives. The nodedetail poll then
  calls `NodeMetrics` and `PodMetricsByNamespace` every 2s, and `NodeDiskUsage` every 30s,
  through a clientset whose exec plugin just failed. kubelogin, gcloud and aws plugins are
  re-run per request after a failure. That runtime behaviour is unconfirmed here, but it is the
  scenario `browse.pollsMetrics`'s comment describes.

Overview has the same zero-state badge. It doesn't poll, but it does call `NodeMetrics` on each
load.

Proof (`zz_proof_test.go` overlay): a `New` + `SetSize` with no `ConnStateMsg` gives
`Keybar().PillText == "NODE"` and `pollsMetrics() == true`.

The fix belongs in the root: after a push and after `popTask`, return
`func() tea.Msg { return kube.ConnStateMsg(m.conn) }` batched with the task's cmd. That covers
all ~25 screens with a `conn` field, without each one reading Session.

## Details: medium

**M1. nodedetail's metrics cost scales with the cluster.**
`loadMetrics` (`load.go:236-257`) is correctly decoupled from the full reload, and its doc
comment says why. It still calls `PodMetricsByNamespace(ctx, "")`, so metrics-server serializes
every pod in the cluster every 2s. `load()` (`:76-83`) repeats that read and adds a
`NodeMetrics` read.

`load()` runs in three situations:

- on every Pod or Node `ResourceChangedMsg`, which is cluster-wide with no namespace, debounced
  to 250ms (`update.go:63-71`);
- on every 250ms retry while the caches are unsynced (`:177-185`);
- after every action result.

The proof counted 5 cluster-wide pod-metrics lists and 6 node-metrics lists for 5 events, with
no poll ticks involved. A node holds at most ~110 pods (`allocatable.pods`), typically spread
over a handful of namespaces. Per-namespace Lists over `{p.Namespace for p in allPods}` would
cut the payload by roughly cluster size ÷ node size. Making `load()` reuse the last poll result
removes the per-event cost entirely.

**M2. Overview's reloads are undebounced and unordered.**
`isOverviewKind` covers the five busiest kinds. Each event spawns a full `loadOverview`
immediately: it projects every node, pod and ReplicaSet, and makes a live `NodeMetrics` call.
Because `load()` captures `m.reloadEpoch` without bumping it, every in-flight reply passes
`applyLoaded`'s epoch check. Replies are applied in completion order, and the metrics round trip
makes that order vary. The proof applied a newer reply showing 1 node, then an older reply, and
the panel went back to 3 nodes.

With no metrics tick, CAPACITY's cpu/mem only move when an unrelated object changes. So on a
quiet cluster the numbers are as old as the last event, while 11a, a screen away, refreshes every
2s.

**M3. The drain count disagrees with the drain.**
Both drain confirms count every non-terminal pod on the node:

- browse uses `loadNodeExtras` → `podCountByNode` (`nodes.go:104-112`);
- nodedetail uses `len(m.allPods)`.

`kube.Cluster.Drain` evicts only pods that are neither DaemonSet-owned nor mirror pods
(`mutate.go:697`), and the fake matches it (`fake.go:643`). So the one number the spec asks the
confirm to show is wrong in the alarming direction on every real node. Then the success message
reports the smaller evicted count, which looks like a partial failure.

**M4. Deleting a Node is easier than draining it.**
`Delete` has no `Kinds` restriction, and browse excludes only Forward and HelmRelease
(`update.go:963`). On the Nodes list, `D` therefore opens "Delete node X? y/N" outside PROD.

`kubectl delete node` makes PodGC delete every pod bound to the node, DaemonSet pods included,
and the cluster loses the node until the kubelet re-registers. Drain is more reversible than
that, yet it always gets a confirm card, and CRD delete was special-cased to `TierModal` for the
same "infrastructure, not workload" reason.

The docs make the key collision worse: CLAUDE.md says `D` is drain, and the design spec says `X`.

## Test gaps

- **No counting-seam test for metrics traffic.** Neither nodedetail nor overview has a test
  that counts metrics calls per event or per tick, or checks the namespace argument. Either would
  have caught M1 and M2. CLAUDE.md asks for exactly this style of test ("what the app fetches")
  at the kube layer. Here it is a seam-counting test like the proof.
- **No test that a pushed or popped task receives the current `ConnState`** (H1). Today's
  offline keybar test (`TestKeybarGoesOfflineAndHidesCordonDrain`) sends the message by hand.
- **`TestDKeyConfirmsThenDrains` uses one plain pod.** It needs DaemonSet and mirror fixtures
  (M3).
- **Overview has no stale-reply ordering test** (M2).
- **E2E:**
  - no drain test;
  - only `TestNoMetricsServerRendersUnknown` on the metrics side, with nothing that installs
    metrics-server to assert 11a/11b/19a agree on a node's usage percentage, which is the
    property the Capacity rule exists for;
  - nothing exercises 11b's disk row through `nodes/proxy`, although kind's kubelet serves the
    summary API.
- **`podrequests_test.go` lacks a pod-level-resources case** (L2).
- **There is no `nodedetail` render benchmark** at a full node's 110–250 pods. It's cheap
  today, but `tableBody` builds every row, which is 03 H5's pattern at a smaller scale.

## Quick wins

1. Replay `m.conn` to the task after a push or pop in the root (H1). This is a one-line batch,
   covers every screen, and comes with one test.
2. Share a `kube.DrainEvicts` predicate between `Drain`, the fake and both confirm labels (M3).
3. Force `TierModal` for `KindNode` delete, and fix the drain/delete keys in CLAUDE.md and
   §11a/§11b (M4).
4. Overview: bump `reloadEpoch` per event and debounce, as nodedetail already does (M2, part 2).
5. Make nodedetail's `load()` cache-only: re-apply the last poll's metrics instead of fetching
   (M1, first half).
6. Use `verbs.TierFor` in browse's cordon/drain (L6), restrict the pods bar to scheduled pods
   (L5), and fix the stale demo-metrics comment (L10).
