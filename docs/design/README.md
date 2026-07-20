# Handoff: kute — Inverted Main-Screen Design

## Overview
A kute Kubernetes TUI: the full-width resource table is the only resting state, and navigation appears on demand via a jump palette (`g`). This replaces the previous persistent 3-pane (groups › kinds › resources) layout. The package covers the full decided surface: main table, jump palette (fuzzy + alias chips), error/disconnected states, pod detail, log view, namespace + context switching, help overlay, YAML view, destructive-action confirm, deployments + events, exec picker, first-run/empty states, nodes (list + detail), port-forwarding (picker + forwards manager + header chip), CRD support (discovered kinds, CRDs list, generic detail), loading states, incident timeline, YAML edit, scale, Helm releases, cluster overview, bulk operations (marked sets), secret decode, RBAC who-can, and ingress/Gateway-API routing tables, plus the 0.2.0 addendum: inline mutation editors (image/tag, resources, labels/annotations, configmap/secret data) and update notifications.

## About the Design Files
The file in this bundle (`Kute Spec.dc.html`, plus its runtime `support.js`) is a **design reference created in HTML** — a mockup showing intended look and behavior, not production code. It is the curated spec: one canonical mock per screen, organized in 17 numbered sections, no competing alternatives. The task is to **recreate these designs in the kute Go codebase** using Bubble Tea + Lip Gloss idioms. HTML px values are design-proportions; map them to terminal cells (see "Mapping HTML → terminal" below).

**Every screen in the file is decided — implement all of them.** Anchor ids (each has a visible badge): `#2a` `#12a` `#12b` `#6a` `#6b` `#7a` `#7b` `#5a` `#5b` `#9a` `#9b` `#11a` `#11b` `#8a` `#8b` `#10a` `#13a` `#13c` `#13d` `#14a` `#14b` `#14c` `#14d` `#4a` `#4b` `#4c` `#10b` `#10c` `#15a` `#16a` `#16b` `#17a` `#17b` `#18a` `#19a` `#20a` `#21a` `#22a` `#23a` `#23b`. Numbering is historical (exploration turns), not an order — follow the 12 section headers in the file. Rejected alternatives (a plain 2b/3a taxonomy palette without alias chips; a 13b inline forward grammar) were removed from this file — 12a/12b supersede the palette-on-open state, 13a supersedes the forward entry flow. 0.2.0 adds `#24a` `#25a` `#26a` `#27a` `#27b` `#28a` `#28b`, sourced from `v.0.2.0.dc.html`.

## Fidelity
**High-fidelity** for color, hierarchy, alignment, copy tone, and interaction model — recreate faithfully. Exact pixel sizes are approximations of a character grid; use the terminal's monospace metrics.

## Design Principles (apply everywhere)
1. Every screen shares one skeleton: **header bar · body · keybar** (and optional strip rows under the header). Errors are not a different-looking app.
2. **Data by default, navigation on demand.** No persistent nav chrome; location lives in the header breadcrumb.
3. **Never blank out data the user already has** — stale data is shown desaturated with an age stamp.
4. Errors are **inline and actionable** (retry keys, copy-error, switch context), never modal dialogs that block browsing.
5. Keybar always reflects the current mode and only shows applicable keys, grouped by intent with `│` separators; modes get a colored pill (FILTER / GOTO / OFFLINE / NO CLUSTER).

## Screens / Views

### 2a — Main table (resting state)
- **Purpose:** the default screen; 100% of body pixels on live resource data.
- **Header bar** (bg `#0e0e15`, bottom border `#26263a`): app name `kute` (purple `#a78bfa`, bold) `│` context `microk8s-cluster` (dim `#676780`) `›` namespace `nva-stage` (purple) `›` kind `Pods` (bold `#f0f0fa`) + hint `(g to jump)` (`#55556e`, small). Right-aligned: `sync 2s` (dim) and connection status `● connected · 12ms` (green `#34d17b`).
- **Health strip** (bg `#0c0c12`, border-bottom `#1c1c2c`): per-status counts — `● 32 running` (green dot), `◐ 2 pending` (yellow `#e8c74a`), `✕ 1 crashloop` (red `#ef6a6a`), `○ 1 completed` (blue `#6aa8ef`); numbers in `#d8d8e8`, labels dim. Right: `36 pods · 3 nodes`.
- **Table columns:** status dot (2ch) · NAME (flex, ellipsize) · READY (5ch) · STATUS (13ch) · ↺ restarts (4ch) · CPU (bar+pct) · MEM (bar+pct) · NODE (9ch) · AGE (right-aligned, 4ch). Column headers uppercase, `#55556e`, letterspaced; sort indicator `↑` in purple next to sorted column.
- **Rows:** 1 line each. Status dot and STATUS text share the status color (Running green, Pending yellow, CrashLoopBackOff red, Completed blue). Names `#d8d8e8`; crashlooping pod names tinted `#f0b7b7`. Restarts > 0 render yellow, otherwise dim. CPU/MEM: mini bar (track `#1c1c2c`, fill purple, fill yellow when ≥70%) + small pct in dim; `–` when metrics unavailable.
- **Selected row:** bg `#1d1633`, left border 2px/1ch purple `#a78bfa`, name brightens to `#f0f0fa`.
- **Footer of table area:** `1–9 of 36` + scrollbar glyphs (`▐▌░░░░`) in `#44445c`.
- **Keybar** (bg `#0e0e15`, top border `#26263a`): `g goto · / filter │ ↵ open · l logs · d describe · e exec │ n namespace · c context` … right: `? help`. Keys purple, labels dim.

### 2b — Jump palette (fuzzy, query typed) — see also 12b for alias re-ranking
- **Trigger:** `g` from anywhere. Centered floating panel (~54% width; bg `#101018`, border `#3b3b58`, drop shadow) over the dimmed table (dim to ~30% opacity).
- **Input row** (bg `#12101d`): `›` prompt in purple, typed query bright with purple block cursor `▎`; right hint `jump anywhere`.
- **Results:** one line each — name with **matched characters highlighted** (`#c4b5fd`, bold), then a type label (`kind · Workloads`, `pod · nva-stage`, `namespace · microk8s-cluster`) in `#55556e`, right-aligned count/status. Result types: kinds, resource names, namespaces, contexts. Selected result = same selection treatment as table rows.
- **RECENT row** (top border `#1c1c2c`): recently visited kinds as plain text. Opening the palette pre-selects the most recently visited *other* kind (alt-tab semantics, like editor Ctrl-Tab — the same grammar 6a/7a share), so `g ↵` returns to it.
- **Palette keybar:** `↵ jump · tab complete · ↑↓ move · esc close`.
- **Main keybar while open:** `GOTO` mode pill (bg `#1d1633`, text `#c4b5fd`, bold) + one-line explanation.

### 3a — SUPERSEDED by 12a (empty query = alias chips + ranked kinds). The two-column taxonomy browser below is historical; the ideas that survive are the live counts, dim zero-count kinds, and the description footer.
- Same palette shell, wider (~69%). Input shows placeholder `type to jump, or browse` in `#55556e`; right hint `all resources · nva-stage`.
- **Body:** two-column grid of all resource groups. Group headings: icon + uppercase name (`◈ WORKLOADS`, `◇ NETWORKING`, `⚙ CONFIG`, `▤ STORAGE`, `⬡ CLUSTER`, `∿ OBSERVABILITY`) in `#55556e`. Kind rows: name `#d8d8e8` + right-aligned live count `#55556e`. Zero-count kinds dim to `#44445c` with count `#33334a`. Selected kind = purple selection treatment.
- **Description footer** (top border `#1c1c2c`): one line for the highlighted kind — name in purple, then `— running application instances · last visited 2m ago` in dim.
- **Keys:** `↑↓←→ browse · ↵ jump · type to narrow · esc close`. Typing any character switches to the 2b fuzzy state.

