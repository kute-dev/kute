# kute — alpha → beta plan

Everything outstanding between `v0.3.0-alpha.4` and a `0.4.0-beta.1` tag, with the
reasoning for why each item gates a beta rather than being post-beta polish.

The remaining distance to beta is not features. It's a frozen public surface,
evidence the app works somewhere other than one AKS cluster, and a path for a user who
hits a bug to tell us something useful.

---

## What "beta" means here

The gates a `0.4.0-beta.1` has to clear:

1. **Keys, persisted-state schema, and config schema are frozen.** Breaking changes from
   beta on come with a version bump and a migration, not a surprise.
2. **No known-broken screens.** Every open finding is either fixed or a recorded,
   deliberate deviation — nothing left that shows the user something untrue.
3. **Validated beyond one cluster.** More than one distribution, more than one auth mode,
   a restricted user, and a cluster big enough to hurt.
4. **A bug is reportable.** Someone hitting a crash or a wrong render can produce
   something we can act on without a screen recording.

---

## 1. Terminal robustness

Three items the design docs record as decided but unbuilt. This is the class of bug that
generates "kute is broken" reports from users whose terminal we never tried.

- **Minimum-size guard screen.** CLAUDE.md lists it as decided, not built. Nothing in the
  code checks for a too-small terminal today (no width/height floor anywhere in the render
  path), so a small window silently produces mangled frames.
  *Acceptance:* below the floor, every screen renders one legible "resize to at least
  N×M" panel instead of a broken layout.
- **ASCII glyph fallback.** `internal/tui/glyphs.go` was centralized specifically to allow
  it ("so a future ASCII…") and the substitution set is decided (`◈ ⧗ ▐ ◌`), but no
  fallback path exists.
  *Acceptance:* a terminal without the exotic glyphs renders the ASCII set with no
  column-width drift — the goldens are the check.
- **Verify colour downsampling before building the decided 256-colour path.** The old
  termenv-based plan may be obsolete: the app is on lipgloss v2 with
  `github.com/charmbracelet/colorprofile v0.4.3`, which downsamples at the writer. Confirm
  what actually happens on a 256-colour and a 16-colour terminal, then either build the
  mapping or delete the decision.
  *Acceptance:* a recorded answer for truecolor / 256 / 16 / `NO_COLOR`, and no theme
  token that becomes unreadable in any of them.

Walk all three in Windows Terminal, tmux, and a 16-colour Terminal.app.

## 2. Validation matrix

Every piece of real-cluster evidence today comes from one AKS cluster over an SSH
port-forward, qualitative, one user, one auth mode
([`lazy-informers.md`](lazy-informers.md) §"Observed on the real cluster").

| Target | What it actually tests |
| --- | --- |
| EKS | `aws eks get-token` exec-credential plugin — see [`managed-clusters.md`](managed-clusters.md); the auth failure class is now handled and unit-tested against fixture plugins, so this row is down to a manual walk for discovery differences and Fargate |
| GKE | `gke-gcloud-auth-plugin`, plus a different metrics-server shape — same as above; the remaining unknowns are the metrics shape and Autopilot's admission rules |
| kind or k3s | the smallest realistic cluster; no metrics-server by default |
| A large cluster (5k+ pods) | table paging, informer memory, and whether lazy start holds up |
| A restricted ServiceAccount | the 403 paths on *every* screen, not just `browse`'s 4b card |
| A cluster with no metrics-server | that CPU/MEM render `–` everywhere rather than lying or crashing |

*Acceptance:* each row walked through the everyday flows (list → detail → logs → events →
timeline → a mutating verb) with results written down, not just "worked".

## 3. Decide §17a (YAML edit mode)

Simple edit mode is OK for now.

## 4. Diagnostics and a bug-report path

A TUI crash currently leaves the user with nothing to attach. There's no log destination,
no crash context, no issue template.

This is diagnostics for a user who cannot otherwise report a bug.

- `--log-file <path>` for the error/klog stream (klog is already wired through a private
  flagset in `internal/app`).
- Version, context, active kind, and terminal size in the crash footer.
- A pre-filled GitHub issue template asking for exactly those.

*Acceptance:* a deliberately-crashed build produces a file a maintainer can diagnose from.

## 5. State the freeze

- Replace the README's "Interfaces, keybindings, and screens may still change between
  releases" with the actual beta contract from the gates above.
- Confirm the state schema's migration hook still works end to end (schema is at v2 with a
  live `migrate`, so this is a verification, not a build).
- Decide whether `verbs.Verb.ID`'s "future key-remapping hook" is part of the frozen
  surface or explicitly not yet.

## 6. Design-spec rulings — the audit's last 6

None of these are broken code. Each is a place where the implementation took a reasoned
position and the spec was never updated, so each needs a one-line decision and then an
edit on whichever side loses. Detail and citations in
[`design-fidelity-delta.md`](design-fidelity-delta.md).

| # | Ruling needed |
| --- | --- |
| 6b collapsed healthy-group line | Spec says green; code renders `TextFaint` on purpose ("a fully-healthy namespace has nothing to triage"). Pick one. |
| Secret-reveal indicator | Spec says bordered chip; code renders a filled pill as a terminal-idiom substitution. |
| `/` filter mechanisms (2 findings: §5b + cross-cutting) | Three implementations — fuzzy+highlight (`browse`, `nodedetail`), plain substring (`events`, `timeline`, `podlogs`), whole-row substring (`yamlview`). Either the spec acknowledges the split (fuzzy for names, substring for prose) or the screens converge. |
| `HTTPRoute` branch in the CRD registry | `crd.go:372` special-cases one kind name for §23b's ATTACHED column, against "CRD support is data, not code". Accept it as a documented carve-out in CLAUDE.md, or move the column onto a discovered-kind capability so the branch disappears. |
| §2a keybar text (`docs/design/README.md:32`) | Pure doc drift: names a `describe` verb that never existed and swaps `e`/`x` versus what ships. Rewrite the line, or delete it in favour of the 0.3.0 mockups that superseded it. |

## 7. Incidental, found while working

Neither is an audit finding; both are wrong in the tree right now.

- **`execpicker`'s "will run" line ellipsizes at the panel's fixed 56 cells**, so for a
  realistic pod name the trailing `-- bash` is cut off. §10a calls that line "copyable
  documentation", which it currently isn't for most pods. Wrapping it to two lines inside
  the panel is the obvious fix.
  `internal/tui/tasks/execpicker/view.go` (`willRunLine`, `panelWidth`)
- **`website/index.html:316`** claims drain gets a "type-the-name modal — the only red
  border in the app". Drain confirms with `y/N` via `components.ConfirmCard`, and
  `TypeNameModal` is a second red-bordered surface. Marketing copy, so the wording is a
  judgement call.

---

## Sequencing

1. **`0.4.0-alpha.1`** — §1 terminal robustness and §7's two fixes. Small, self-contained,
   and §1 is the one that changes what users see.
2. **`0.4.0-alpha.2+`** — §6's five rulings (fast once decided) and §3's §17a decision.
3. **Then** §2's validation matrix, since it's the long pole and wants a stable build to
   test against. §4's diagnostics ideally land before it, so the matrix run produces
   attachable output.
4. **`0.4.0-beta.1`** once the matrix passes and §5's freeze is written down.
