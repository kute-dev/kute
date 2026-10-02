# 13 — Shell, navigation, palette & theming

Scope:

- The root shell: `internal/tui/{model,update,chrome,goto,namespace,context (palette parts),help,paste,keycast,layout,theme,styles,glyphs,inputstyles,palette_styles,counts,messages,session,version}.go`.
- Shared components: `internal/tui/components/*` and `components/palette`.
- Cross-cutting sweeps over **every** task package:
  - hex literals;
  - clock and I/O reads in render;
  - `RoutePaste` coverage;
  - `j/k` ≡ `↑↓`;
  - `esc`;
  - `CapturingInput`;
  - `ConnStateMsg` handling.
- Design sections: §2a/2b, §6a, §7a/7b, §12a/12b, §15a, "Interactions & Behavior", and "Design Tokens".

Verification:

- `go vet` and `go test` pass for `internal/tui` and `internal/tui/components/...`.
- Findings marked **(proved)** were reproduced with throwaway `go test -overlay` tests, with `GOCACHE`/`GOTMPDIR` under `$TMPDIR`. No repo file was changed.
- Everything else is "traced by code". Anything that depends on runtime behaviour outside this repo is marked **unconfirmed**.

Already covered elsewhere and not repeated here:

- 05 H1: the conn-replay symptom on nodedetail and overview. **H1 below is the root-level design that fixes it.**
- 07 M2: fluxdetail matches `kube.ConnState`. 11 notes the same bug in certchain.
- 06 M3, 07 L5, 08 L7: per-screen tick chains dying on push/pop. H2 below is the root cause, plus the browse instance nobody filed.
- 03 L2, 04 L4, 08 L1: `time.Since` in the objectdetail, poddetail and helmhistory renders.
- 02 H1: the context-palette probe freeze.
- 10 M7/L5: hand-wired confirm hints.

## Summary

These parts are sound:

- **Theme discipline.**
  - There are no hex or raw colour literals outside `theme.go`. The one hit is `cmd/site`, which is not view code.
  - Both themes populate every token, and `TestThemeCompleteness` enforces it.
  - The light values match the design table.
  - The mode-pill hues match the spec.
- **One palette shell.**
  - `components/palette` is the only palette implementation. goto, namespace, context, and who-can's verb and resource palettes are all scopes of it.
  - No task forks it.
- **Aliases.**
  - `p d s i n c e` are the kinds' first letters, highlighted via `Matches=[0]`, never shown as chips.
  - An alias pins its kind to rank 1 and never fires on its own (`goto.go:107-150`, `:289-316`).
- **Paste.** Every screen that owns a text buffer calls `RoutePaste`. The four screens that embed an `actions.Controller` without it (certchain, fluxdetail, fluxtree, helmhistory) never open a type-the-name buffer.
- **Purity sweep.** View code does no I/O.

The defects sit in the root's **activation contract**: what a task is told when it becomes the active task.

1. **Activation drops state.** Every swap site sets `m.task`, but none of them replays `m.conn`. A pushed, popped, rebuilt or routed screen therefore starts out believing it is online (H1, **proved** for both push and pop).
   - The root delivers every message only to the active task. A *parked* task's self-rescheduling tick therefore lands on whichever screen is on top, and the chain dies there.
   - `Reload()` re-arms none of them. Browse's 2s metrics poll, the clock on the resting screen, freezes after any ↵ + esc round trip (H2).
2. **`routeGoto` never calls `Init()` on the browse it builds.** If the jump's kind or namespace already equals `Session.Location`, the fresh browse returns a nil cmd and stays on "Loading Pods…" forever. Pods → ↵ → `g p ↵` reproduces it (H3, **proved**). `goToResource` was patched for this; `GotoKindMsg` and `SwitchNamespaceMsg` were not.
3. **The goto palette caps its resource corpus at 50 *before* filtering.** A pod beyond the first 50 cached rows is "no matches" (H4, **proved**). The cap also means each keystroke projects the current kind's whole list.
4. **The namespace palette reads breadth-first.** On the Helm releases list, `n` calls `ListRaw(HelmRelease, ns)` once per namespace. That starts one release-Secret informer per namespace, synchronously, on the Update loop (H5, **proved**). This is exactly the fan-out that `docs/lazy-informers.md` §5.5 was written to avoid.
5. **The shell steals keys from inline confirms.** certchain, helmhistory, secretdata and configmapdata don't report `CapturingInput` while a y/N is open. Their advertised `n cancel` opens the namespace palette instead, and `c`, `g` and `q` are taken too (M1, **proved**).
6. **tasks/update (28b) has a value-receiver `Update`.** Its returned `Model` doesn't implement `Task`, so the root silently discards every state change (M2, **proved**).
7. **Layout at realistic widths drops information.**
   - The header drops its whole right side, connection badge included, as soon as the crumbs are long. An EKS ARN context at 120 columns is enough (M3, **proved**).
   - At 80×24 the help overlay squeezes its LIST column to bare `…` (M4, visible in the checked-in golden).