### 4a — Connection lost mid-session
- Header status becomes `◌ disconnected` (red).
- **Error banner** under header (bg `#2a1518`, border-bottom `#4a2228`): `▲` red icon, verbatim error `watch stream lost · dial tcp 10.0.0.5:16443: i/o timeout` in `#f0b7b7`; right: `retry 3 · next in 4s` (`#c98a8a`) + keys `r retry now`, `c switch context` (keys `#ef8a8a`).
- **Stale strip** replaces health strip: `⧗ showing snapshot from 10:24:31 · 94s old` in yellow; right `counts frozen · 36 pods`.
- **Table:** identical layout, rendered desaturated/dimmed (HTML uses saturate(0.35) + 66% opacity; in terminal, substitute the dimmed color ramp — e.g. statuses drop to muted variants, text one step darker). Browsing/selection still works.
- **Keybar:** `OFFLINE` pill (bg `#2a1518`, text `#ef8a8a`), keys `r retry · c context · ↑↓ browse snapshot`; right note `mutating actions disabled`. Delete/exec/edit verbs are disabled while offline.
- Auto-retry with visible countdown and attempt counter; `r` retries immediately.

### 4b — RBAC / API error on one kind (403)
- Header stays green/connected — the error is scoped to the kind, not the app.
- **Body:** centered card (bg `#16121a`, border `#3a2a30`, radius): title `403 Forbidden` (red, bold) + `secrets.v1 · list` (dim). Verbatim message with entity names highlighted in yellow: `User "dev-readonly" cannot list resource "secrets" in namespace "nva-stage"`. Below a divider, recovery actions each on one line: `g jump to another kind — everything else still works`, `c switch context — prod-eks grants secrets:list`, `y copy error — paste to your cluster admin`, `r retry`.
- Under the card: `last successful list: never · RBAC errors are not retried automatically` in `#44445c`.
- **No auto-retry on 4xx** — auto-retry is reserved for network failures (4a).

### 4c — Cluster unreachable at launch
- Header: context name + `◌ connecting failed` (red). No data exists, so recovery paths ARE the screen (centered, ~58% width):
  - Title row: `✕ microk8s-cluster is unreachable` (bold) + right `retrying in 8s · attempt 2`.
  - Raw error in a red-tinted box (bg `#16121a`, border `#3a2a30`, text `#c98a8a`).
  - `SWITCH CONTEXT` section: bordered list of all kubeconfig contexts, **pre-probed in the background** with reachability + latency — `✕ microk8s-cluster (current · timeout)`, `● prod-eks (reachable · 32ms)` (selected), `● kind-local (reachable · 4ms)`. Failing context stays listed.
  - Keys: `↵ connect to selected · r retry now · e edit kubeconfig path · q quit`.
- **Keybar:** `NO CLUSTER` pill + `probing other kubeconfig contexts in the background`.

### 5a — Pod full view (↵ on a table row)
- Breadcrumb extends: `… › Pods › nva-worker-9k2ss` (pod name bold).
- **Title row:** pod name (bold, 14px-equivalent = emphasized) · status `✕ CrashLoopBackOff` (red) · `↺ 6 restarts` (yellow) · right `watching · live` (dim).
- **Meta grid** (label over value): NODE / IP / QOS / CONTROLLER / AGE. Controller is a link (`deploy/nva-worker ↗` in purple) that jumps via the goto machinery.
- **Last-termination banner** (promoted to top, bg `#2a1518`, border `#4a2228`): `Last termination` (red bold) + `exit 137 · OOMKilled · 4m ago`; body line names the container (yellow) and the memory limit + next backoff. This answers "why is it broken?" first — never bury it.
- **CONTAINERS:** grid — status glyph · name · image (dim, ellipsize) · state (`Waiting · backoff` red / `Running` green) · restarts right-aligned.
- **CPU/MEM bars** with `used / limit` text; MEM at 96% renders the bar and text red.
- **EVENTS (newest first):** grid — type (`Warning` yellow/red, `Normal` blue) · reason · age · message (ellipsize).
- **Right sidebar** (~25% width, bg `#0a0a0f`, left border): LABELS (`key=` dim, value bright), RELATED (purple links with `↗`: owner deploy, rs, svc, configmaps), TOLERATIONS.
- **Keybar:** `esc back · j/k next/prev pod │ l logs · e exec · y yaml · ctrl-d delete │ tab cycle container · ? help`. `j/k` moves through the table's pod list **without leaving detail view**.

### 5b — Log view (l from table or detail)
- Breadcrumb: `… › nva-worker-9k2ss › logs · worker`; right status `▶ following` (green).
- **Toolbar strip:** `container worker (tab: metrics-sidecar)` · `since 15m` · `wrap on` · `timestamps on`; right: `12 WRN · 3 ERR in view` (counts colored).
- **Stream** (bg `#08080d`): `HH:MM:SS` timestamp `#44445c` · level token colored (`INF` green, `WRN` yellow, `ERR` red) · message `#8a8aa2`; ERR lines get message text `#f0b7b7` and a full-width red-tinted row (`#2a1518`) for the most significant one. **Restart boundaries** drawn inline as a centered rule: `─── container restarted · restart 6 · 10:24:02 ───` in `#55556e`.
- Bottom status line: `▶ live · 2 new lines/s` (green) with cursor.
- **Behavior:** follow by default; scrolling up auto-pauses, `space` toggles; `w`/`e` jump to previous/next warning/error; `/` filters the live stream in place using the same filter grammar as the table; `tab` cycles containers; `s` changes since-window; `ctrl-y` copies the visible view.

