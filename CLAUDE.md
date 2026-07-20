# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

TUI Kubernetes console (Bubble Tea + Lip Gloss). 

Design spec: `docs/design/README.md` (source of truth for screens, layout, tokens, keys, glyphs).


## Invariants — never violate

- **No hex color literals in view code.** All colors come from the semantic `Theme` struct (dark + light variants live in one file). New UI = new token if needed, never an inline color.
- **Both themes always.** Any new surface must define/reuse tokens that work in dark and light; test both.
- **Every verb goes through the command registry.** Key, label, destructiveness tier, applicable kinds — keybar, help overlay, and confirm flow render from it. Never hand-wire a keybinding in a view.
- **Destructive-action policy:** reversible verbs execute immediately; delete = inline y/N in non-prod, type-the-name modal in PROD contexts (kubeconfig annotation, never a name heuristic); drain/force-delete always modal. Red borders are reserved exclusively for destructive confirms.
- **Resource kinds are registry entries**, not bespoke screens. New kind = columns + status derivation + verbs registered on the shared list/detail skeleton.
- **Render functions are pure**: f(model, theme, size). No I/O, no clock reads, no globals in render paths.
- **All cluster access goes through the `ClusterProvider` interface.** UI code never imports client-go directly. The fake provider must stay feature-complete for tests/demo mode.
- **Persisted state is versioned.** Any change to the recents/perContext schema bumps the version and adds a migration.
- **One palette shell** parameterized by scope (goto/namespace/context) — never fork it.
- **Aliases are reserved for the ~8 built-in daily kinds** (p d s i n c e) and are always the kind's first letter, rendered as a highlighted first letter — never a chip. Aliases re-rank to rank 1, never fire instantly. CRDs and the long tail never get them.
- **CRD support is data, not code.** Discovered kinds become kind-registry entries from API discovery (printer columns → columns, Ready-style condition → status, else neutral). No per-CRD layout code, ever. Deleting a CRD always gets the type-the-name modal.
- **Forwards are a registry kind** on the shared list skeleton, global and session-scoped; a failing forward may only change the header chip color — never a modal or banner over unrelated screens.

## Conventions
- Status glyphs, key vocabulary, and mode-pill hues per the design README — don't invent alternatives.
- `j/k` ≡ `↑↓` everywhere; `esc` always walks back exactly one level.
- Watch-based updates, not polling (metrics poll on the sync interval only).
- Terminal capability degradation: 256-color palette mapping (via termenv) and ASCII substitutes for exotic glyphs (`◈ ⧗ ▐ ◌`) are the decided fallback path; a minimum-terminal-size guard screen is decided but not yet built.

## Workflow
- After UI changes, run the golden-file snapshot tests (both themes) and update intentionally-changed snapshots only.
- New screens/verbs: update the command registry and kind registry first; views follow.
- Commit messages: `type(scope): short imperative description`, lowercase, one focused line, no trailing period. Match the granularity of the change — e.g. `build(deps): bump go module dependencies, k8s.io to v0.36.2`, `ci(pages): pin workflow actions to commit SHA`. Avoid vague messages like "update deps" or "fix stuff"; name what actually changed.

## Commands

```sh
go run ./cmd/kute      # run the app (or ./run.sh)
go test ./...            # all tests
go test ./internal/tui/tasks/browse -run TestSomething  # single test
go vet ./...
UPDATE_GOLDEN=1 go test ./internal/tui/tasks/podlogs   # regenerate pod-logs golden fixtures
```

Tooling versions are pinned via **mise** (`mise.toml`: go 1.26.4, kubectl 1.35). `KUBECONFIG` is set in `mise.toml` to `{{config_root}}/.kube/config` (repo-relative, gitignored — every checkout gets its own, no machine-specific setup needed); the app also falls back to `~/.kube/config` and then in-cluster config. Run `mise install` to get the pinned toolchain.

## Architecture

Layered, with the TUI depending on `kube` only through interfaces so screens can be tested without a live cluster.