Counts: **5 high, 6 medium, 15 low.**

## Findings

| # | Sev | Location | Issue | Suggestion |
|---|---|---|---|---|
| H1 | high | `tui/model.go:871-877` (push), `:888-898` (`popTask`), `:560-574` (`routeGoto`), `:1255-1268` (`openUpdatePanel`), `:593-605` (`ReplaceRootMsg`), `:641-661` (4c swaps), `:739-744` (context rebuild); `:616-619` records `m.conn` but forwards only to `m.task` | No activation site replays the root's `m.conn` to the newly active task. 27 task packages keep their own `conn`. The zero value reads as online, and a parked task never sees transitions. **(proved)** In a root-level overlay, after `ConnUnauthenticated` + push, the child has `Offline()==false`. If a parent is parked as Connected, an outage happens while the child is on top, and esc pops back, the parent still says `connected` while the root says `unauthenticated`. In the unauthenticated phase the health loop never re-emits (02/05), so this lasts indefinitely. The resumed browse polls metrics, re-runs the credential plugin and enables mutating verbs. | Funnel every swap through one `activate(task, fresh)` helper: resize, `Init()` when the root built the task, then a **synchronous** `m.task.Update(kube.ConnStateMsg(m.conn))` when `m.conn.Phase != ""`. Reset `m.conn` on `ReplaceRootMsg`. Add an AST test against `case kube.ConnState:`. Full design below. |
| H2 | high | `tui/model.go:871` (messages only to `m.task`), `:239-241` (`Reloader` contract); `browse/update.go:76-79` (`Reload`), `:249-252`; `browse/metrics.go:28-32`; arming sites `browse/model.go:969`, `:1372`, `helm.go:150`, `update.go:145` | A parked task's tick message is delivered to the active task, which ignores it, so the chain ends. Browse's `Reload()` only schedules a list reload. `metricsTickMsg` is re-armed only by `Init`, `resetAndLoad`, the helm path and an offline→online transition. After any push that outlasts one 2s tick (pod detail, logs, YAML), the resting screen's CPU/MEM columns and bars freeze while the header still says `sync 2s`. 06 M3, 07 L5 and 08 L7 are the same root cause on other screens. Traced by code. | Make it part of the activation contract. Either widen `Reloader` to "Reload re-arms every self-rescheduling chain", with each chain epoch-guarded so a still-live chain isn't doubled (browse: `m.metricsEpoch++` then `loadMetricsCmd` + `scheduleMetricsTick` when `pollsMetrics()`, plus the cron clock per 06 H1), or add `Resumer.Resume() tea.Cmd`, which `activate` calls on pop. Add a root test: push, deliver the parent's tick to the root, pop, then assert the parent re-arms. |
| H3 | high | `tui/model.go:560-574` (`routeGoto` never calls `fresh.Init()`); `browse/model.go:1102-1105` (`switchKind` no-op when `kind == m.kind`), `:1136-1139` (`switchNamespace` no-op); compare `:1199-1215` (`goToResource`'s `m.rows != nil` fix) | A fresh browse seeds its kind and namespace from `Session.Location`. A `GotoKindMsg` for the same kind, or a `SwitchNamespaceMsg` for the same namespace, returns a nil cmd. Nothing has loaded and no spinner tick is running, yet the root still pushes the instance. **(proved)** `New` + `Update(GotoKindMsg{Pod})` gives `cmd == nil`, `state == loading`; the namespace case is the same. Repro: Pods list → ↵ pod detail → `g` `p` ↵ gives a permanently "Loading Pods…" screen. The same happens from overview, flux tree or who-can: `g` to the kind you came from, or choose the `current` row in `n`. | Have `routeGoto` call `Init()` on what it builds (part of H1's `activate(…, fresh=true)`); the epoch guards drop the duplicate load. Also copy goToResource's `m.rows != nil` guard into `switchKind` and `switchNamespace`. Extend `TestGotoResourceOnAFreshInstanceAlwaysLoads` to both messages. |
| H4 | high | `tui/goto.go:47` (`maxGotoResults`), `:383-430` (`gotoResourceItems` returns after 50 items), `:289-316` (`palette.Filter` runs afterwards) | The cap is applied to the **unfiltered corpus**. Only the first 50 rows in cache order, current kind first, are searchable, so any other resource returns "no matches". This is a false claim of absence. **(proved)** With 61 pods, typing `zzz-target` (pod #61) renders `no matches`. Each keystroke also runs `resources.List` (full projection) over the current kind, and over other synced kinds until 50 items, plus a namespace list: O(N) work on the Update loop for each key. | Snapshot a names-only corpus when the palette opens (as `namespaceItemsCache` does). Filter first, cap after, then project status glyphs for the ≤50 survivors only. Add a test with more than 50 rows. |
| H5 | high | `tui/namespace.go:45-55` (`namespaceCountDescriptor` uses the current kind), `:255-266` (a `resources.List` per namespace, on the Update loop); `app/app.go:94-106`; `kube/helm.go:592-648` (one informer per namespace read) | On the Helm releases list, `n` asks for every namespace's releases. Each `ListRaw(HelmRelease, ns)` starts its own release-Secret informer. It also gzip-decodes every release and runs `UnsettledWorkloads` per namespace, synchronously, before the palette draws. This breaks "never read a cache breadth-first", and it is precisely what the per-namespace Helm cache exists to prevent: a cluster-wide 8 MB of manifests over a VPN, here spread across N separate LISTs. The first open also shows 0 counts for every unsynced cache. **(proved)** Five namespaces gave five `ListRaw(HelmRelease, ns-i)` calls on a single `n`. For other kinds the read hits a cache that has already started, but it still projects every object of the kind on the Update loop. | Count only kinds whose cache is already started for that namespace, and otherwise show the ghost dash (the same rule the goto palette and §5.6 apply). For Helm, always fall back to the Pod count. Better still, move the per-namespace counts into a `tea.Cmd`, as `fetchNamespaceCPUSharesCmd` already does, so `n` opens instantly. Add a recorded-actions test. |
| M1 | medium | `certchain` (no `CapturingInput`; confirm `update.go:154-162`), `helmhistory` (none; `update.go:150-157`), `secretdata/model.go:287-289`, `configmapdata/model.go:429-431`; root `tui/model.go:858-861`, `:946-957` | The root handles `g`, `:`, `n`, `c`, `U`, `q` and `?` for any Screen that isn't `CapturingInput()`. These four screens open an inline y/N (cert renew, Helm rollback, key removal) and advertise `n cancel`, but `n` opens the namespace palette and the confirm stays pending. **(proved)** On secretdata after `D`, pressing `n` leaves `PaletteOpen()==true` and `actions.Active()==true`. A bare ↵ in that palette alt-tabs to the previous namespace, and because secretdata is an `ObjectScreen`, a fresh browse is pushed. | Root-level: yield every key while the active Screen's `Keybar().Pill == ModeConfirm`, which all 13 confirm-capable screens already set. Or add `actions.Active()` to each `CapturingInput`. Add a root test that drives `n` through a ModeConfirm screen. |
| M2 | medium | `tasks/update/update.go:20-41`, `:45-…` (value receiver, returns `Model`); `app/update.go:111-118` (`&u`); `tui/model.go:871-877` | `(*Model).Update` resolves to a value-receiver `Update`, which returns a `Model` value. Because `SetSize` is on the pointer, that value doesn't implement `Task`, so `updated.(Task)` fails and the root silently keeps the old pointer. Every state change in 28b is lost: `feedback` ("command copied", "opened in browser", "… skipped", "check failed"), `checking` (so `r` can fire repeated rechecks) and `conn`. **(proved)** After a root-routed `UpdateCheckedMsg{Err}`, `feedback == ""`; a direct call gives "check failed — offline?". | Switch tasks/update to pointer receivers and return `m`. In the root, treat a non-`Task` `updated` as a programming error: log to diag or panic under test. A root test should push each factory-built task and round-trip one message. |
| M3 | medium | `tui/chrome.go:331-341` (`insetChromeLine` drops `right` when `gap < 1`), `:345-366`; goldens `test/golden/browse/{argo,certificates,crd-list,crd-instances}-80x24.golden` line 1 | When the crumbs don't leave room, the header drops sync, the update chip, the forward chip **and** the connection badge, including `◌ disconnected` / `credentials expired`. It never truncates the crumbs. **(proved)** With context `arn:aws:eks:us-east-1:123456789012:cluster/payments-prod` at **120** columns, the header shows no connection state. Every EKS user has an ARN context, and the checked-in 80-column goldens already show the badge missing. | Lay out right-to-left: reserve the conn badge, which always wins, then the chips, then sync. Truncate the crumbs in the middle (the context segment first, never the kind). Add an 80×24 golden with an ARN context and an offline state. |
| M4 | medium | `tui/help.go:122-148` (`helpColumnWidths`); `test/golden/help/80x24.golden` | Smallest-need-first allocation hands the whole budget to the three narrower columns and leaves the widest, LIST, at the 8-cell floor. At 80×24, the default terminal, every LIST label (`/ filter`, `1-9 …`, `↑↓ jk move`, `ctrl-d half-page`, `space mark`, `↵ open`) renders as `…`. The golden locks this in. | Water-fill: raise every column toward a common cap so all columns shrink evenly, or wrap labels onto two lines. Regenerate the 80×24 golden and assert that no column is fully ellipsized. |
| M5 | medium | `tui/model.go:1047-1057`; `docs/design/README.md` §7a | In the context palette, bare `r` re-probes and bare `P` toggles PROD, so neither letter can be typed into the fuzzy query. `prod`, `staging-eu-north`, `kind-cluster` and every `arn:…` context contain an `r`. Typing `prod` filters on `pod`. §7a sanctions the keys, but that contradicts "all share the same fuzzy input". | Bind re-probe to `ctrl+r` and mark-prod to `ctrl+p`, or honour bare `r`/`P` only while the query is empty. Update §7a and the palette's key row. |
| M6 | medium | — (systemic) | The `ConnState` / `ConnStateMsg` mix-up (07 M2, 11) can happen again because every task re-implements the case by hand and nothing checks the type. | Covered by H1's design: an AST guard test now, `Session.Conn()` reads later. |
| L1 | low | `tui/glyphs.go:3-5`; inlined runes in 14 files for `◌`, 5 for `◈`, plus `◐ ⏸ ‖ ◷ ⚠` (e.g. `resources/projections.go:173`, `:676`, `:737`, `resources/flux.go:97`, `argo.go:85`, `groups.go:46`, `registry.go:59`) | CLAUDE.md lists the ASCII fallback for `◈ ⧗ ▐ ◌` as the decided path. It isn't built, and the glyph-file comment's "one-file change" no longer holds: `resources` can't import `tui`, so the runes are copied as literals. The braille spinner also has no fallback. | Move the glyph constants into a leaf package (e.g. `internal/glyph`) that both `resources` and `tui` import. Add `glyph.ASCII()` behind the capability check. |
| L2 | low | `tui/layout.go:43-68` (`Truncate`/`PadLine`) vs hardened `components/layout.go:20-151` | Frame pads every header, strip and body line with `tui.PadLine`. That helper lacks all three fixes the go-practices fuzzing added to `components.Pad`. **(proved)** For width 10, `PadLine` returns 11 cells for `"\xe10"`, 0 for `"\x1b"` and 9 for `"؀"`; `components.Pad` returns 10 each time. It also ellipsizes with `"..."` rather than `"…"`. | Delete `tui.Truncate`/`PadLine` and call `components.Truncate`/`Pad`. |
| L3 | low | `tui/theme.go:157-226` | Light-theme contrast against `Bg` (WCAG): `Warn` 3.57 (dark 11.9), `Good` 4.11, `AccentMuted` 2.42 (dark 4.29). The 4a muted ramp (`Muted.*`) sits at about 2.6–2.9 in light against 2.8–4.3 in dark, so the offline view is noticeably harder to read in light. No test checks contrast. The values match the spec, so this is a spec change. | Add a contrast test on text-bearing tokens (≥4.5 for text, ≥3 for glyph-only). Darken light `Warn`/`Good` and the muted ramp in the spec and the struct together. |
| L4 | low | `tui/theme.go` (bg tokens); README "Test … on 256-color fallback" | Nearest-xterm-256 mapping by RGB distance collapses several background tokens. Light: `SelBg`, `MarkBg`, `ErrBannerBg` and `AllNsPillBg` all become 255, so marked rows and the selected row share one background. Dark: `SelBg`, `AllNsPillBg` and `BorderSubtle` all become 235, so the blue ALL NS pill and the purple pill look the same. **Unconfirmed**: colorprofile's real conversion may differ. | Add a 256-profile golden (`lipgloss.SetColorProfile(colorprofile.ANSI256)`) for browse with a marked row and the ALL NS pill. Choose bg tokens that land on distinct cube entries. |
| L5 | low | `nodedetail/view.go:322` | `time.Since(c.LastTransitionTime)` in the conditions render: a render-path clock read. It is the same family as 03 L2 / 04 L4 / 08 L1, but not filed for this site. | Use `m.now.Sub(…)`. |
| L6 | low | `tui/model.go:678-681` (unconditional `Location.Kind = msg.Kind`); `goto.go:608-611` (`pushRecentKind`); `session.go:149-159` | The synthetic kinds `Overview`, `WhoCan` and `FluxTree` open pushed screens, and `Event` is redirected the same way. The root still writes them into `Location.Kind`, and nothing restores the value on pop. Browse then shows Pods while `Location.Kind == "Overview"`. That value is persisted per context at exit, pushed into `RecentKinds`, and used as `current` by `gotoBrowseSelection`. `browse.New` only papers over it for Event and unknown kinds. | Write `Location.Kind` only for kinds that have a `Descriptor`. Alternatively, snapshot Location on push and restore it in `activate` on pop. |
| L7 | low | `tui/counts.go:137-181` | `fetchGotoCountsCmd`'s goroutine reads `sess.Registry.Descriptor` while the Update loop may replace `sess.Registry` on a CRD event (`model.go:758`, `:771`). That is a data race on the field, and CLAUDE.md requires Cmd prologue captures. | Capture `reg := sess.Registry` with `kinds` before the closure. |
| L8 | low | `EscapeBacker` implemented only by `execpicker/keys.go:41` and `debugpanel/keys.go:134`; ~20 screens return `BackMsg` from a Cmd | The input race `EscapeBacker` was introduced to fix still applies everywhere else. A key typed right after esc can land on the closing screen, e.g. `ctrl+d` after esc on pod detail begins a delete on the pod rather than acting on browse. Traced by code. | Invert the default: the root pops synchronously on esc unless the task reports local esc handling. `BackOnEscape` already encodes exactly that, so implement it on every pushed screen. |
| L9 | low | `tui/model.go:848-851`; `keycast.go:66-86` | `--keycast` records every printable key, including values typed into secretdata's masked buffer and the YAML-view secret edit, and shows them in the chip. That defeats 27b's "every message masks the value" exactly in the demo recordings keycast exists for. | Skip `record` while the active task reports a masked buffer, e.g. an optional `MasksInput() bool`. |
| L10 | low | `nodedetail/update.go:435-438` (filter: `up`/`down` only); `jobattempts/rerun.go:46`, `browse/job_actions.go:70` (rerun choice: `up`/`down` only) | `j/k` parity gaps. nodedetail's filter lacks the `ctrl+j/ctrl+k` that browse, events, timeline and podlogs offer. The create/replace rerun toggle has no `j/k` even though it has no text buffer. | Add `ctrl+j/k` and `j/k` respectively. |
| L11 | low | `tui/help.go:41-121` (no height cap); `components/overlay.go:38` (clamps `top` only) | No minimum-size guard (CLAUDE.md: decided, not built). On short terminals, Compose clips the help panel's bottom rows (MISC, `esc close`). The palette caps its own height, but help doesn't. | Cap help at `palettePanelMaxHeight` and scroll or truncate the columns. Build the guard screen. |
| L12 | low | `poddetail/view.go:799-804`, `objectdetail/view.go:316-321`, `certchain/view.go:314` | Byte-length pad and wrap helpers (`len(s)`, `s[:width]`) cut multi-byte runes and mis-measure wide characters. 07 L10 filed the fluxdetail copy. | Use `components.Pad` / `components.Wrap`. |
| L13 | low | `tui/model.go:560-562` (`routeGoto` requires a stack), `:678-713`; `tasks/setup/update.go:17-44` | On the 4c/10b setup root (stack empty), `g`/`n` open and dispatch normally, but setup ignores `GotoKindMsg`/`SwitchNamespaceMsg`. The pick is silently dropped while `Session.Location` is still overwritten. | Hide `g`/`n` on setup (a ModeSetup gate in `handleShellKey`), or show a "connect first" note. |
| L14 | low | `tui/goto.go:289-299` | §2b says typing searches "kinds/resources/namespaces/contexts", but the fuzzy corpus has no contexts. | Add context items that dispatch through `contextDispatch`. |
| L15 | low | 136 `KeyHint{Key: "…"}` literals in 28 task files (e.g. `setup/view.go:86`, `browse/keys.go:139`) | They bypass the verb registry (CLAUDE.md: "never hand-wire a keybinding in a view"). Key notation has drifted as a result: `ctrl+q`/`ctrl+d` vs `ctrl-d`/`ctrl-x`. Overlaps 10 L5. | Add a source-scan test against literal `Key:` in `tasks/**/keys.go` / `view.go`, with an allowlist for pure movement keys. Normalize to one chord spelling. |

## Details (high / medium)

### H1 + M6 — the activation contract: making the root the source of connection truth

**What happens today.**

- The root records each `kube.ConnStateMsg` in `m.conn` (`model.go:619`) and forwards it to `m.task` only.
- A task swaps in at eight places, and none of them tells the new task the connection state:

  | Site | Location |
  |---|---|
  | generic push | `:871-877` |
  | pop | `:888-898` |
  | `routeGoto` | `:560-574` |
  | `openUpdatePanel` | `:1255-1268` |
  | `ReplaceRootMsg` | `:593-605` |
  | 4c → setup | `:652-661` |
  | 4c → browse | `:641-650` |
  | context rebuild | `:739-744` |

- Each of the 27 task packages holds its own `conn kube.ConnState`, which starts at the zero value. The zero value has `Offline()==false`, so it reads as online.
- A parked task misses every transition while something else is on top.

The overlay proof (`internal/tui`, a minimal `connTask`) showed both failures:

- **Push.** The root is unauthenticated, but the pushed child's `Offline()` is false.
- **Pop.** The parent was parked while connected and the outage happened under the child. After esc, the root reports `unauthenticated` and the parent reports `connected`.

During `ConnUnauthenticated` the health loop never pings again, so neither screen self-corrects. 05 H1 lists the consequences: polling resumes, the plugin re-runs, and mutating verbs are enabled.

07 M2 and 11 add a second failure. Two screens switch on the wrong type (`kube.ConnState`), and nothing catches it, because every package re-implements the case by hand.

**Design.** Fix it in three layers, each independently shippable.

1. **One activation helper in the root** (fixes H1, H3 and half of H2):

   ```go
   // activate makes t the active task. fresh: the root (not the parent's
   // Update) constructed t, so nobody has called its Init yet.
   func (m *Model) activate(t Task, fresh bool) tea.Cmd {
       m.task = t
       m.resizeTask()
       var cmds []tea.Cmd
       if fresh {
           cmds = append(cmds, m.task.Init())
       }
       if m.conn.Phase != "" { // never seen a state → leave the task's default
           updated, cmd := m.task.Update(kube.ConnStateMsg(m.conn))
           if nt, ok := updated.(Task); ok {
               m.task = nt
           }
           cmds = append(cmds, cmd)
       }
       return tea.Batch(cmds...)
   }
   ```

   **Call sites, and what each passes.**
   - **Generic push** (`:876`): `fresh=false`. The parent already batched the child's Init into `cmd`.
   - **`popTask`**: `fresh=false`, then `Reload()` plus the H2 re-arm.
   - **`routeGoto`, `openUpdatePanel`, and every factory-built swap** (4c, `ReplaceRootMsg`, the context rebuild): `fresh=true`.

   **Why the replay is synchronous.** The replay is a direct `Update` call rather than a `tea.Cmd`. A Cmd would let the first frame render "● connected" and would let a key press reach an un-gated verb before the message arrived.

   **Why replaying is safe.** Every existing `ConnStateMsg` handler is idempotent for a repeated state. Browse and nodedetail only act on an *edge*. On a fresh browse with an outage in progress, the replay bumps `metricsEpoch`, which drops the poll that `Init`/`resetAndLoad` just armed. That is the desired outcome.

   **Two caveats.**
   - `ReplaceRootMsg` brings in a *new* cluster, so `m.conn = kube.ConnState{}` must be set before `activate`. Otherwise the replay would mark the reconnected browse offline.
   - The same applies to the `SwitchContextMsg` rebuild. Replay only once the new cluster has reported a state of its own, or reset `m.conn` in `switchContextCmd`'s result handling.

2. **Guard the message type** (M6). Add `internal/tui/connmsg_guard_test.go`. It parses `internal/tui/tasks/**/*.go` with `go/parser` and fails on any `*ast.CaseClause` in a type switch whose list contains the selector `kube.ConnState`. It is about 30 lines, catches 07 M2 and certchain today, and can't drift. A compile-time option is to give screens `SetConn(kube.ConnState) tea.Cmd` (interface `ConnAware`, plus a `var _ tui.ConnAware = (*Model)(nil)` per package) and have `activate` call it instead of sending a message. That is heavier, so do it only if layer 3 doesn't land.

3. **Make the root's copy the only copy** (follow-up). Add `Session.conn` (written in the root's `ConnStateMsg` case, on the Update goroutine) and `Session.Conn()`.
   - `LiveConnBadge` and every `offline()` gate read the session value instead of a per-task field. The first frame is then correct by construction.
   - The 27 per-task `conn` fields, and most `ConnStateMsg` cases, can go.
   - Only edge-triggered work keeps a handler: browse's and nodedetail's poll restart, and `actions.SetOffline`.
   - The crash report (`app/crash.go:150`) already reads the root's copy.