### 6a — Namespace palette (`n`)
- Same palette shell as goto (2b), scoped to namespaces; opens over the dimmed table. Input prompt `ns ›`, right hint `microk8s-cluster · 6 namespaces`.
- **Columns:** selection glyph · NAMESPACE (current one tagged `current` in `#55556e`) · count column for the kind the table is currently showing (header names the kind — `PODS`, `DEPLOYMENTS`, `INGRESSES`, …; falls back to `PODS` for a cluster-scoped active kind — Nodes, Namespaces, Forwards — or before the first navigation) · HEALTH (inline colored glyph counts: `●32 ◐2 ✕1`, tallied for that same kind) · CPU share right-aligned (always pod CPU usage, independent of the active kind — metrics-server has no other kind's usage to report). Zero-count namespaces dim (`#44445c`) but stay listed.
- **`all namespaces` is a first-class last row** (blue `#6aa8ef`, glyph `∗`), separated by a top border — reached via `↑↓`/`↵`, not a dedicated key: `a` types into the query like any other letter, so filtering to a namespace starting with "a" isn't shadowed by an all-namespaces jump.
- **RECENT row:** last-visited namespaces. Opening the palette pre-selects the most recently visited *other* namespace (alt-tab semantics, like editor Ctrl-Tab), so `n ↵` — the same two keystrokes as the old double-tap — toggles straight back to it; typing goes anywhere else.
- **Numbered recent pick:** current and the immediately-previous namespace (the row tagged `previous` — the alt-tab target above) are both excluded from the numbered pick and from the RECENT row's own list: each already has its own on-row tag, so a digit for either — or repeating either in RECENT's text — would be redundant. Every recent namespace *after* that gets a 1-based digit, most-recent-first, capped at `1`-`9`. The digit renders directly on the row itself, in the same leading gutter cell the selection arrow occupies — the row IS the legend, no separate lookup needed. Typing that bare digit as the query's only character jumps `Sel` straight to it and shows a "`↵` switches to *name*" footer; it's a tentative selection, not an instant jump — `↵` still commits it. Any further typed character makes the digit "just the query's first character" again and reverts to plain fuzzy filtering, so e.g. `2048` still filters to a namespace named that. Same grammar as 7a's.
- **Recents float to the top** of the empty-query list, ahead of the rest (which keep their existing order): current first, then `previous`, then the numbered `1`-`9` recents in digit order — regardless of where they'd otherwise sort alphabetically. Once a query is typed, ordinary fuzzy-match ranking takes over instead. `all namespaces` still stays pinned as the last row regardless.
- Switching keeps kind + filter. Keys: `↵ switch · ↑↓ move · 1-9 recent · esc close`.

### 6b — All-namespaces mode
- Header scope segment renders `∗ all namespaces` in blue — scope is never ambiguous. Health strip shows cluster-wide counts + `125 pods · 6 namespaces`.
- **Rows grouped by namespace** (no NAMESPACE column): group header line (bg `#0e0e15`) = `▾` + namespace name (`#c4b5fd`) + pod count + right-aligned trouble chips (`◐2 ✕1`).
- **Triage default:** unhealthy pods first within groups; fully-healthy namespaces collapse to a single line (`▸ nva-prod · 54 pods · all running` in green). Partially-shown groups end with `+ N running · ↹ expand` in `#44445c`.
- Keys: `↹` expand/collapse group · `↵` open · `n` namespace palette · `N` jump into the selected pod's namespace (scoped mode, pod still selected). Keybar pill `ALL NS` (bg `#12203a`, text `#8ab8ef` — blue, not purple).

### 7a — Context palette (`c`)
- Third instance of the palette shell, one breadcrumb level up. Prompt `ctx ›`; right hint `~/.kube/config · 5 contexts`.
- **Columns:** glyph · CONTEXT (current tagged) · NAMESPACE (the context's remembered namespace) · STATUS right-aligned.
- **Reachability probed lazily on open:** `● 12ms` green, `◌ probing…` yellow, `✕ unreachable` red (row dims but stays selectable).
- **PROD tag** (border `#4a2a2a`, text `#ef9a9a`, 10px) comes from a kubeconfig annotation — it drives the 8b confirm escalation. (Implementation deviation, mvp-plan.md decision #2: sourced from `~/.config/kute/config.yaml`'s `prodContexts` list instead, never a name heuristic.) Opening the palette pre-selects the most recently visited *other* context (alt-tab semantics), so `c ↵` toggles straight back to it. Key `r` re-probes; `ctrl+p` marks/unmarks the selected context PROD — a ctrl-chord, not a bare letter, since `p`/`P` are common leading characters in prod context names (`prod-eks`) and must keep reaching the fuzzy query — writing straight back to that config file so it's picked up by every other kute session reading it, and the palette stays open with the toggled row still selected.
- **RECENT row + numbered pick:** same as 6a's — last-visited contexts on a RECENT row (current and the `previous`-tagged context excluded, same redundancy rule), each remaining one also carrying its digit directly on the row (leading gutter cell), and typing a bare `1`-`9` jumps `Sel` to that entry with a "`↵` switches to *name*" footer, degrading back to plain fuzzy filtering the moment a second character arrives.
- **Recents float to the top**, same as 6a's: current, then `previous`, then the numbered `1`-`9` recents in digit order, ahead of every other context (which keep their existing order) — only in the empty-query state; a typed query ranks by fuzzy match instead.
- Each context remembers its own namespace + kind + filter; switching restores them. Keys: `↵ switch · ↑↓ move · 1-9 recent · r re-probe · ctrl+p mark prod · esc close`.

### 7b — Help overlay (`?`)
- Floating panel (~79% width) over dimmed table. Header: `? help` + `keys for <View> view · globals below` + version right-aligned.
- **Three columns, ~6 rows each:** column 1 = current view's keys (swaps per view: PODS VIEW, LOGS…), columns 2–3 fixed = SCOPE (`g n c a / ↵ toggles last`) and GLOBAL (`↑↓ jk`, `esc`, `p pause sync`, `r reconnect`, `?`, `q`). Column headings `#c4b5fd` uppercase over `#1c1c2c` rule.
- Must fit without scrolling — if the key map outgrows one screen, cut keys, don't paginate. Read-only; app keeps syncing underneath. Close on `?` or `esc`. Keybar pill `HELP`.

### 8a — Manifest / YAML view (`y` on any object)
- Breadcrumb: `… › <object> › YAML`; right status `● live · updates as object changes`. Info strip: kind + name, `resourceVersion` (ticks live), and what's folded.
- **Line-numbered gutter** (`#33334a`, right-aligned) + syntax-colored YAML: keys `#c98fde`, string values `#b8d78f`, punctuation `#55556e`, numbers/warn values `#e8c74a`.
- **Noise folded by default:** `managedFields` and verbose status blocks collapse to one dim line — `▸ managedFields (212 lines folded)` in `#44445c`. `↹` fold/unfold at cursor, `f` show all.
- Cursor line highlighted (bg `#1d1633` across gutter + content). Read-only in MVP; `Y` copies full YAML to clipboard; `/` searches. Keybar pill `YAML`.

### 8b — Destructive-action confirm (`ctrl-d` delete)
- **Two tiers of friction:** non-prod contexts = inline `y/N` prompt in the keybar (no modal). PROD contexts (tag from 7a) = centered modal with **type-the-name confirmation**; `↵` stays dead until the typed name matches (show `7/16` progress in `#44445c`).
- Modal: **the only red-bordered surface in the app** (border `#5c2a2a`; header bg `#1a1014`, title `✕ delete pod` red bold, `PROD CONTEXT` tag right).
- Body answers "what actually happens": owner (`Deployment/nva-worker — will be recreated` in green), grace period (`30s`), and the harder chord for force delete (`ctrl-k`, grace 0).
- Keys: `↵ delete (when name matches)` (key rendered red) · `esc cancel`. Keybar pill `CONFIRM` (bg `#2a1418`, text `#ef9a9a`).

### 9a — Deployments list (exemplar for every non-pod kind)
- **Not a new screen** — the pods skeleton (header · summary strip · table · keybar) with kind-specific columns: status glyph · NAME · READY · ROLLOUT · IMAGE · AGE. Unhealthy-first sort + `+ N stable` collapse carry over. Services, configmaps, etc. follow the same recipe.
- ROLLOUT column: `stable` dim · `2m 14s ▸` yellow while progressing (IMAGE shows `new ← old` during transition) · `degraded` red.
- Keys: `↵` = the deployment's pods (pods view with pre-applied filter, not a new view) · `r` rollout restart (non-destructive, no confirm — moved off `R` in 0.2.0 to make room for 25a's resources editor on the same row) · `R` resources editor (25a) · `y` yaml · `g` goto. Keybar pill `DEPLOY`.

### 9b — Events view (`e`)
- Summary strip: `▲ 4 warnings` (yellow) · `○ 31 normal` · right `last hour · deduped · warnings first`.
- **Deduped, not a firehose:** repeats collapse to one row with a count column (`×41`). Columns: type glyph · REASON·OBJECT (two-line cell: reason colored by severity, object `pod/name` under it in `#676780` 11px) · MESSAGE (widest, verbatim) · × count · LAST right-aligned.
- Red reserved for events tied to an actively-failing object (BackOff on the crashlooping pod); other warnings yellow. Normal events fold into one group line (`▸ normal · 31 events — Pulled · Created…`), `↹` expands.
- Keys: `↵` go to object (its table, row selected) · `w` warnings only · `t` time window · `/` filter. Events are a routing layer, not a dead end; also reachable per-object from pod detail.

### 10a — Exec container picker (`x` on a pod)
- **Skipped entirely for single-container pods.** Small centered panel: header `exec › <pod>` + `2 containers`.
- Rows: container name (+ image in `#55556e`; sidecars labeled `sidecar`) · state (`● running`) · detected shells right-aligned (`sh, bash` — bash preferred).
- **`will run` line** above the keybar shows the exact command: `kubectl exec -it <pod> -c <container> -- bash` in `#9a9ab2` — no magic, copyable documentation.
- On `↵`: **kute suspends and hands the tty to kubectl exec** (like git → editor); no embedded terminal emulator in MVP. Exit returns to the same pod. Keybar pill `EXEC`.

### 10b — First run · no kubeconfig
- The only screen with the wordmark (ASCII-art logo, `#33334a`) + tagline + version. Header shows `○ no cluster` in the connection-state slot.
- **`LOOKED IN` box** (border `#1c1c2c`, bg `#0c0c12`): each path checked with why it failed — `✕ $KUBECONFIG — not set`, `✕ ~/.kube/config — no such file`.
- One dim line of provider hints (`aws eks update-kubeconfig · gcloud … · microk8s config`). No in-app wizard.
- Keys: `r retry` (after fetching credentials) · `k enter kubeconfig path` · `q quit`. Keybar pill `SETUP` (neutral gray `#1c1c2c`/`#9a9ab2`).

### 10c — Empty namespace (connected, zero pods)
- **Distinct from error states:** header stays green/connected, table column header still renders — the app is fine.
- Centered body: `no pods in <ns>` + explainer (`the namespace exists and you can read it — there's just nothing here`) + three ways out, **each with live data**: `n switch namespace — nva-stage has 36 pods` · `a all namespaces — 125 pods cluster-wide` · `g other kinds — this namespace has 2 configmaps, 1 secret`.
- Keybar: normal `PODS` pill + `0 pods · watching — new pods appear live` (new pods appear without refresh).

### 11a — Nodes list (cluster-scoped)
- Namespace segment **drops out of the breadcrumb** (`… cluster › Nodes` + small `cluster-scoped` tag). Summary strip: `● 3 ready · ◐ 1 pressure · ◈ 1 cordoned` + right `5 nodes · 125 pods · cluster cpu 46% · mem 71%`.
- Columns: glyph · NAME (control-plane role tagged inline) · STATUS (`Ready` dim, `MemPressure` yellow, `cordoned` blue `◈`) · PODS `62/110` · CPU bar+pct · MEM bar+pct · VERSION · AGE. Bars are block glyphs (`▮▮▮▮▯▯`), colored yellow only when hot (mem 91%). Version skew flagged with a quiet yellow `▲`.
- Keys: `↵` node detail · `C` cordon/uncordon (reversible, no confirm) · `D` drain (evicts workloads → routes through the 8b confirm, showing how many pods will be evicted) · `y` yaml. Keybar pill `NODES`.

### 11b — Node detail (↵ from nodes list)
- Same detail recipe as 5a: facts panel · related-objects table · keybar.
- **Top half, two columns:** CONDITIONS (Ready green; active pressure yellow with kubelet message + age; inactive conditions dim `false`) │ ALLOCATED/ALLOCATABLE (cpu/mem/pods as bar + `used / total` text, hot values yellow) + TAINTS.
- **Bottom half: the node's pods** — the pods table filtered to this node, **sorted by memory** so the greedy pod sits on top of a MemoryPressure node. Columns: glyph · NAME · NAMESPACE · MEM · CPU · AGE. `↵` opens the pod (5a) — node → culprit → detail is three keys.
- Keys: `↵ open pod · C cordon · D drain · e node events · esc back`. Keybar pill `NODE`.


### 12a — Goto palette on open (alias letters)
- Replaces the plain empty-query state: pressing `g` shows the ~8 daily kinds ranked first, each with its **first letter highlighted** (`#c4b5fd`, bold) as the alias indicator — no chip glyph, no gutter column: `P`ods · `D`eployments · `S`ervices · `I`ngresses · `N`odes · `C`onfigMaps · `E`vents. Below them, unhighlighted kinds (StatefulSets, Jobs…) and a `+ 13 more kinds · type to narrow` line in `#44445c`.
- Right-aligned live counts; selected row = standard selection treatment, its count in purple.
- Footer line: `alias — colored first letter · typing it pins that kind to rank 1 · ↵ jumps`. Keys: `type to narrow · ↑↓ move · ↵ jump · esc close`.
- **The highlighted letter IS the documentation** — no help page for aliases. Invariant: every alias IS the kind's first letter (the palette captures all input while open, so keybar keys like `n`/`c` cannot collide). A kind only gets an alias if its first letter is free among the aliased set.
- Aliases exist ONLY on the ~8 built-in daily kinds — never on CRDs or the long tail.

### 12b — Alias typed (`g d ↵`)
- Typing an alias letter **re-ranks, never fires instantly**: the aliased kind pins to rank 1 with label `alias match` in purple (the typed letter doubles as the fuzzy-match highlight); normal fuzzy matches (DaemonSets, pod names, namespaces) continue below unchanged.
- Footer confirms the destination: `↵ jumps to Deployments in nva-stage — namespace and filter carry over`.
- Whole jump is three keys (`g d ↵`), no modifier, no chord timing. Any second character makes it a plain fuzzy query, so "no" still finds Nodes and NetworkPolicies.
- Jump always lands in the **current namespace**; cross-object jumps stay in detail-view RELATED links — one rule per mechanism.

### 13a — Port-forward picker (`f` on a pod/service/deployment row)
- Same small centered panel recipe as the exec picker (10a). Header: `⇄ forward › <object>` + `pod · nva-stage · 2 ports`.
- Columns: status glyph · PORT · NAME (+ `container <name>` in `#55556e`) · LOCAL right-aligned. Ports come from containerPorts (pods), spec.ports (services), or the pod template (deployments).
- **Local port is edited in place** on the selected row (`localhost:8080▎` with purple cursor) — no separate field. Pre-fills the first free port ≥ the remote (80 → 8080 since 80 is privileged; 9090 → 9090). If the pre-fill is busy, the row says so inline (`8080 busy → 18080`) and pre-fills the next free one.
- `will run` line above the keybar: `kubectl port-forward pod/<name> 8080:80 -n <ns>` — copyable documentation, no magic.
- Keys: `↵ start · type local port · ↑↓ port · esc cancel`. Single-port objects still show the panel (the local port is a real decision, unlike exec's shell).

### 13c — Forwards manager
- **A registry kind, not a bespoke screen** — rendered by the shared list skeleton, reachable via `g` ("fo" ↵); individual forwards fuzzy-match in the palette by port and target name. Breadcrumb: `… › ⇄ Forwards` + `all namespaces` tag (forwards are global, never namespace-filtered).
- Summary strip: `● 3 active · ◌ 1 reconnecting` + right `4 forwards · 2 namespaces · forwards end when kute exits`.
- Columns: glyph · LOCAL (`localhost:8080`) · TARGET (`pod/name:port` + port name dim; svc/deploy targets show the **resolved backing pod** inline: `svc/postgres:5432 → pod/postgres-0`) · NAMESPACE · UPTIME · TRAFFIC right-aligned (`41 KB/s`, or `idle 12m` in dim — makes stale forwards safe to kill).
- Failing forward: yellow `◌` row, LOCAL goes yellow, TARGET carries the verbatim error + retry state (`pod restarted · retry 2 · next in 4s`) — same backoff/countdown discipline as 4a; the row keeps its slot. Svc/deploy forwards **re-resolve to a fresh pod** before retrying.
- Keys: `↵ open target · y copy url │ x stop · r restart · X stop all │ f new forward · esc back`. `x` executes immediately (reversible); only `X stop all` gets the inline y/N. Keybar pill `FORWARDS`.

### 13d — Ambient header forward chip
- **Zero chrome when no forwards exist** — the header is untouched.
- With active sessions, a quiet chip appears right of the breadcrumb, left of connection status: `⇄ 3` in purple (`#a78bfa`) — awareness, not a control; the route to act is `g → forwards` (13c).
- One reconnecting: the chip goes yellow — `⇄ 2 · ◌ 1` in `#e8c74a`. A dying forward **never** modals or banners over unrelated screens; the manager row carries the details.
- Forwards are session-scoped (quit tears them down) and global across context switches — the chip is global, not per-context.

### 14a — Custom resource list (exemplar: Certificates)
- Same skeleton as pods. Breadcrumb kind segment carries the API version tag: `Certificates` + `cert-manager.io/v1` in `#44445c` 11px.
- **Columns come from the CRD's `additionalPrinterColumns`** — kute never guesses. NAME and AGE always present; the rest as declared (here: READY · SECRET · ISSUER), ellipsized.
- **Status derivation is conditions-based:** a Ready/Available-style condition maps to the standard glyphs (`✕ False` red, `◐ False`-but-Issuing yellow, `● True` green) and drives the summary strip (`status from Ready condition` noted right). Unhealthy-first sort + `+ N ready` collapse carry over.
- **Fallback (no printer columns, no conditions):** NAME + AGE only, neutral `·` glyph in `#55556e`, strip drops counts and says `no status semantics · NAME + AGE only` — never fake health.
- Verbs from the command registry — only generic ones apply: `↵ open · y yaml · e events · ctrl-d delete · / filter`. Delete follows the 8b prod policy. Keybar pill = short kind name (`CERTS`).

### 14b — CustomResourceDefinitions list
- Cluster-scoped: namespace drops from the breadcrumb + `cluster-scoped` tag, same rule as Nodes (11a). Strip: `● 27 established · ◐ 1 installing` + `28 definitions · 9 API groups · sorted by group`.
- Columns: glyph · NAME (plural, lowercase) · GROUP · VERSIONS (served versions; deprecated ones dim) · SCOPE (`Namespaced`/`Cluster`) · COUNT (live instance count, right) · AGE.
- Status glyph = the CRD's **Established condition**; freshly-applied CRDs show `◐` until the API serves them. Zero-count kinds render dim.
- **`↵` jumps straight to that kind's instance list (14a)** — CRDs are a routing layer, like events. Keys: `↵ open instances · y yaml · / filter · g goto`. Keybar pill `CRDS`.
- Deleting a CRD deletes all its instances — it **always** gets the type-the-name modal (8b), even outside PROD.

### 14c — Goto palette with discovered kinds
- Discovered CRD kinds join the fuzzy corpus **at connect time** (API discovery, cached per context) — no config, no plugin. `g cert` surfaces Certificates, CertificateRequests, ClusterIssuers.
- Type label's group slot carries the **API group** instead of a built-in category: `kind · cert-manager.io`; cluster-scoped kinds append `· cluster`. Custom resource **instance names** are searchable too (`api-tls` → `certificate · nva-stage` + `● ready`), same rule as pods, from the watch cache.
- **No alias chips for CRDs** — chips stay reserved for the ~8 built-ins (12a). A daily-used CRD earns rank in the untyped list via the same recency/frequency scoring as everything else.

### 14d — Generic custom resource detail (`↵` on any custom resource)
- Built only from what **every object has** — no CRD-specific layout code: title row (name · condition-derived status · apiVersion · `watching · live`), **meta grid from printer-column values** (ownerReferences and issuer-style refs render as purple `↗` links reusing the goto machinery, like 5a's RELATED), then:
- **CONDITIONS verbatim:** glyph · type · status · message · age. The message text IS the diagnosis (`Issuing certificate as Secret does not exist`) — never paraphrase it.
- **EVENTS (newest first):** the object's events, same grid as 5a.
- **If the object has no conditions and no events, `↵` skips this screen and opens YAML (8a) directly** — an empty detail screen is worse than the manifest.
- Keys: `esc back · j/k next/prev │ y yaml · e events · ctrl-d delete`. Keybar pill `DETAIL`.

### 15a — Loading a kind
- The **full shell paints in the first frame** — breadcrumb, column headers, keybar. Loading replaces only the rows, never the app.
- Header status: `◐ loading pods · 0.4s` (yellow) — a counting timer, not a fake progress bar; on timeout 4c takes over. Strip: `◐ listing pods in nva-stage…` + right `watch starts when the list lands`.
- **Skeleton rows** (7, fading opacity toward the bottom): gray pills (`#1c1c2c` name, `#16161f` cells) laid out on the exact column grid of the real table, so live data is a fill-in, not a relayout. Footer `– of –`.
- Nav keys (`g n c ?`) live immediately; row actions dark until rows exist (keybar says `row actions enable when data lands`).
- Revisiting a kind seen this session: **cached rows dimmed** (4a's stale grammar) instead of skeletons.

### 16a — Incident timeline (namespace, `t`)
- Breadcrumb `… › Timeline` + `last 30m` tag. Summary strip counts by kind of change: `⇅ 1 rollout · ↺ 41 restarts · ▲ warnings …`.
- **One clock, newest first**: events + container restarts + rollout revisions merged into a single feed; rollouts (`⇅` purple) are the visual anchors. Each line: time · glyph · object · what changed; `↵` goes to the object.
- Answers "what changed in the last 30m" during an incident — the correlation view events (9b) can't give.

### 16b — Incident timeline (object-scoped)
- Same feed filtered to one object, opened from its detail view; includes a **revision rail** (deployment revisions as a vertical rail with the current one highlighted) — the idiom Helm history (18a `h`) reuses.

### 17a — YAML edit mode (`e` inside 8a)
- Same surface as 8a; `e` turns the read-only view into a buffer editor. **managedFields + status are stripped from the buffer** (not folded); watch updates pause while editing (`✎ editing · live updates paused` yellow in header).
- Changed lines: 1-cell purple gutter bar `▎`, new value in `#e8c74a`, dim `· was …` annotation. A change strip above the keybar summarizes the running diff (`✎ 2 changed · replicas 4 → 6 · memory 512Mi → 768Mi`).
- **`ctrl-s` = server-side dry-run first**; validation errors render as an inline banner on the offending line (4a's red-tint idiom), never a modal. On success, apply + drop back to live YAML with the new resourceVersion. PROD contexts get an inline y/N on apply; delete remains the only type-the-name surface.
- resourceVersion conflict (object changed underneath): banner offers `d diff theirs · r rebase your edits · esc discard` — never silent last-write-wins.
- Keys: `ctrl-s dry-run + apply · ctrl-z undo · ↹ fold at cursor · esc discard (y/N if dirty)`. Keybar pill `EDIT`.

### 17b — Scale (`+`/`−` on a deployment/statefulset row)
- Reversible → **inline keybar prompt, never a modal** (8b's cordon tier). Prompt pre-fills current±1; typing a number replaces it; `+/−` nudge; `↵` applies; `0` = deliberate scale-to-zero ("pause this workload").
- `will run` line above the keybar: `kubectl scale deploy/<name> --replicas=N -n <ns>` — same copyable-documentation idiom as 10a/13a.
- HPA-managed workloads show `managed by hpa/<name> — scaling overridden on next sync` as a yellow note instead of blocking. Keybar pill `SCALE`.

### 18a — Helm releases (registry kind, `g "hel"`)
- Shared list skeleton. Strip: `● 3 deployed · ◌ 1 pending-upgrade · ✕ 1 failed` + `from sh.helm.release.v1 secrets`. Columns: glyph · RELEASE · CHART · APP VER · REV · STATUS (failed carries the reason verbatim: `failed · hook timeout`) · UPDATED.
- **Browsing needs no helm binary** — decoded from release secrets in the watch. Mutating verbs (`R` rollback) shell out to `helm` with a `will run` line; helm missing from PATH explained inline.
- `↵` = objects in the release (filtered tables, 9a's recipe) · `h` history (16b's rail idiom) · `v` values in the YAML viewer, read-only. Rollback inherits 8b friction. **No install/upgrade-from-repo — deliberately out of scope (CI's job).** Keybar pill `HELM`.

### 19a — Cluster overview (`g "ov"`)
- Cluster-scoped (namespace drops from breadcrumb). Strip: top-line trouble counts + `v1.30.2 · 5 nodes · 125 pods · 6 namespaces`.
- **A routing layer, not a dashboard** — two-column body, every line a selectable row whose `↵` lands on an existing screen: CAPACITY (cpu/mem/pods bars, same bar idiom) + NODES (pressure/cordoned first, `+ 3 ready` collapse) │ TROUBLE (cluster-wide unhealthy-first aggregation; empty = `nothing unhealthy · 125 pods running` in green) + RECENT CHANGES (timeline's rollout feed, cluster-wide, 30m).
- `↹` next panel · `t` timeline · `e` events. **Not the start screen** — pods table (2a) remains the resting state. Keybar pill `OVERVIEW`.

### 20a — Bulk operations (marked set)
- Works in any list. `space` marks the cursor row and advances; `*` marks everything the current filter matches (**filter-then-mark is the bulk grammar** — no range-mark chord). `esc` clears marks before it walks back a level.
- **Mark ≠ selection**: cursor keeps the purple bar + `#1d1633`; marked rows get `▪` (purple) in a leading cell + quieter `#14101f` tint. The mark column exists only while ≥1 row is marked (zero chrome otherwise, 13d's rule). Strip's first slot becomes `▪ 3 marked`; mode pill shows the count (`3 MARKED`).
- Set-applicable verbs act on the set and the keybar says so (`ctrl-d delete 3 · y/N`, key red); per-row verbs (logs, exec, `↵`) still follow the cursor. Bulk-capability is declared per-verb in the command table.
- `will run` line lists every name: `kubectl delete pod a b c -n <ns>`. Delete follows 8b: inline y/N non-prod; **PROD modal becomes type-the-count** (`type 3 to confirm`) and lists every object.
- Marks are per-view and drop on kind/namespace switch.

### 21a — Secret decode (inside the YAML view)
- Not a new screen — 8a grows secret semantics when the object is a Secret. **Masked by default**: `data` values render `•••••••• · base64 · 41 B` (never raw base64). Strip: `Secret · Opaque · 4 keys · 1 revealed` + `decoded in memory only — never logged, never on disk`.
- `x` reveals/masks the cursor key in place — plaintext in the string color + a bordered `revealed` tag; `X` reveals all behind an inline y/N. **Leaving the view re-masks everything** — reveal state never persists.
- `y` copies the decoded value of the cursor key (the only plaintext export); `Y` full-YAML copy keeps values base64. Multiline values expand to an indented block with the fold idiom. Keybar pill `SECRET`.

### 22a — RBAC who-can (registry kind, `g "who"`; `w` from a 403 card)
- **A query, not a browser**: the summary strip holds the question — `who can list secrets in nva-stage` — with `v` verb · `k` resource · `n` namespace opening the palette shell to change each slot.
- Columns: glyph · SUBJECT · KIND · VIA (`clusterrole/admin ← rb/admins`, ellipsized) · SCOPE (`namespace` dim / `cluster` blue). Resolution walks (Cluster)RoleBindings → (Cluster)Roles **from the watch cache — no server round-trip, works read-only**; wildcards and aggregated ClusterRoles resolve to the effective rule in VIA.
- **Entry from 4b**: the 403 card gains `w who-can`, arriving with the failed verb+resource pre-filled and the current user pinned as a red `✕` row whose VIA explains the closest miss (`role/viewer grants get, list on pods — not secrets`).
- `same as` strip shows the equivalent `kubectl auth can-i` (10a's idiom). `↵` opens the binding's YAML — the answer is always inspectable. Keybar pill `WHO CAN`.

### 23a — Ingress routing table (`↵` on an ingress)
- **Not a describe page** — one row per host+path → `service:port`, the join raw YAML makes you do in your head. Backends resolve live from the watch: green ● service exists + ready endpoints; red ✕ `service not found` (inline, never a tooltip); yellow ◐ `0 ready`.
- The ingress **list** earns its keep first: NAME · CLASS · HOSTS · TLS (●/–) · BACKENDS (`3 ok · 1 broken`) · AGE. Strip counts unhealthy-first.
- TLS column shows cert expiry from the referenced secret (yellow <30d, red expired); a strip above the keybar names each secret — `↵` there jumps to it (21a's secret semantics apply).
- `↵` on a route → backend Service (9a's filtered-table recipe); `y` copies the full URL. Keybar pill `ROUTES`.

### 23b — HTTPRoute / Gateway API routing table
- Gateway API splits routing across **two objects owned by different people** (Gateway = infra, HTTPRoute = app team, joined by parentRefs). Kute resolves the join both ways: the route's parent strip shows `gw/public › https:443 · ✓ accepted` (from `status.parents`); `p` opens the Gateway.
- **Attachment status lives in the list**: HTTPRoute list gains ATTACHED (`✓ gw/public` green / `✕ not accepted` red with the condition message verbatim) — a valid-but-unattached route is the #1 Gateway API footgun.
- One row per rule match → backendRef; weighted backends stack under their match (`└ same match`) with split percentages (canary weight yellow). Same ●/✕/◐ backend grammar as 23a.
- Gateway `↵` mirrors it: listeners as rows (protocol:port · hostname · TLS + expiry · `12 routes attached`); `↵` on a listener filters to attached routes.
- GRPCRoute/TCPRoute reuse the table with fewer columns; all kinds arrive via `g` discovery like any CRD.

*(0.2.0 addendum — inline mutation editors, sourced from `v.0.2.0.dc.html`)*

### 24a — Set image / set tag (`i` on a workload row)
- Same inline tier as scale (17b) — no modal. Panel opens under the row (bg `#101018`, border `#3b3b58`); container tabs across the top (`↹` switches container, sidecars labeled dim).
- **Tag-first editing:** the `image ›` field pre-fills the current ref with the cursor on the tag, repo prefix dim — the 95% case is "bump the tag." `ctrl-u` unlocks the full ref for the rename case. One verb, two depths, no separate "set tag" screen.
- **History from the watch cache, never a registry call:** a TAG · SEEN · FROM table lists this workload's own ReplicaSet revision history (rollback targets, labeled by revision) plus the same image tag seen on other workloads/namespaces (`3.4.2 · seen 40m ago · aim-prod` — the "promote what prod runs" case).
- Re-entering the current tag flips the strip to `same image — apply is a no-op; use rollout restart` and `↵` does nothing.
- `will run` line: `kubectl set image deploy/aim-worker worker=registry.aim.dev/aim-worker:3.4.2 -n aim-stage`, right-aligned `applying rolls out 4 pods`. Multi-container workloads cycle with `↹`; the will-run line always names the container.
- Keys: `↵ apply · ↑↓ pick from history · ↹ container · ctrl-u full ref · esc cancel`; footer points to 9a to watch the rollout. PROD contexts get the inline y/N on apply, per 8b's tiering. Keybar pill `SET IMAGE`.

### 25a — Resources — set limits next to live usage (`R` on a workload)
- **Key conflict resolved (0.2.0):** `R` here collided with 9a's already-shipped `R` = rollout-restart on the same Deployments-list row. Resolved in favor of this screen — rollout-restart moved to `r`; `R` now opens the resources editor on Deployment/StatefulSet/DaemonSet rows.
- The fix for the OOMKill diagnosed in 5a. Strip under the header: container tabs, `usage: p95 over the last 6h · from the metrics poll`, right-aligned failure callout (`✕ OOMKilled 4m ago at the current limit`).
- Table: FIELD · CURRENT · NEW · P95 USAGE, rows for cpu/mem request and limit. Each field's usage renders as a mini bar sourced from the metrics poll — the mem-limit row shows the bar pinned at capacity with the OOMKill context, so the new value is a decision, not a guess. No metrics → USAGE column reads `metrics unavailable` dim, editor still works.
- Typing replaces the selected field's value; `+/−` nudges by unit steps (64Mi / 50m). Values parse as k8s quantities — an invalid quantity underlines red inline and blocks `↵`, never a modal (same inline-error idiom as 17a).
- `u` unsets a field (explicit removal, since "no limit" is a real and dangerous state) — the NEW cell then renders `— none` in yellow.
- Validation: request > limit blocks inline before apply; namespace LimitRange/ResourceQuota violations surface as the server's verbatim dry-run message, same idiom as 17a. Only changed fields go into the command.
- `will run` line: `kubectl set resources deploy/aim-worker -c worker --limits=memory=768Mi -n aim-stage`.
- Keys: `↵ apply changed fields · ↑↓ field · +/− nudge (64Mi / 50m) · ↹ container · u unset field · esc cancel`. Keybar pill `RESOURCES`.

### 26a — Labels & annotations editor (`m` on any object, CRDs included)
- Two grids, LABELS · N then ANNOTATIONS · N, same key=/value/right-note column shape.
- **Joins render before you touch anything:** a label a Service selector matches carries an inline yellow `⚠ selector · svc/aim-worker`; editing that key opens with `changing this detaches 4 pods from svc/aim-worker` above the keybar and requires the inline y/N even though metadata edits are otherwise reversible. Deployment selector labels are immutable server-side — kute says so up front instead of letting the apply bounce off the API server.
- Controller-managed annotations (`deployment.kubernetes.io/revision`, `kubectl.kubernetes.io/*`) render read-only dim. Helm-owned metadata stays editable but carries a note that the next `helm upgrade` may revert it.
- Add is one row, not a form: `a` add label / `A` add annotation opens `key=` with completion from keys already used elsewhere in the namespace, `↹` jumps to the value.
- Remove: `ctrl-d` on a key + inline y/N (reversible, no type-the-name modal — per 8b's tiering).
- `will run` line, exact per verb: `kubectl label deploy/aim-worker env=staging --overwrite -n aim-stage` (`--overwrite` only appears when overwriting) / `kubectl annotate` equivalent.
- Keys: `↵ apply · a/A add label / annotation · ctrl-d remove key · y/N · y copy key=value · esc back`. Keybar pill `META`.

### 27a — ConfigMap value edit (`↵` on a key, inside a ConfigMap's Data view)
- A value-edit, not a YAML session. Strip under the header names every consumer from the watch (`deploy/aim-worker ↗ env`, `deploy/aim-gateway ↗ volume`), right-aligned `pods don't reload configmaps on their own`.
- Table: KEY · VALUE · SIZE. Short values edit in place (prior value stays visible as `was info ·` while typing). Multi-line keys show a folded summary (`▸ 48 lines · e opens the buffer editor`) — `e` opens the 17a buffer editor scoped to just that value, same dry-run-first apply.
- Two apply depths from the same row: `↵` applies without restarting anything (kute never restarts consumers on its own); `ctrl-r` chains the apply with `kubectl rollout restart` for every consuming workload and prints every command it runs.
- Conflict handling matches 17a: the patch carries the observed resourceVersion; a concurrent change surfaces the diff/rebase/discard banner.
- `will run` line: `kubectl patch cm/aim-config --type merge -p '{"data":{"LOG_LEVEL":"debug"}}' -n aim-stage`.
- Keys: `↵ apply · ctrl-r apply + rollout restart consumers · e buffer editor (multi-line) · esc discard`. Keybar pill `EDIT VALUE`. `a` add key uses the same line-insert gesture as 27b.

### 27b — Secret add key (`a` in the Data view, line-insert)
- Decode/reveal semantics inherited from 21a. Strip: `Opaque · 3 keys → 4`, right `values decode in memory only · re-masked on exit`. Existing rows stay masked (`••••••••••••••••`) — adding a key never reveals its neighbors.
- `a` appends a highlighted `+` row: type the key name, `↹` to the value, which is visible while typing (with an inline `x re-mask` hint) and shows size as `new`.
- The `will run` line itself masks the value (`"SMTP_PASSWORD":"••••••"`) — copyable documentation must never leak the secret into scrollback or a shared screen. The real patch sends the value via `stringData`; kute does the base64, not the user.
- Plaintext exists only in process memory: `x` re-masks the input row, `esc` zeroes the buffer, nothing is logged or persisted. `ctrl-v` paste is never echoed to scrollback.
- Fixing an *existing* key is the 27a value-edit flow on the same surface — adding is a distinct gesture from editing and never shows neighboring values.
- PROD contexts add the inline y/N on apply; removing a key keeps the y/N too (recoverable only if the old value exists elsewhere — the prompt says so).
- Keys: `↵ apply · x re-mask input · ctrl-v paste (never echoed) · esc discard`. Keybar pill `ADD KEY`.

### 28a — Update chip (ambient, status bar)
- Zero chrome when no update is pending (13d's rule). With one available, a quiet chip renders left of the connection dot: `↑ 0.2.1` in yellow — same "worth knowing, not urgent" hue as other passive facts; no banner, no flash, nothing steals focus mid-incident. The keybar's right slot names the key while live: `U 0.2.1 available — what's new`.
- **Check hygiene:** one GET against the releases feed per 24h, cached in the state dir; offline/airgapped → silently no chip, no retry storm. `update.check: false` in config disables it entirely (relevant behind egress-flagging proxies).
- Per-version dismissal: opening 28b for a version (or `x` skip there) hides the chip until the *next* release — it never re-nags for a version already seen. Pre-releases only surface if you're already running one.
- `U` opens 28b from anywhere.

### 28b — What's-new panel (`U` from anywhere, also `:update`)
- The changelog plus the exact upgrade command — kute never self-updates. Header: `you run 0.2.0 · latest 0.2.1 · released 2d ago`.
- CHANGELOG list: type tag (`fix` red / `new` green) + one-line verbatim description; truncates to `… 4 more · o opens release notes in browser`.
- Install-command box: `installed via` + detected package-manager chip (e.g. `homebrew`, "detected from the binary path"), right note `kute never updates itself`; below, the literal command (`$ brew upgrade kute`) with `y copies · runs in your shell, not here`.
- kute detects its own install method (brew cellar path, apt, go install, plain binary) and prints that manager's exact command — same "will run" idiom used everywhere else in the app. Plain-binary installs get the release URL instead of a command. A tool that mutates clusters must not also mutate itself mid-session.
- Empty state (opening `:update` while already current): `0.2.0 is the latest` in green + last-checked timestamp + `r` to re-check now — the only place a manual check exists.
- Keys: `y copy command · o release notes ↗ · x skip 0.2.1 — hide the chip · esc back`. Keybar pill `UPDATE`.

## Interactions & Behavior (system-wide)
- **One palette shell, three scopes:** `g` (anything) · `n` (namespaces) · `c` (contexts). Opening any of them pre-selects the most recently visited *other* entry of that scope (alt-tab semantics, like editor Ctrl-Tab), so `↵` with no typing toggles straight back to it — same two keystrokes the old double-tap used, now visible through the palette. All share the same fuzzy input, selection treatment, and keybar footer.
- `g` opens the jump palette anywhere. Empty query = alias letters + ranked daily kinds (12a); typing = fuzzy results across kinds/resources/namespaces/contexts (2b/12b). `esc` closes; `↵` jumps.
- `/` opens filter on the current view (table rows or log stream). Show `matched/total` and a "N hidden by filter — esc to clear" notice so items never silently disappear. Highlight matched characters in results.
- `↵` opens the selected resource's full view; `esc` walks back one level (detail → table; palette/filter → close).
- `j/k` and `↑↓` are synonyms for movement everywhere; in detail view `j/k` means next/prev sibling resource.
- Connection loss: switch to 4a automatically; keep the last snapshot; retry with exponential backoff and a visible countdown; disable mutating verbs. On reconnect, silently return to live and drop the stale strip.
- RELATED/CONTROLLER links reuse the goto navigation (push detail view of the target).
- **Destructive-action policy:** reversible verbs (cordon, rollout restart) execute immediately; delete = inline `y/N` in non-prod, type-the-name modal in PROD contexts; drain and force-delete get the modal always. The PROD flag comes from a kubeconfig annotation, never a name heuristic.
- `x` execs: single container → straight to shell; multiple → picker (10a). App suspends, shell takes the tty, exit restores the exact prior state.
- `e` opens events (namespace-scoped from a list view; object-scoped from a detail view). `↵` on an event navigates to its object.
- `y` opens the YAML view on any selected object, any kind.
- `g <alias> ↵` is the canonical kind jump: alias letters re-rank (pin to rank 1), never fire instantly. Aliases: p pods · d deployments · s services · i ingresses · n nodes · c configmaps · e events. First-letter highlights render only on these.
- `f` starts a port-forward from any pod/service/deployment row (13a). Sessions live in the Forwards registry kind (13c); the header chip (13d) appears only while sessions exist. Forwards die with the app, survive context switches, and never interrupt other screens on failure.
- CRD kinds are discovered per context at connect and cached; they get list (14a), detail (14d), goto (14c), and all generic verbs with zero configuration — this is the payoff of the kind registry + command table architecture.
- `t` opens the incident timeline (namespace-scoped from lists, object-scoped from detail); `↵` on a line goes to the object.
- `e` inside the YAML view enters edit mode (17a): dry-run before apply, watch paused, conflicts surfaced — never silent overwrite.
- `+`/`−` on scalable workloads opens the inline scale prompt (17b); `0` scales to zero. Reversible tier — no modal.
- `space` marks / `*` marks all filter matches / `esc` clears (20a). Set-capable verbs act on the marked set; PROD bulk delete = type-the-count modal.
- `x`/`X` reveal secret values in the YAML view (21a); re-masked on exit; `y` copies decoded.
- `w` on a 403 card opens who-can pre-filled (22a); also reachable as a registry kind via `g`.
- Helm releases browse without the helm binary; rollback shells out with a `will run` line (18a).
- Ingress/HTTPRoute `↵` opens a live routing table (23a/23b) — backends resolved from the watch, never a describe page; `p` on an HTTPRoute opens its parent Gateway.
- `i` opens the set-image/tag editor on a workload row (24a); history comes from the watch cache (ReplicaSet revisions + cross-workload image sightings), never a registry call.
- `R` on a workload row opens the resources editor (25a); 9a's rollout-restart moved to `r` to make room for it.
- `m` opens the labels/annotations editor (26a) on any object, CRDs included; selector-linked labels carry an inline join warning before you can edit them.
- ConfigMap/Secret `Data` views: `↵` edits a value in place (27a), `a` inserts a new key as a line-insert (27b); `ctrl-r` on a ConfigMap value chains the apply with a rollout-restart of every consumer.
- `U` opens the what's-new/update panel from anywhere (28a/28b), also reachable as `:update`; kute checks once per 24h and never self-updates — it only prints the detected package manager's upgrade command.

## State Management (suggested Bubble Tea model shape)
- `mode`: `browse | filter | goto | detail | logs | offline | error | noCluster` — drives keybar contents + mode pill.
- `location`: context / namespace / kind / (resource) — rendered as the breadcrumb.
- `snapshot`: last successful resource list + `fetchedAt` (for the 4a stale stamp).
- `connState`: `connected(latency) | reconnecting(attempt, nextRetryAt) | failed(error)`.
- `palette`: query, results, selection, recents (persist recents across sessions).
- `filter`: query per view; saved-view slots optional (was explored in 1b; not part of the decided scope).
- `perContext`: map of context → last namespace/kind/filter (restored on context switch), plus that context's own namespace-recents list — a namespace only exists inside its own cluster, so unlike kinds/contexts its recents aren't a single global list. `recentKinds`/`recentContexts` stay global (kinds/contexts exist across every cluster) and back their palettes' alt-tab pre-selection filtered to what the current context actually has.
- `probes`: async reachability results for contexts (latency or error), refreshed on palette open.
- `forwards`: list of {localPort, target(kind/name/port), resolvedPod, namespace, context, state(active|reconnecting(attempt,nextRetryAt)|stopped), startedAt, lastTrafficAt, bytesPerSec} — global, session-scoped, drives 13c rows + 13d chip.
- `discovery`: per-context cache of API resources (group, version, kind, scope, printer columns, established) with fetchedAt — feeds the kind registry, goto corpus (14c), and CRDs list (14b).
- `marks`: per-view set of marked object keys (cleared on kind/namespace switch); keybar + strip + confirm policy read its size.
- `reveal`: per-secret set of revealed data keys — in-memory only, cleared on view exit; never serialized.
- `whoCan`: current query {verb, resource, namespace} + resolved subject rows (from cached bindings/roles).
- `editBuffer`: YAML edit state {baseResourceVersion, text, dirty, dryRunResult|conflict} — exists only in edit mode.
- `timeline`: merged feed window (events + restarts + rollout revisions) per scope.
- `imageHistory`: per-workload tag history derived from the watch (ReplicaSet revisions + cross-namespace image sightings) — feeds 24a, no network calls of its own.
- `updateCheck`: {lastChecked, latestVersion, seenVersions} — cached in the state dir, drives the 28a chip and 28b's per-version dismissal; absent/inert when `update.check: false`.
- Watch streams update the table in place; metrics poll on the `sync` interval shown in the header.

## Design Tokens — semantic, two themes

The app supports **dark and light themes**. Implement colors as a single semantic `Theme` struct — every view renders through it; **no hex literal ever appears in view code**. The mockup HTML shows the dark theme; light values below are the decided equivalents (not naive inversions — status colors darken/saturate to hold contrast on light backgrounds).

The per-screen sections above cite dark hexes (they describe the mockup); resolve each to its semantic token via this table when implementing.

Theme selection: default = terminal background detection (`lipgloss.HasDarkBackground()` at startup), overridable by `--theme dark|light|auto` and a config-file key. Don't use `lipgloss.AdaptiveColor` per-color — the struct swap is the mechanism, so an explicit override is trivial.

| Role | Dark | Light |
|---|---|---|
| `Bg` app background | `#0b0b10` | `#f7f7fa` |
| `BgChrome` header/keybar | `#0e0e15` | `#eef0f4` |
| `BgStrip` summary strips | `#0c0c12` | `#f2f3f7` |
| `BgLog` log stream | `#08080d` | `#fbfbfd` |
| `BgPalette` palette/modal | `#101018` | `#ffffff` |
| `BgPaletteInput` | `#12101d` | `#f4f1fa` |
| `Border` primary | `#26263a` | `#d5d7e0` |
| `BorderSubtle` | `#1c1c2c` | `#e4e6ee` |
| `BorderPalette` | `#3b3b58` | `#b9bcd0` |
| `Text` bright | `#f0f0fa` | `#14141c` |
| `TextPrimary` | `#d8d8e8` | `#2a2a38` |
| `TextSecondary` | `#9a9ab2` | `#565668` |
| `TextDim` | `#676780` | `#8a8a9e` |
| `TextFaint` | `#55556e` | `#9a9aae` |
| `TextGhost` | `#44445c` / `#33334a` | `#b4b4c6` / `#c6c6d4` |
| `Accent` purple | `#a78bfa` | `#6b46d9` |
| `AccentHi` | `#c4b5fd` | `#5936b8` |
| `SelBg` selection row | `#1d1633` | `#ece5fb` |
| `Good` green | `#34d17b` | `#148a4e` |
| `Warn` yellow | `#e8c74a` | `#a87b0a` |
| `Bad` red | `#ef6a6a` | `#cc3a3a` |
| `BadSoft` / `BadText` / `BadMuted` | `#ef8a8a` / `#f0b7b7` / `#c98a8a` | `#d95c5c` / `#a02c2c` / `#b06868` |
| `Info` blue | `#6aa8ef` | `#2a6fce` |
| `ErrBannerBg` / `ErrBannerBorder` | `#2a1518` / `#4a2228` | `#fbe9ea` / `#e5b7bb` |
| `ErrCardBg` / `ErrCardBorder` | `#16121a` / `#3a2a30` | `#f6f1f4` / `#dcc9cd` |
| `BarTrack` | `#1c1c2c` | `#e2e4ec` |
| `YamlKey` | `#c98fde` | `#8a3fb8` |
| `YamlStr` | `#b8d78f` | `#4c7a1e` |
| `YamlPunct` | `#55556e` | `#9a9aae` |
| `YamlFold` | `#44445c` | `#b4b4c6` |
| `ProdBorder` / `ProdText` | `#4a2a2a` / `#ef9a9a` | `#e3b9b9` / `#b03030` |
| `ConfirmBorder` / `ConfirmHeaderBg` / `ConfirmPillBg` | `#5c2a2a` / `#1a1014` / `#2a1418` | `#d98a8a` / `#f9ecec` / `#f5dede` |
| `AllNsPillBg` / `AllNsPillText` | `#12203a` / `#8ab8ef` | `#e0ebfa` / `#1f5cad` |

Rules that hold in both themes:
- Text hierarchy is a 6-step ramp `Text → TextGhost` (contrast descends; on light the ramp runs dark→pale, never re-order roles).
- Bar track `BarTrack`; bar fill `Accent`, `Warn` ≥70%, `Bad` at limit. Warn numbers in YAML use `Warn`.
- Red borders (`ConfirmBorder`) remain reserved exclusively for destructive confirms.
- Mode pills by hue: purple = normal modes · blue = ALL NS · red = OFFLINE/CONFIRM · gray = SETUP. Pill bg = the theme's tinted surface, pill text = the hue's text token.
- Selection cue = 1-cell accent bar + `SelBg` row background — verify it in both themes; the bar is the primary cue.
- 4a desaturation: each theme defines its own muted ramp (dark: dim the colors; light: wash toward gray) — don't compute it, declare it in the struct.
- Test on a real light terminal and on 256-color fallback; hex values degrade via termenv.

Status glyphs: `●` running · `◐` pending · `✕` failed/crashloop · `○` completed · `◌` disconnected/probing · `↺` restarts · `⧗` stale · `▶` following · `▲` warning/version-skew · `◈` cordoned · `∗` all namespaces · `⇄` port-forward.

Typography: single monospace face (mock uses JetBrains Mono; the terminal's font applies). Weight via bold only. Uppercase + letterspacing for section labels/column headers.

## Architecture notes
The enforceable rules distilled from this design (kind registry not hardcoded views, actions as a command table, cluster access behind an interface, versioned persisted state, pure rendering, terminal capability degradation) live in `CLAUDE.md`'s Invariants and Conventions sections — that file is the one to keep in sync, not this list.

## Mapping HTML → terminal
- 1 row of table/list = 1 terminal line. Strips, keybar, header = 1 line each.
- 2px left selection border = a 1-cell colored bar (`▎` or bg on first cell) + row bg `#1d1633` via Lip Gloss background.
- Progress bars = block glyphs (`█▓░` or `▰▱`) ~6–8 cells wide, colored per token rules.
- Rounded corners/shadows on the palette = Lip Gloss rounded border; "dim the backdrop" = re-render the table through a faint style.
- Desaturation in 4a = swap the color ramp for muted variants (no filter support in terminals).

## Assets
None — all glyphs are Unicode text; no images or icons.

## Files
- `Kute Spec.dc.html` — the 0.1.0 curated spec: 12 sections, one canonical mock per screen (2a–23b), with design-rationale notes under each. Toggle the notes via the showNotes tweak when viewing.
- `v.0.2.0.dc.html` — the 0.2.0 addendum: 2 sections (update notifications; inline mutation editors), covering 24a–28b. Same showNotes toggle; no separate README — folded into this one.
- `support.js` — runtime for opening either HTML mockup in a browser (keep next to the HTML files).
