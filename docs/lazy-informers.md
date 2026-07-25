# kute — Lazy informers & startup cost

How kute went from pulling ~22 MB before drawing anything to pulling ~4 MB, what that
cost in design changes, and what is still on the table.

Branch: `perf/lazy-informers` (8 commits, not merged). Full suite green, `-race` clean,
**zero golden fixtures changed** — this work alters *when* data loads, never how it renders.

## Contents

1. [The problem](#1-the-problem)
2. [What shipped](#2-what-shipped)
3. [Measured effect](#3-measured-effect)
4. [Design decisions worth remembering](#4-design-decisions-worth-remembering)
5. [What's left](#5-whats-left)

---

## 1. The problem

`kube.Cluster.Start` registered and started **21 cluster-wide informers** at connect and
blocked on all of them. Before first paint the app pulled every Secret, ConfigMap, Event,
ReplicaSet and ControllerRevision in the cluster, then kept watching them for the rest of
the session whether or not those screens were ever opened. Startup cost scaled with
cluster size rather than with what was on screen.

This surfaced as a 1–2 minute startup over an SSH port-forward to a private AKS cluster,
with the connection banner flapping throughout. The tunnel only made it visible; the
underlying property was there on every cluster.

Two facts from the investigation shaped everything that followed:

- **Every typed read already funnelled through `Cluster.ListRaw`** — including the
  non-registry paths (`rbac.go`, `events.go`, `CountInstances`). No typed lister access
  existed anywhere else, which made an implicit auto-start hook in `ListRaw` a complete
  backstop: no screen has to declare what it needs, and no read path can forget to.
- **`browse` already had the loading machinery.** `applyRowsLoaded` handled "empty result
  + cache not synced → stay loading, retry in 250 ms". It only needed a per-kind truth
  source instead of the cluster-wide one.

### Why not namespace-scoped informers

Considered and ruled out. `namespace == ""` is a first-class, user-reachable state (the
`a` key, §6b's namespace-grouped triage view), and ~15 consumers genuinely need
cross-namespace reads — the overview, the Nodes list, `nodedetail`, the empty-state hints,
cross-workload image history, the namespace palette's per-namespace counts, and
`routetable`'s cross-namespace Gateway resolution. Worse, a namespace switch would have
become a `SwitchContext`-shaped cache rebuild, turning today's instant filter change
(one field write) into a loading flash. It trades away features for the same win laziness
gets for free.

---

## 2. What shipped

| Commit | What it does |
|---|---|
| `df863a8` `fix(kube)` | Stop a slow link reporting a false connection outage |
| `08b85bb` `refactor(kube)` | Drive typed informers and cache reads from one kind table |
| `132f229` `fix(tui)` | Keep loading states tied to the kind actually being viewed |
| `54d5d1e` `perf(kube)` | **Start each kind's informer on first use instead of all at launch** |
| `5428db3` `perf(tui)` | Count kinds in the jump palette without loading them |
| `43202a4` `fix(tui)` | Refresh screens when their secondary resources change |
| `668464d` `perf(kube)` | Drop managedFields from informer caches |
| `914db9e` `chore(scripts)` | Add `scripts/measure-cluster-payload.sh` |

### df863a8 — the false-outage fix

The `/livez` ping ran on a 2 s interval with a 3 s timeout, sharing one HTTP/2 connection
with the initial LIST storm. On a constrained link it queued behind megabytes of body and
blew its deadline while the link was healthy — the same cluster reported ~200 ms the
moment the sync finished. Every timeout flipped `ConnReconnecting`.

Timeout raised to 10 s, and a *timed-out* probe is forgiven while caches are doing an
initial LIST (bounded at 90 s). Only timeouts: a refused connection, DNS failure, TLS
error or 401 still flips on the first ping, so the 4c unreachable-context screen appears
as promptly as ever. The rule is deliberately blind to the current phase — keying off
"we're still Connected" would let one unlucky watch error early in the sync re-arm the
banner storm behind it.

`internal/kube/health.go`, `probe.go`

### 08b85bb — one kind table

`registerWatches` held a map of 21 informers; `ListRaw` held a parallel 21-arm switch;
a comment asked humans to keep them in sync. Both collapse into one `typedKinds` table
carrying each kind's informer accessor, cache reader, GVR and scope.

**This was a safety fix, not tidying.** Reaching for a factory's `Lister()` *registers*
that informer as a side effect, so `ListRaw` was already registering all 21 — they simply
never started, because `factory.Start` was called exactly once. The first incremental
`factory.Start` would have run every one of them **with no watch-error handler attached**.
Latent while startup was all-or-nothing; a correctness trap the moment it became
incremental.

Also closed two data races the table made obvious: `ListRaw` read `c.factory` unlocked
while `SwitchContext` replaced it under `c.mu`, and `ensureDynamicKind` did the same with
`c.dynFactory`/`c.stopCh`.

`internal/kube/watch.go`, `cluster.go`, `dynamic.go`

### 132f229 — per-kind sync state

Screens asked the cluster whether *every* informer had synced to decide if an empty list
was trustworthy. Informers fill independently, so that conflates two questions — and on a
cluster whose RBAC forbids listing some rarely-watched kind, the aggregate answer is "no"
forever.

`Cluster.KindSynced(kind)` answers for one kind's own cache. `Synced()` keeps its name but
is now explicitly a connect latch. `KindSynced` reports "settled" for anything nothing will
ever deliver — a stopped cluster, a kind with no informer, and a kind whose watch came back
Forbidden — so a caller gating a spinner on it cannot hang.

`helmAwareLister` maps `HelmRelease` → `Secret`, the cache releases are actually decoded
from. Load-bearing: without it the Helm list flashes "no releases" while Secrets fill.

`internal/kube/cluster.go`, `internal/app/app.go`, `browse/model.go`,
`nodedetail/model.go`, `internal/tui/namespace.go`

### 54d5d1e — the feature

Connect starts three informers (`Namespace`, `Pod`, `Node`) and waits only on those.
Everything else starts when something first reads it, via `ensureKind` called from
`ListRaw`. The first read of a lazy kind returns an empty cache — which is exactly what
`KindSynced` reports on, so callers hold their loading state and the informer's own change
events bring them back.

Discovered CRDs got the same treatment: discovery used to open an instance watch per
Established CRD whether or not it was ever browsed.

The eager three earn their place by being needed before any navigation, or by having no
reload path if they arrive late (the Pods health strip, Nodes list, node detail and
overview all read Nodes without subscribing to Node changes).

`internal/kube/cluster.go`, `watch.go`, `dynamic.go`, `health.go`

### 5428db3 — counts without loading

The jump palette shows a count per kind and got them by measuring informer caches — so
pressing `g` would have started a watch for every registered kind. The launch stampede,
one keystroke away.

Counts now come from a one-object metadata LIST reading `remainingItemCount` (~2 KB per
kind, no watch, no cache). It works for kinds whose informers have never run, so the long
tail gets a *real* number where it previously showed whatever its unstarted cache held.

Fetched off the Update loop, memoized for 10 s because the palette rebuilds its item list
on every keystroke. A count that hasn't arrived renders as a dim en dash, never a
fabricated zero.

`internal/kube/count.go`, `internal/tui/counts.go`, `goto.go`, `browse/hints.go`

### 43202a4 — secondary-resource refresh

Most lists are built from more than the cache they're named after: Deployments' IMAGE
column compares against ReplicaSets, Ingresses' BACKENDS resolves through Services and
Pods, pod detail's EVENTS grid reads Events and hops through ReplicaSets for the owner.
Each screen subscribed only to the kind it displayed.

**Two pre-existing bugs fixed here**, independent of laziness:

- `routetable` had **no change subscription at all** — it loaded once on open and never
  again, so a backend that moved left BACKENDS wrong until you navigated away and back.
- The synchronous one-shot readers (rollout history, scale prompt) read caches from a
  keypress handler with nowhere to put a loading state.

One `auxKinds` table names each kind's secondary kinds, driving both reload and prefetch.

`browse/aux.go`, `browse/update.go`, `poddetail/update.go`, `routetable/update.go`

### 668464d — managedFields transform

Server-side-apply bookkeeping, routinely a third of an object's stored bytes, read nowhere
in this app. Stripped on the way into every cache, typed and dynamic. Safe for writes
because nothing does read-modify-write through a cache — every mutation is a patch built
from scratch and sent via the clientset.

The YAML view still shows them, fetched live for the single object on screen. A failed
fetch costs the fold and nothing else.

**Caveat worth knowing:** this saves memory and *zero network*, because the transform runs
after the reflector decodes the wire object. At ~1 k pods it's single-digit MB. It's
insurance for larger clusters, not part of the startup win. This commit is the one that
could be dropped without disturbing anything else.

`internal/kube/transform.go`, `yaml.go`, `yamlview/`

---

## 3. Measured effect

Real numbers from `aks-aim-prod-eastus2-02` via `scripts/measure-cluster-payload.sh`
(48 CRDs, 91 pods, 2 nodes, 14 namespaces):

| | before | after |
|---|---|---|
| Typed kinds at connect | 19.0 MB | **1.3 MB** |
| CRD discovery | 2.76 MB | 2.76 MB (unchanged) |
| **Total** | **≈ 21.8 MB** | **≈ 4.1 MB** |

A 5× cut. What each lazy kind now costs only if opened:

```
secrets              310 objects   12.3 MB     ← largest single kind
replicasets          284 objects    2.2 MB
controllerrevisions  111 objects    1.4 MB
jobs                  49 objects  358.9 KB
clusterroles         101 objects  370.0 KB
deployments           33 objects  324.4 KB
```

---

## 4. Design decisions worth remembering

- **Auto-start is implicit, inside `ListRaw`.** The alternative — every caller declaring
  its kinds — meant touching ~62 call sites where a *missed* one fails silently as an
  empty list. Implicit is correct by construction and needs suppression at exactly two
  breadth-first consumers (the goto palette, the empty-state hints).
- **`ensureKind` holds `c.mu` across `factory.Start`.** The two-phase alternative has a
  real interleave: goroutine B's `Start` runs A's just-registered informer before A wires
  its handler, and `SetWatchErrorHandler` then fails outright on a running informer.
- **The connect-grace window re-arms on every informer start**, not at connect. Lazy starts
  move LIST contention mid-session; anchoring at connect would leave "open Secrets on a
  slow link" unforgiven and paint an offline banner over a healthy cluster.
- **`KindSynced` reports true for things that will never arrive** (stopped cluster,
  no informer, Forbidden). A spinner gated on it cannot hang.
- **Counts are never fabricated.** Unknown renders as `–`, not `0`.

### Verification notes

Three claims were checked rather than asserted:

- The race test genuinely fails on the old unlocked `c.factory` read (confirmed by
  reverting the lock and observing `WARNING: DATA RACE`).
- The namespace-palette test genuinely hangs on "loading namespaces…" with the old
  aggregate gate (confirmed by reverting the gate).
- The first end-to-end transform test **passed while proving nothing**, because the test
  helper built a factory without the transform. Both factories now come from one
  `newTypedFactory` constructor so production and tests can't drift again.

---

## 5. What's left

### 5.1 CRD discovery — 2.71 MB of pure waste (highest value)

CRD discovery is now **68% of what a connect pulls**, and **98.2% of it is OpenAPI
validation schemas kute never reads**:

```
CRDs:            48
transferred:     2.76 MB
actually used:   50.4 KB  (1.8%)
schema overhead: 2.71 MB  (98.2%)

largest CRDs:
    367.3 KB  prometheusagents.monitoring.coreos.com
    319.6 KB  prometheuses.monitoring.coreos.com
    250.0 KB  alertmanagers.monitoring.coreos.com
    237.1 KB  thanosrulers.monitoring.coreos.com
    236.0 KB  scrapeconfigs.monitoring.coreos.com
```

`ParseDiscoveredKind` reads only `spec.group`, `spec.names.{kind,plural}`, `spec.scope`,
`spec.versions[].{name,served,storage,deprecated,additionalPrinterColumns}` and
`status.conditions`. It never touches `spec.versions[].schema`.

**A cache transform cannot fix this** — it runs after the reflector decodes, so the 2.76 MB
still crosses the wire. The server has to not send it, and there's no field projection for
CRDs beyond `PartialObjectMetadata` (which strips `spec` entirely, losing printer columns,
names and scope).

**Proposed approach.** The discovery API (`/apis`) returns group, version, kind, plural and
namespaced for every served resource in a few KB — most of what `ParseDiscoveredKind`
builds, and the Established filter comes free since discovery only lists served resources.
The one missing piece is `additionalPrinterColumns`; fetch that per-CRD, lazily, when a
kind is actually opened, falling back to neutral NAME/AGE columns until then — which
§14a's spec already sanctions ("printer columns → columns, Ready-style condition → status,
**else neutral**"). Kinds you never browse cost nothing.

Estimated: CRD discovery **2.76 MB → single-digit KB**, putting connect at ~1.3 MB total.

Touches `internal/kube/discovery.go`, `dynamic.go`, `cluster.go`, and the CRD column path.

### 5.2 Helm releases read every Secret (small, self-contained)

`helmAwareLister` decodes Helm releases from *all* Secrets, so listing releases pulls
12.3 MB on this cluster to find the `helm.sh/release.v1` ones. A field-selector-scoped read
(`type=helm.sh/release.v1`) cuts it to near nothing. Worth doing regardless of §5.1.

`internal/app/app.go`, `internal/kube/helm.go`

### 5.3 Known trade-offs accepted, not bugs

- **The fuzzy `g` corpus is smaller from a cold session.** `gotoResourceItems` lists
  *objects*, not counts, so §5's count fix doesn't cover it — typing a pod name into `g`
  searches only started kinds. Mitigation if it bites: rank un-started kinds by name-match
  on the *kind*, so the jump to that list is still one keystroke.
- **Opening the CRDs list starts a watch per discovered kind**, to keep its COUNT column
  truthful. Strictly better than the old behaviour (which opened them at connect
  regardless), but the column really wants `CountLive` rather than a cache length.
- **Opening the Secrets list still pulls 12.3 MB.** Inherent — you asked for Secrets.

### 5.4 Not done, deliberately

- **No CRD-LIST instrumentation in the app.** A TUI has no console to log to, and a debug
  channel for one measurement is a feature nobody asked for.
  `scripts/measure-cluster-payload.sh` answers the question from outside.
- **The managedFields fetch happens on YAML-view open, not on fold-expand.** The collapsed
  line reads `▸ managedFields (212 lines folded)` — it needs the count to render, which
  can't be known without fetching. One small GET on an explicitly-opened screen beat
  changing spec'd copy.

---

## Running the measurement

```sh
scripts/measure-cluster-payload.sh              # current kubecontext
scripts/measure-cluster-payload.sh --context X  # a specific one
```

Read-only. Needs `kubectl` and `python3`.