**Tests.**

- A root test with a recording task:
  - push while offline → the child sees the state before its first `View`;
  - outage while parked → after pop, the parent sees it;
  - `ReplaceRootMsg` → the conn state is cleared.
- A per-screen golden for one pushed screen opened offline (the OFFLINE pill).

### H2 — tick chains die on push and pop

Bubble Tea delivers a Cmd's message to the root, and the root hands everything to `m.task` (`:871`). A parked browse's `metricsTickMsg{epoch}` therefore reaches pod detail, which drops it as an unknown type.

On pop, `Reload()` (`browse/update.go:76-79`) bumps `reloadEpoch` and schedules a list reload. `load()` doesn't touch metrics, and `metricsTickMsg` has exactly four arming sites:

- `Init` (`model.go:969`);
- `resetAndLoad` (`:1372`);
- helm (`helm.go:150`);
- the offline→online edge (`update.go:145`).

So after ↵ pod detail → (> 2 s) → esc, the Pods table's CPU and MEM stop updating until the user switches kind or namespace. The header keeps advertising `sync 2s`. The same mechanism kills the cronjobdetail/jobattempts clock (06 M3), fluxdetail's 1 Hz tick (07 L5) and helmdetail's spinner (08 L7).

Fix it once in the contract, not per screen: document `Reloader.Reload` (or a new `Resumer.Resume`) as "re-arm every self-rescheduling chain, epoch-guarded". In browse:

```go
func (m *Model) Reload() tea.Cmd {
    m.reloadEpoch++
    cmds := []tea.Cmd{m.scheduleReload(m.reloadEpoch)}
    if m.pollsMetrics() {
        m.metricsEpoch++ // orphan any chain that did survive (short push)
        cmds = append(cmds, m.loadMetricsCmd(m.metricsEpoch), m.scheduleMetricsTick(m.metricsEpoch))
    }
    // + cron clock per 06 H1's clockEpoch
    return tea.Batch(cmds...)
}
```

### H3 — a fresh browse stuck on its loading skeleton

`routeGoto` constructs a browse with `m.buildBrowse()` and routes the message straight into `Update`, never calling `Init`. Browse's in-place handlers short-circuit on "no change". The comment at `browse/model.go:1199-1214` documents this exact trap for `goToResource` and fixes it there, but `switchKind` (`:1103`) and `switchNamespace` (`:1137`) keep the old guard.

The overlay proof against a browse built the way `routeGoto` builds it: `Update(GotoKindMsg{Pod})` and `Update(SwitchNamespaceMsg{"default"})` both return `cmd == nil` with `state == loading`. There isn't even a spinner tick.

The root's GotoKindMsg comment (`model.go:664-677`) shows that the ordering dance around `Session.Location` exists only to dodge this. With `activate(fresh, true)` running `Init()`, that dance becomes unnecessary.

