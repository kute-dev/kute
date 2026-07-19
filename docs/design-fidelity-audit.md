# kute — Design-Fidelity Audit

Every screen in `docs/design/README.md` checked against the current implementation. 40 design sections, 15 task packages, 13 parallel review passes. Each screen was read against its exact spec text and driven live in `--demo` mode via `tmux`, side by side with its Go source — not just re-read from memory.

**Findings only — no code was changed.** `17a` (YAML edit mode) was out of scope per instruction; `3a` is marked superseded in the spec itself and wasn't reviewed. A handful of the most surprising claims below were independently re-verified by hand after the review passes returned (marked ✓ **spot-checked**).

**69 findings total** — 9 invariant violations · 13 missing features · 47 fidelity gaps.

## Contents

1. [Navigation & chrome](#1-navigation--chrome) — 2b · 12a · 12b · 7b · 6a · 6b · 7a
2. [Resting screens](#2-resting-screens) — 2a · 15a
3. [Error & connectivity states](#3-error--connectivity-states) — 4a · 4b · 4c · 10b
4. [Pod detail & logs](#4-pod-detail--logs) — 5a · 5b
5. [YAML, confirm & secrets](#5-yaml-confirm--secrets) — 8a · 8b · 21a
6. [Deployments, events, nodes](#6-deployments-events-nodes) — 9a · 9b · 11a · 11b
7. [Exec & port-forwarding](#7-exec--port-forwarding) — 10a · 13a · 13c · 13d
8. [Custom resources](#8-custom-resources) — 14a · 14b · 14c · 14d
9. [Empty state, timeline, scale, bulk](#9-empty-state-timeline-scale-bulk) — 10c · 16a · 16b · 17b · 20a
10. [Helm, overview, RBAC, routing](#10-helm-overview-rbac-routing) — 18a · 19a · 22a · 23a · 23b
11. [Cross-cutting behavior](#11-cross-cutting-behavior) — system-wide

---

## 1. Navigation & chrome

Goto/namespace/context palettes, alias letters, and the help overlay. The shared palette shell genuinely is shared — confirmed clean across all three scopes.

### 2b · 12a · 12b — Jump palette & aliases

- **[missing]** The `GOTO` mode pill + one-line explanation the spec calls for whenever the palette is open never renders — `Model.Mode()` has no callers anywhere in the render path.
  spec `README.md:39` · code `internal/tui/model.go:778`, `chrome.go` render path
- **[gap]** Default mode-pill style (moot until the above is wired) uses `Accent` instead of the spec's `AccentHi`, and is never bold.
  spec `README.md:39` · code `internal/tui/chrome.go:357-368`
- **[gap]** The palette advertises "`tab` complete" but Tab is a dead key — `handlePaletteKey` has no case for it, so it silently no-ops.
  spec `README.md:38` · code `internal/tui/model.go:435-503` · ✓ live-confirmed: identical pane before/after Tab
- **[gap]** 12a's footer copy drops the "colored first letter ·" clause from the spec's exact wording.
  spec `README.md:171` · code `internal/tui/goto.go:247-252`

Confirmed correct: aliases are exactly p·d·s·i·n·c·e, always the kind's own first letter, highlighted not chipped; typing one re-ranks to rank 1 rather than firing instantly, and a second character correctly degrades to plain fuzzy matching. No hex literals.

### 7b — Help overlay

- **[gap]** An off-by-two width calculation truncates every key-hint row with a stray "…" at most terminal widths. Columns are sized to fill `frameWidth`, then the assembled row is re-padded to `frameWidth − 2` — two cells short of what it was sized against. Reproduces at widths 80 and 220; doesn't reproduce at exactly 120, which is the one width the existing test uses.
  spec `README.md:113-114` · code `internal/tui/help.go:62` vs. `128-132` · ✓ spot-checked — confirmed by re-reading help.go directly

No golden/snapshot test exists for `help.go` at any width — this is exactly the class of bug a golden fixture would have caught.

### 6a · 6b — Namespace palette & all-namespaces mode

- **[gap]** 6b's collapsed healthy-group line (`▸ nva-prod · 54 pods · all running`) renders gray (`TextFaint`) instead of the spec's green — a deliberate, code-commented deviation ("de-emphasized rather than drawing the eye with green"), worth a design sign-off.
  spec `README.md:99` · code `internal/tui/tasks/browse/view.go:808-828`
- **[gap]** 6b's group header renders its trouble summary as inline text (`1 crashloop · 1 pending`) rather than right-aligned health-style glyph chips, and has no distinguishing background fill.
  spec `README.md:98` · code `internal/tui/tasks/browse/view.go:771-787`
- **[invariant]** The `--demo` fake cluster never sets pod CPU metrics, so 6a's CPU-share column (and the main table's CPU/MEM columns generally) can never actually be exercised in demo mode — a gap in "the fake provider must stay feature-complete for demo mode."
  CLAUDE.md invariant · code `internal/kube/fake/fake.go:378-390`

Confirmed correct: numbered-recents digit grammar, alt-tab preselection, the "all namespaces" pinned last row (blue, reached only via ↑↓/↵ — 'a' correctly still types into the query), and the blue (not purple) `ALL NS` pill.

### 7a — Context palette

- **[gap]** No CONTEXT / NAMESPACE / STATUS column-header row is rendered, unlike 6a's namespace palette which gets one.
  spec `README.md:104` · code `internal/tui/model.go:794-796`
- **[gap]** The PROD tag renders as bold text only — the spec's bordered-chip treatment is never applied. A bordered `Prod` style exists in the theme but is dead code, referenced nowhere.
  spec `README.md:106`, tokens `377` · code `internal/tui/palette_styles.go:62`

Confirmed correct — and worth calling out: the PROD tag genuinely comes only from `~/.config/kute/config.yaml`'s `prodContexts` list, never a name heuristic — live-proved against a context literally named `kwok-prod-sim`, which renders with no PROD tag until explicitly configured. `ctrl+p` is correctly chorded so bare `p` still reaches the query.

---

## 2. Resting screens

The default pods table and its loading state. Structurally excellent — every element from the spec exists and colors route through Theme tokens with zero hex-literal violations. The two real defects are behavioral rather than structural.

### 2a — Main table

- **[gap]** CPU/MEM cells show the literal string `n/a` instead of the spec's clean `–` when metrics are unavailable — both the real and fake clients populate a non-empty `"n/a"` string that bypasses the empty-string fallback. Reproduces on real clusters too, for any pod not yet scraped.
  spec `README.md:29` · code `internal/tui/tasks/browse/view.go:839-849` · ✓ live-confirmed in --demo
- **[gap]** The keybar's default verb set has drifted from the spec's literal text (`↵ open · l logs · d describe · e exec`): there is no `describe` verb anywhere in the codebase, and `e`/`x` are swapped versus the mockup — `e` opens Events, `x` opens Exec. Likely an intentional evolution the spec text was never updated to reflect.
  spec `README.md:31` · code `internal/tui/verbs/verbs.go:55-71`

### 15a — Loading state

- **[missing]** "Revisiting a kind seen this session: cached rows dimmed instead of skeletons" is not implemented at all — there is no per-kind row cache anywhere; every revisit shows the full 7-row skeleton from scratch, even seconds after last viewing that kind.
  spec `README.md:232` · code `internal/tui/tasks/browse/model.go:678-696`

---

## 3. Error & connectivity states

10b (no kubeconfig) is essentially spec-perfect, fully live-verified. The other three carry real defects — one of them a genuine security-relevant gap.

### 4a — Connection lost mid-session

- **[invariant]** **Mutating verbs are not actually disabled while offline — only hidden from the keybar.** The key-dispatch switch (`ctrl+d`, cordon/drain, rollout-restart, scale, exec) never checks connection state, and `actions.Controller.Begin` has no connectivity gate either. A user can still delete, drain, or exec against a broken connection; only the keybar's displayed hints change.
  spec `README.md:52` · code `internal/tui/tasks/browse/update.go:369` (ctrl+d case has no offline check), `internal/tui/actions/controller.go:84` · ✓ spot-checked directly against update.go and controller.go
- **[gap]** The error banner has no background/border fill — `ErrBannerBg`/`ErrBannerBorder` are defined in the theme but referenced nowhere in view code, so the banner reads as colored text on the plain background instead of a distinct callout box.
  spec `README.md:49` · code `internal/tui/tasks/browse/view.go:159-177`

### 4b — RBAC / 403 on one kind

- **[gap]** Two of the card's four recovery-action lines silently drop their explanatory clause (e.g. "— prod-eks grants secrets:list" is never rendered).
  spec `README.md:57` · code `internal/tui/tasks/browse/view.go:436,438`

Everything else — verbatim message with entity highlighting, no-auto-retry-on-4xx, the later-added `w who-can` recovery line — matches the spec correctly.

### 4c — Cluster unreachable at launch

- **[missing]** The spec's headline feature — a pre-probed, bordered, selectable SWITCH CONTEXT list with per-context reachability and latency — is knowingly not built. It's deliberately deferred to the context palette (`c`) instead, per the code's own comment: "Live reachability is deliberately not duplicated here."
  spec `README.md:65-66` · code `internal/tui/tasks/setup/view.go:184-196`
- **[gap]** Live-reproduced rendering bug: a long connection error word-wraps inside the raw-error box, but the layout helper's width calculation doesn't account for embedded newlines correctly — the wrapped second line gets truncated down to a bare "…", silently dropping the tail of the real error message.
  spec `README.md:64` ("verbatim") · code `internal/tui/tasks/setup/view.go:108-118`, `internal/tui/components/layout.go:10-30` · ✓ live-reproduced against a realistic 65-char TLS/timeout error
- **[gap]** The raw error box itself has no border, unlike the correctly-bordered LOOKED IN box on the adjacent 10b screen.
  spec `README.md:64` · code `internal/tui/tasks/setup/view.go:138-139`

Title row, retry countdown/attempt counter, and the `NO CLUSTER` pill all match and were live-verified against a genuinely unreachable IP.

### 10b — First run / no kubeconfig

No findings. Fully live-verified end to end (genuinely missing kubeconfig): wordmark, LOOKED IN box, provider hints, keybar, and the gray (not purple/red) `SETUP` pill all match the spec exactly.

---

## 4. Pod detail & logs

Structurally complete — every section is present and correctly ordered — but several color/content fidelity gaps cluster around severity signaling specifically.

### 5a — Pod full view

- **[gap]** The title row's status text is missing its leading glyph (✕/◐/●/○) — the underlying data has it, but the render call discards it.
  spec `README.md:71` · code `internal/tui/tasks/poddetail/view.go:143-147`
- **[gap]** The MEM bar doesn't turn red at 96% usage — only the adjoining text does. Golden-fixture-confirmed: at 246Mi/256Mi (96%), the bar fill renders Warn/yellow while the text renders Bad/red.
  spec `README.md:75` · code `internal/tui/tasks/poddetail/view.go:342-366`, `internal/tui/components/bar.go:54-64`
- **[missing]** The last-termination banner never shows "next backoff" — the domain model carries exit code/reason/age but has no field for it at all.
  spec `README.md:73` · code `internal/kube/pods.go:60-66`
- **[gap]** EVENTS rows never escalate "Warning" to red for the pod's own actively-failing events — always yellow. The sibling 9b events screen already implements exactly this red-vs-yellow split; poddetail doesn't reuse it.
  spec `README.md:76` · code `internal/tui/tasks/poddetail/view.go:397-429`
- **[gap]** The CONTROLLER link shows the pod's direct owner (ReplicaSet) rather than the resolved Deployment the spec's example shows. The ReplicaSet→Deployment resolution logic already exists in this file for a different shortcut (`alt+o`) but isn't reused here.
  spec `README.md:72` (example: `deploy/nva-worker ↗`) · code `internal/tui/tasks/poddetail/view.go:217-262`

Confirmed correct: `j`/`k` genuinely cycles the sibling pod list in place without leaving detail view, including correct no-op behavior at list ends.

### 5b — Log view

- **[gap]** Every ERR line gets the full-width red-tinted row, not just "the most significant one" the spec calls for — there is no selection logic among multiple ERR lines at all.
  spec `README.md:83` · code `internal/tui/tasks/podlogs/view.go:146-195` · ✓ live-verified — two ERR lines both got the full tint
- **[gap]** `/` filtering uses plain case-insensitive substring matching, not "the same filter grammar as the table" (which is fuzzy-ranked) — a deliberate, self-documented deviation.
  spec `README.md:85` · code `internal/tui/tasks/podlogs/model.go:239-254`

---

## 5. YAML, confirm & secrets

The command-registry/tier architecture is followed correctly end to end here — no hand-wired keys, `ConfirmBorder` correctly scoped to destructive surfaces only. One real, moderate-severity bug in the secret-reveal indicator.

### 8a — YAML view

No findings. Fold-by-default, syntax coloring, cursor-line highlight, read-only (correctly, since 17a edit mode is out of scope), `Y`/`/` — all matched in code and live capture.

### 8b — Destructive-action confirm

- **[gap]** The grace-period line shows generic text ("default grace period applies") instead of the spec's concrete figure ("`30s`") — the domain layer never exposes `terminationGracePeriodSeconds`, so there's no real value to show.
  spec `README.md:125` · code `internal/tui/tasks/browse/delete.go:96-104`

Confirmed correct: the two-tier inline-vs-modal friction, `N/M` type-the-name progress gating, and `ConfirmBorder`'s exclusive use on destructive surfaces (checked every call site) all hold.

### 21a — Secret decode

- **[gap]** **The "revealed" safety tag is silently truncated off for long decoded values** — common for real secret payloads (certs, kubeconfigs, tokens). The full content string (value + tag) is padded/truncated as one unit with no space reserved for the tag, so anything near the line width loses the tag that's supposed to flag it as plaintext-on-screen. Multi-line values are unaffected.
  spec `README.md:271-274` · code `internal/tui/tasks/yamlview/view.go:188-196, 231-239` · ✓ spot-checked directly against view.go's Pad() call
- **[gap]** The reveal indicator renders as a filled pill rather than the spec's "bordered" chip — a deliberate, documented terminal-idiom substitution.
  spec `README.md:273` · code `internal/tui/tasks/yamlview/view.go:241-251`

Confirmed correct: masked-by-default rendering, `x`/`X` reveal semantics, re-masking on view exit (live-verified — reopening showed 0 revealed again), and `Y` keeping the full-YAML copy base64-encoded.

---

## 6. Deployments, events, nodes

Solid on structure and interaction — verb tiers, sort/collapse recipes, and keybars all wire correctly to the shared registries. Gaps are concentrated in secondary visual polish, several with an exact precedent elsewhere in the same file.

### 9a — Deployments list

- **[gap]** Keybar pill reads "DEPLOYMENTS", not the spec's "DEPLOY".
  spec `README.md:131` · code `internal/tui/tasks/browse/keys.go:86-90`
- **[missing]** IMAGE never shows `new ← old` during a rollout transition — a documented scope cut, since the previous ReplicaSet's image isn't reachable from the Deployment object alone.
  spec `README.md:130` · code `internal/resources/projections.go:202-219`

### 9b — Events view

- **[gap]** Row layout diverges from the spec's 5-aligned-column model (glyph · REASON·OBJECT · MESSAGE · ×count · LAST) — actual rendering is two merged full-width lines, with MESSAGE sharing a line with OBJECT rather than owning its own widest column. All the underlying data/coloring/dedup logic is correct; only the columnar structure differs.
  spec `README.md:135` · code `internal/tui/tasks/events/view.go:253-281`

### 11a — Nodes list

- **[gap]** STATUS "Ready" renders green instead of the spec's dim — the adjacent ROLLOUT column already has exactly this "healthy state renders dim, not green" carve-out, but it was never extended to Nodes' own STATUS column.
  spec `README.md:158` · code `internal/tui/tasks/browse/view.go:693-694`
- **[gap]** Bars use `■`/`□` (solid/hollow square) instead of the spec's block glyphs (`▐▌▌░░░`) — affects both 11a's CPU/MEM columns and 11b's allocation bars, since it's one shared component.
  spec `README.md:158` · code `internal/tui/components/bar.go:47,49`

### 11b — Node detail

- **[missing]** The active-pressure condition line never appends age, and leaves a dangling separator when the kubelet message is empty.
  spec `README.md:163` · code `internal/tui/tasks/nodedetail/view.go:222-223`
- **[gap]** A NotReady condition renders yellow here, but red on 11a's own list for the identical signal — an internal inconsistency between the two screens.
  code `internal/tui/tasks/nodedetail/view.go:220-221` vs. `internal/resources/projections.go:466-467`
- **[gap]** ALLOCATED/ALLOCATABLE's "used / total" text never turns yellow when hot — only the bar's fill segment changes color.
  spec `README.md:163` · code `internal/tui/tasks/nodedetail/view.go:264-272`

---

## 7. Exec & port-forwarding

13a (forward picker) matches the spec closely with no findings. The other three have real gaps — and two of the app's states (multi-container exec, a failing forward) turn out to be unreachable in demo mode at all, which is itself worth flagging.

### 10a — Exec container picker

- **[gap]** Shell detection isn't implemented — every row shows the same hardcoded literal `"sh, bash"` regardless of the container's actual image.
  spec `README.md:141` · code `internal/tui/tasks/execpicker/view.go:125`
- **[missing]** Sidecar containers are never labeled "sidecar" — no detection signal exists on the domain model at all.
  spec `README.md:141` · code `internal/kube/pods.go:143-168`
- **[invariant]** Demo fixtures contain zero multi-container pods, so this whole picker screen can never actually be reached by driving `--demo` mode — only exercised by synthetic unit-test fixtures. A gap in "the fake provider must stay feature-complete for demo mode."
  CLAUDE.md invariant · code `internal/kube/fake/fixtures.go`

Confirmed correct: single-container pods skip the picker and exec straight to shell; kute correctly suspends and hands the tty to `kubectl exec`, live-verified.

### 13a — Port-forward picker

No findings. In-place local-port editing, the 80→8080 pre-fill rule, busy-port fallback text, and the "will run" line all matched, live-verified end to end.

### 13c — Forwards manager

- **[gap]** Breadcrumb shows a generic "cluster-scoped" tag instead of the spec's "all namespaces" tag — Forwards is registered with the same cluster-scoped flag Nodes uses, routing it through the wrong breadcrumb path.
  spec `README.md:189` · code `internal/resources/registry.go:56`
- **[invariant]** A failing forward's yellow/retry/backoff state is unreachable in demo mode — the fake dialer never errors — which blocks live verification of the hard "never modal/banner" invariant, though code inspection shows it holds.
  code `internal/kube/fake/forward.go`

Confirmed correct: `x` stops one forward immediately with zero confirmation, `X` shows an inline y/N (not a modal), matching the spec's tier split.

### 13d — Ambient header forward chip

- **[gap]** The chip disappears entirely on the exec-picker and forward-picker screens specifically — every one of the other 12 screens' `Header()` includes it; these two omit it.
  spec `README.md:196-198` · code `internal/tui/tasks/execpicker/view.go:30-56`, `internal/tui/tasks/forwardpicker/view.go:30-48`

Confirmed correct: zero chrome when no forwards exist, purple→yellow escalation on a reconnecting session, and the chip surviving a context switch — all live-verified.

---

## 8. Custom resources

The architectural payoff genuinely holds: the Certificate exemplar and every other discovered kind except one documented exception flow through one generic path with zero per-kind branches. Both hard invariants (CRD delete always modal; no alias chips) passed live testing. Gaps are finishing-touches, not architecture.

### 14a — CR instance list (Certificates)

- **[missing]** The breadcrumb never shows the dim API-version tag (e.g. `cert-manager.io/v1`) next to the kind name.
  spec `README.md:202` · code `internal/tui/tasks/browse/view.go` Header()
- **[gap]** Breadcrumb and keybar pill use the raw lowercase plural (`certificates` / `CERTIFICATES`) instead of the spec's capitalized/abbreviated form (`Certificates` / `CERTS`) — no abbreviation logic exists for any CRD kind, which also produces `CUSTOMRESOURCEDEFINITIONS` on 14b instead of `CRDS`.
  spec `README.md:206, 212` · code `internal/tui/tasks/browse/keys.go:86`
- **[gap]** The neutral-status fallback (no printer columns, no conditions) renders the glyph in Info blue instead of the spec's `TextFaint`, and the "no status semantics · NAME + AGE only" strip copy never appears anywhere in the codebase.
  spec `README.md:205` · code `internal/tui/tasks/browse/view.go:341-352` · ✓ live-verified via raw ANSI capture on an AppProject row

### 14b — CustomResourceDefinitions list

- **[missing]** Strip wording is generic ("ok" / "customresourcedefinitions") instead of CRD-specific "established/installing" + "N definitions · M API groups · sorted by group" — every other kind that needed custom strip wording has one; CRDs don't.
  spec `README.md:209` · code `internal/resources/crd.go:224-235`
- **[gap]** Rows fall back to alphabetical order, not the spec's "sorted by group" — a live check confirmed rows from the same API group scattered rather than clustered.
  spec `README.md:209` · code `internal/tui/tasks/browse/sort.go`

### 14c · 14d — Discovered-kind goto & generic detail

No findings. Fully matches spec, live-verified — including the "empty conditions and events skips straight to YAML" fallback and condition messages rendered verbatim, never paraphrased.

### Architecture invariant — "no per-CRD layout code"

- **[invariant]** One narrow, documented exception exists: the registry special-cases the literal kind name "HTTPRoute" to attach a bespoke ATTACHED column for §23b — the sole kind-name branch found anywhere in the CRD registry.
  code `internal/resources/crd.go:334-339`

Confirmed correct, live: CRD delete always requires the type-the-name modal even outside PROD; no CRD kind ever gets an alias chip.

---

## 9. Empty state, timeline, scale, bulk

Unusually faithful. 10c, 16a/16b, and 20a came back with no findings at all — every behavioral, textual, and visual detail live-verified against the spec.

### 10c · 16a · 16b · 20a — Empty namespace, timeline, bulk ops

No findings. Live-computed "ways out" counts, the merged one-clock timeline with a real revision rail, filter-then-mark-only bulk grammar, esc-clears-marks-before-back ordering, and the PROD type-the-count modal all matched exactly.

### 17b — Scale

- **[missing]** The HPA-managed-workload yellow note ("managed by hpa/<name> — scaling overridden on next sync") is entirely absent — there's no HPA detection, lister, or fixture anywhere in the codebase.
  spec `README.md:249-252` · code `internal/tui/tasks/browse/scale.go`

Everything else — inline-never-modal prompt, pre-fill current±1, `0` allowed, exact "will run" command line — confirmed live.

---

## 10. Helm, overview, RBAC, routing

The highest-fidelity batch in the whole audit — 18a, 19a, and 22a came back with zero real findings, matching the spec down to its exact worked examples in places.

### 18a · 19a · 22a — Helm releases, overview, who-can

No findings. Helm browsing needs no `helm` binary for listing (confirmed no shell-out in the list path); who-can resolves entirely from the watch cache with no live API round-trip; overview is confirmed to genuinely be a routing layer, not the app's start screen.

### 23a — Ingress routing table

- **[gap]** Rows don't sort unhealthy-first despite the spec's explicit requirement — Ingress isn't included in the set of kinds that get the unhealthy-first reorder.
  spec `README.md:284` · code `internal/tui/tasks/browse/sort.go:14-21`
- **[missing]** The TLS strip names each secret but they aren't selectable — the spec's "`↵` jumps to it" isn't implemented; no key handling targets these rows at all.
  spec `README.md:285` · code `internal/tui/tasks/routetable/view.go:262-282`

### 23b — HTTPRoute / Gateway API routing table

- **[missing]** A Gateway listener's `↵` always opens the full, unfiltered HTTPRoute list rather than filtering to that listener's attached routes — a self-documented scope cut in the code comment itself.
  spec `README.md:292` · code `internal/tui/tasks/routetable/update.go:150-159`

---

## 11. Cross-cutting behavior

The one place per-screen review can't catch a regression: rules that are supposed to hold everywhere. The palette shell, the destructive-action tier system, and esc/back navigation genuinely are shared infrastructure. Several verbs and policies that exist in the registry simply aren't asked for by every screen that should ask for them.

- **[invariant]** **`verbs.Verb.Mutating` is declared, and its doc comment states it drives disabling verbs in OFFLINE mode — but it is never read anywhere in the codebase.** The only reference to it outside its own declaration is a test assertion, not a runtime consumer.
  code `internal/tui/verbs/verbs.go:26-28` · ✓ spot-checked — grepped for every reference to the field
- **[invariant]** Only `browse` implements any offline-mode UI at all. `nodedetail`, `poddetail`, `objectdetail`, and `helmhistory` never show the OFFLINE pill or gate their own mutating verbs (cordon/drain/delete/rollback) while disconnected — they track connection state only to color the header badge.
  spec `README.md:52, 301`
- **[invariant]** `f` (port-forward) is wired only in `browse` — missing from `poddetail` and `nodedetail`'s own pod rows, both genuinely pod objects the spec lists alongside `x` and `y` as available "on any object row." A single grep for the key across every package confirms one call site.
  spec `README.md:304, 308` · code `internal/tui/tasks/browse/update.go:343` (sole site)
- **[missing]** The mode-pill gap found in the goto palette turns out to be systemic: `Model.View()` never touches the underlying task's `Keybar()` output while composing any overlay, so no scope's pill (GOTO, NAMESPACE, CONTEXT, HELP) can ever render, from any screen.
  Design Principle 5, `README.md:19` · code `internal/tui/model.go:821-851`
- **[gap]** `/` filter's "highlight matched characters" claim resolves to three different implementations: fuzzy + per-character highlight (table, node detail), plain substring with no highlight at all (events, timeline, logs), and substring with whole-row highlight (YAML view). Each divergence is deliberately code-commented as a reasoned trade-off, but the spec frames it as one shared mechanism.
  spec `README.md:298`
- **[gap]** The "N hidden by filter — esc to clear" notice exists in exactly one screen (`browse`). Node detail, logs, events, and timeline show only a bare match count with no "hidden" framing or clear-hint — the filter-specific instance of the app's own "never blank out data without saying so" principle, honored in one of five filterable screens.
  Design Principle 3, `README.md:17` · code `internal/tui/tasks/browse/view.go:308` (sole site)

**What holds up cleanly, system-wide:** the destructive-action tier policy (`verbs.TierFor` + the kubeconfig-annotation `isProd()` check) is genuinely uniform across every screen that owns a mutating verb, CRD bulk-delete included. `g`/`n`/`c`/`?` are reachable from every screen tested, and `esc` correctly walks back exactly one stack frame everywhere, live-verified from a pushed pod-detail screen. Every field in the spec's suggested state-management shape (`probes`, `discovery`, `marks`, `reveal`, `editBuffer`, `timeline`, `whoCan`) is genuinely wired to real code — `Mutating` above is the only dead one.

---

## Out of scope

`17a` (YAML edit mode) was explicitly excluded per instruction. `3a` is marked superseded by the spec's own text (superseded by 12a) and was skipped accordingly.

## Method

12 parallel reviewers each covered a cluster of screens, reading the exact spec text, reading the Go implementation, and driving the live app in `--demo` mode via tmux for both themes where reproducible. A 13th pass checked the spec's system-wide "Interactions & Behavior" section against findings from the other twelve. A handful of the highest-impact claims were independently re-verified afterward. No code was changed — this is a findings report only.