- **`cmd/kute/main.go`** — thin entry point; calls `app.Run()`.
- **`internal/app`** — composition root. `NewModel` builds a `kube.Cluster` (real cluster, informer-backed) or a `kube/fake.Cluster` (`--demo`) and roots the model at `tasks/browse`, injecting it as the `resources.RawLister` and `browse.MetricsReader` seams and wiring an `OpenLogsFunc` that pushes `tasks/podlogs` for the selected pod. `RunWithConfig` starts the informers (or the demo fake's event loop) and pipes both resource-change and connection-state events into the program. If no cluster is reachable and `--demo` isn't set, `browse` still renders, in an error state carrying the real connection error (`browse.Config.InitErr`). This is the only place real Kubernetes wiring happens.
- **`internal/kube`** — Kubernetes access. Defines domain structs (`Pod`, `PodMetrics`, `Context`, `ResourceKind`, `ConnState`, `Event`), one-shot/`Client*` readers, and `Cluster` (shared informer factory + `PodMetricsByNamespace` + watch/conn-state events). Write access is the narrow `Mutator` interface (`DeleteResource`, `DeleteResourceForced`, `Cordon`, `Drain`, `RolloutRestart`), implemented on `Cluster` and called from `browse`/`poddetail` (delete), `browse`/`nodedetail` (cordon/drain), and `browse` (Deployments' `R` rollout-restart) via `actions.Controller`. Event reads (`ObjectEvents`, `NamespaceEvents`) back `poddetail`'s EVENTS grid and the `events` (9b) screen. Consuming *read* interfaces (`resources.RawLister`, `browse.MetricsReader`, …) are declared **in the packages that use them**, not here — so `kube` has no dependency on `tui`.
- **`internal/resources`** — the resource catalog: `Descriptor`/`Registry` (per-kind columns, projection, health tallying/labeling), `Group`s, `columns.go` (`components.Column`/`Cell` specs per kind), and `List`/`Count` over a `RawLister`. Kind-agnostic; drives `browse`.
- **`internal/tui`** — root model (`Model`, task stack, palette/help overlay routing), Chrome v2 (`Screen`/`Frame`), the semantic `Theme`/`Styles`, `glyphs.go`, `verbs` (the keybar/help/confirm verb registry), and `actions` (the shared confirm→execute controller). No pre-redesign chrome remains as of Phase 6.
- **`internal/tui/components`** — reusable stateless renderers: `table` (the inverted-layout table), `bar` (CPU/MEM mini-bars), `overlay` (dim+compose a modal panel over a base view), `card`, `confirmmodal` (`ConfirmCard` — the minimal y/n confirm card `browse`/`nodedetail`'s cordon/drain verbs still use; `TypeNameModal` — 8b's type-the-name PROD delete confirm, the app's other red-bordered surface), plus `palette` (the one jump/namespace/context modal shell) as its own subpackage.
- **`internal/tui/tasks/*`** — one package per screen: `browse` (the one resting screen for every resource kind — table, health strip, filter, empty state; Deployments get a ROLLOUT/IMAGE column pair and `R` rollout-restart as of Phase 7), `poddetail` (5a pod detail — status/termination banner/meta grid/containers/bars/events/sidebar, pushed from `browse`'s Pods list and `nodedetail`'s pod rows on `↵`), `nodedetail` (11b node detail — conditions/allocated-allocatable/taints facts panel over the node's own pods table, pushed from `browse`'s Nodes list on `↵`), `yamlview` (8a read-only syntax-highlighted YAML view, pushed by `y` on any object from `browse`/`poddetail`/`nodedetail`), `podlogs` (5b streaming log viewer — toolbar with container/since/wrap/timestamps, severity-colored lines, restart boundaries, on Chrome v2 as of Phase 6), `events` (9b deduped, severity-colored event feed — hand-rolled two-line rows, not `components.Table` — namespace-scoped from `browse`'s `e` or object-scoped from `poddetail`/`nodedetail`'s `e`, `↵` jumps to the involved object via a `tea.Sequence(BackMsg, GotoResourceMsg)`), `execpicker` (10a exec container picker — pushed by `x` on a Pod with more than one container from `browse`/`poddetail`; a single-container pod execs immediately without this screen).

### The Task contract and navigation stack

`tui.Model` is the root Bubble Tea model. It wraps a single active `Task` and keeps a `stack []Task` for back-navigation:

- `Task` = `tea.Model` + `SetSize(w, h)`. Every screen implements it.
- When a task's `Update` returns a *different* task instance, the root pushes the current one onto the stack (this is how `browse` opens `podlogs` via the injected `OpenLogs` callback, on `l`).
- A `tui.BackMsg` pops the stack. Screens send `BackMsg` instead of quitting to return to the previous screen.
- The root also fans `tea.WindowSizeMsg` out to the active task's `SetSize`.

### Task package conventions

Each task package is split into files by responsibility: `model.go` (struct + `New`/`Init`/`SetSize`), `update.go` (message handling), `view.go` (rendering), `keys.go` (key bindings) — `browse` also has `sort.go`, `filter.go`, `selection.go`, `metrics.go`, and `hints.go` for its extra concerns. State is tracked with the shared `tui.TaskState` enum (`loading`, `ready`, `empty`, `error`, `permission-denied`, `confirming`, `success`, `cancelled`).

Mutating actions are designed to go through a `confirming` state before executing: screens embed an `actions.Controller`, press a mutating key → `Begin(tier, tui.TaskAction)` (the caller resolves `tier` via `verbs.TierFor(verb, isProd)` — `TierNone` verbs like cordon execute immediately; `TierInline`/`TierModal` move to `confirming`), then either `y`/`n` (`TierInline`, and every `TierModal` verb except delete/force-delete — e.g. Drain) or typing the resource's name + `enter` (`TierModal` delete/force-delete only, via `Confirm`/`TypeRune`/`Backspace`/`Escalate`) route to execution, and the `actions.ResultMsg` returned feeds `HandleResult`. Execution runs through `kube.Mutator`, so no screen calls a write verb directly. `browse`'s Nodes list and `tasks/nodedetail` call this for `C` cordon/uncordon (`TierNone`, no confirm) and `D` drain (`TierModal`, still `components.ConfirmCard`'s minimal inline card, unchanged since Phase 9 — deliberately not upgraded to the type-the-name modal). `browse` (any row, any kind) and `tasks/poddetail` call it for `ctrl-d` delete, which *is* tier-driven: inline `y/N` in non-prod, `components.TypeNameModal` (type-the-name + `ctrl-k` force-delete escalation) when the active context is tagged prod.

Namespace/context switching now dispatches for real: the root shell's `g`/`n`/`c` keys each open the one `components/palette` overlay (scoped Goto/Namespace/Context — `internal/tui/goto.go`/`namespace.go`/`context.go` build its `Item`s from `Session`), and `Enter` sends a navigation message (`GotoKindMsg`/`GotoResourceMsg`/`SwitchNamespaceMsg`/`SwitchContextMsg`) that the root `Update` uses to keep `Session.Location` authoritative before forwarding to the active task — `browse` handles all four. A context switch is the one that actually rebuilds the cluster: `kube.Cluster.SwitchContext` runs in a `tea.Cmd` (blocking, since it re-syncs informer caches), so every `Session` read/write around it (recents, `PerContext` restore) happens synchronously *before* the `tea.Cmd` closure is built — the closure itself only touches the stable `*kube.Cluster` pointer. `?` opens a help overlay built from the active `Screen`'s own `Keybar()` (current-view column) plus `Session.HelpScope`/`HelpGlobal` (the fixed SCOPE/GLOBAL columns).

Dependencies are always passed via a package-local `Config` struct with interface-typed fields, and `New` fills in defaults for zero values. Follow this when adding a screen: define the interface you need in the task package, implement it on a `Client*` type in `kube`, and wire it in `app.NewModel`.

## Testing

Heavy use of table tests plus **golden-file** rendering tests under `test/golden/`, all following the same `UPDATE_GOLDEN=1`-guarded `TestGenerateGoldenFixtures` + comparison-test pattern:

- `internal/tui/components` (the `Table` component itself) against `test/golden/table/*.golden`.
- `internal/tui/tasks/browse` (full-screen 2a renders, 120×36 and 80×24) against `test/golden/browse/*.golden`, plus **forced-truecolor goldens in both themes** (`test/golden/browse/120x36-{dark,light}.golden`, ANSI included) — the fixtures that actually pin the Theme-token-to-cell color mapping, since the plain goldens render colorless under test. This is the pattern to copy for new theme-sensitive screens (`truecolorGoldenFixtures` in `browse/golden_test.go`): force the profile with `lipgloss.SetColorProfile(termenv.TrueColor)` + deferred restore. The profile is a global, so packages with truecolor goldens must not use `t.Parallel` in tests that render.
- `internal/tui/tasks/podlogs` against `test/golden/podlogs/`.

When you change rendering output, regenerate with `UPDATE_GOLDEN=1 go test ./path/to/package` and review the diff rather than hand-editing fixtures. Tasks are tested by driving `Update` with synthetic messages and asserting on `View()`/`Render()` output or state — no live cluster needed because all `kube` access is behind interfaces (see `browse`'s `fakeLister`/`fakeMetrics` test helpers, or `kube/fake` for a whole-cluster fixture set).