### H4 — the goto palette can't find resources past #50

`gotoResourceItems` returns as soon as it has 50 items (`goto.go:425-427`). `gotoFuzzyItems` then filters that truncated list (`:315`). The overlay proof used 61 pods named `app-00…app-59` plus `zzz-target`; typing `zzz-target` rendered `no matches`. On any real namespace with more than 50 pods, most of them can't be reached through `g`, and the palette states that they don't exist.

The same function runs on **every keystroke**, and `resources.List` projects the full current-kind list (5k pods → 5k projections) before it stops at 50. That cost is on the Update loop, which is what `countCache` and `namespaceItemsCache` were introduced to avoid.

Fix:

1. When the palette opens, build a corpus of `{kind, ns, name}` triples for started caches. This is cheap with `meta.Accessor`; no projection is needed.
2. Fuzzy-filter that corpus.
3. Cap the result at 50.
4. Project status only for the survivors.

### H5 — the namespace palette's per-namespace fan-out

`namespaceCountDescriptor` uses the *current* kind, so on the Helm list the counting loop (`namespace.go:255-266`) calls `resources.List(HelmRelease, ns)` for every namespace. That path goes through `helmAwareLister.ListRaw` and `ListHelmReleaseSecrets(ns)`, and `ensureHelmSecrets(ns)` starts a new filtered Secret informer per namespace. The proof recorded five `ListRaw(HelmRelease, ns-i)` calls for five namespaces from one `n` press.

On first open each cache is unsynced, so the palette also reports `0` releases everywhere: an empty-state claim without a sync check.

`sessionScoped` guards only `--namespace-scoped` mode. The Helm kind is per-namespace in *every* mode (CLAUDE.md, `kube/helm.go:579-591`), so it needs the same treatment unconditionally.

### M1 — shell keys stolen from inline confirms

`handleShellKey` runs whenever `!taskCapturingInput(m.task)`. Browse, poddetail, nodedetail, objectdetail, cronjobdetail, jobattempts, timeline and debugpanel all include `actions.Active()` in `CapturingInput`. These four screens don't:

- certchain and helmhistory don't implement it at all;
- secretdata checks only `adding`/`editing`;
- configmapdata checks only `adding`/`editing`/`multiline`.

All four route `n` to `Cancel()` in their `updateConfirmKey`, but the key never reaches them. Proved on secretdata (`D` then `n`): the palette opened and the removal confirm stayed pending. `q` opens the quit confirm, and `y`/↵ there quits.

The simplest root-level fix uses data every confirm already provides: their `Keybar()` returns `Pill: tui.ModeConfirm` (13 packages). The root can treat a ModeConfirm keybar as capturing.

### M2 — 28b loses its own state

`tasks/update` is the only task whose `Update` has a value receiver. `buildUpdateFactory` returns `&u`. The root calls through the pointer, gets a `Model` value back, finds that it doesn't implement `Task` (`SetSize` is on `*Model`), and keeps the stale pointer.

Proved: after a root-routed failed check, `feedback == ""`, while the direct call gives `"check failed — offline?"`. The feedback strings from `y`, `o` and `x` never show, `checking` never becomes true, and `conn` is never updated. The package's golden and unit tests call `Update` directly, so they pass.

### M3 / M4 — layout at real widths

**M3 (header).** `insetChromeLine` is all-or-nothing for `right`. `test/golden/browse/argo-80x24.golden` line 1 already shows `kute │ c microk8s-cluster › n argocd › g Applications argoproj.io/v1alpha1` with no sync and no badge. With an EKS ARN context, the proof at 120 columns drops `sync 2s  ◌ disconnected` entirely. The connection badge is the one right-side item that must never disappear, because it is how 4a announces an outage on screens without a stale strip.

**M4 (help overlay).** `helpColumnWidths` fills small columns first. At 80 columns, LIST (natural width about 20) is left at the 8-cell floor, and the golden shows `/      …`, `1-9    …`, `↑↓ jk  …` and so on.

## Test gaps

- **Root activation** (H1, H2, H3). Nothing pushes or pops a task and asserts what it was told, or that its tick chains survive. Add:
  - push-offline;
  - outage-while-parked + pop;
  - `ReplaceRootMsg` clears conn;
  - pop re-arms a parked tick;
  - `routeGoto` to the current kind or namespace loads (needs a real browse, so it belongs in `internal/app`, or a browse-package test that mimics `routeGoto`).
- **Message-type guard** (M6). An AST test for `case kube.ConnState:`.
- **Non-`Task` return** (M2). An app-level test that builds every factory task (setup, browse, update) behind the real root and checks that one state change survives a root-routed message.
- **Goto corpus** (H4). A test with more than 50 rows, plus a benchmark of `gotoFuzzyItems` at 5k pods per keystroke.
- **Namespace palette reads** (H5). A recorded-actions test: on the Helm list, `n` must not call `ListRaw(HelmRelease, ns)` for any namespace except the current one.
- **Confirm key ownership** (M1). For each ModeConfirm screen, `n` must reach the task and the palette must stay closed.
- **Layout.**
  - Header goldens at 80 and 120 columns with an ARN context, offline (M3).
  - A help golden assertion that no column is entirely `…` (M4).
  - 256-colour goldens (L4).
  - A light-theme contrast test (L3).
- **Fuzz.** Extend `layout_fuzz_test.go` to `tui.PadLine`, or delete it (L2).

## Quick wins

1. M2: make `tasks/update` use pointer receivers. That is about five signatures and fixes all of 28b's feedback.
2. H3: add `m.rows != nil` guards in `switchKind`/`switchNamespace`, and/or call `fresh.Init()` in `routeGoto`.
3. H1: replay `m.conn` synchronously after the push at `model.go:876` and in `popTask`. That is about ten lines and covers 05 H1 for every screen, before the full `activate` refactor.
4. M1: treat a `ModeConfirm` keybar as capturing in `handleShellKey`. One condition.
5. H5: in `namespaceCountDescriptor`, fall back to Pods for `KindHelmRelease`. One condition stops the fan-out today.
6. H4: move the 50 cap after `palette.Filter`. A one-line reorder fixes correctness; the snapshot fixes performance.
7. L2: delete `tui.Truncate`/`PadLine` in favour of `components.Truncate`/`Pad`.
8. L7: capture `sess.Registry` in `fetchGotoCountsCmd`'s prologue.
9. L5: `nodedetail/view.go:322` → `m.now.Sub`.
