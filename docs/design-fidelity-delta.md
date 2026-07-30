# kute — Design-Fidelity Audit: delta against `main`

Re-verification of every finding in [`design-fidelity-audit.md`](design-fidelity-audit.md)
against the tree at `61cdafd`, 103 commits after the audit was written. Each finding was
re-checked at its cited code location (or wherever that code moved to), not re-read from
the report.

**53 of 59 findings are closed. 6 remain open — all of them spec-vs-code rulings, not
defects: five decided deviations the spec text never absorbed, one pure doc drift.**

Two fixes landed the same day this delta was written (`3e9e5cd`, `8975c5f`) and have moved
to [Closed since the audit](#closed-since-the-audit). Only one of them closed a *finding*
(§10a's hardcoded shell list); the other hardened a position this delta had recorded as
already-decided, so the count moves by one, not two. What's left needs a decision, then an
edit on whichever side loses; the open items are tracked as work in
[`beta-plan.md`](beta-plan.md) §6.

A note on the count: the audit's headline says "69 findings total — 9 invariant · 13
missing · 47 fidelity gaps", but the report enumerates **59** bullets (8 invariant · 13
missing · 38 gap). The headline overcounted; 59 is the real number and what this delta
tracks.

---

## Still open

### 1. 6b's collapsed healthy-group line renders gray, not green

`groupLineStyle` uses `TextFaint` for `rowKindCollapsedSummary`; §6b specifies green. The
deviation is code-commented as intentional ("a fully-healthy namespace has nothing to
triage, so it's deliberately de-emphasized rather than drawing the eye with green"), and
the spec still says green. Needs a design decision, then whichever side loses gets edited.

spec `docs/design/README.md:99` · code `internal/tui/tasks/browse/view.go:991-1007`

### 2. Secret-reveal indicator is a filled pill, not a bordered chip

§21a says bordered; `revealedTag` renders a filled pill, documented as a terminal-idiom
substitution. The *truncation* half of this finding — the tag silently disappearing off
long values — is fixed (`yamlview/view.go:228-247` now reserves the tag's width).

spec `docs/design/README.md:273` · code `internal/tui/tasks/yamlview/view.go:257-263`

### 3. `/` filter resolves to three different mechanisms

Unchanged, and each divergence is still individually justified in a comment:

| Screen | Mechanism |
|---|---|
| `browse`, `nodedetail` | `sahilm/fuzzy` + per-character match highlighting |
| `events`, `timeline`, `podlogs` | plain case-insensitive substring, no highlight |
| `yamlview` | substring, whole-row highlight |

The spec frames filtering as one shared mechanism. Either the spec acknowledges the split
(fuzzy for names, substring for prose) or the screens converge.

spec `docs/design/README.md:298` · code `browse/filter.go:19`, `nodedetail/filter.go:5`,
`events/view.go:170`, `podlogs/model.go:243`, `timeline/update.go:183`

### 4. Podlogs `/` doesn't use the table's filter grammar

The same split as above, called out separately by the audit for §5b. Same resolution
either way.

code `internal/tui/tasks/podlogs/model.go:243-254`

### 5. One kind-name branch survives in the CRD registry

`crd.go:372` still special-cases `dk.Kind == kube.KindHTTPRoute` to attach §23b's
ATTACHED column — the sole per-kind branch in the CRD path, against the "CRD support is
data, not code" invariant. Narrow and documented. Accept it explicitly in CLAUDE.md as a
carve-out, or move the column onto a discovered-kind capability so the branch disappears.

### 6. §2a's keybar text in the spec no longer matches any code

Pure doc drift, no code defect: the spec's `↵ open · l logs · d describe · e exec` names
a `describe` verb that has never existed, and swaps `e`/`x` versus the shipped bindings
(`e` = events, `x` = exec). The 0.3.0 keybar re-curation (`caffc5e`, `b63daec`) changed
the keybar and the spec line was never updated.

spec `docs/design/README.md:32` · registry `internal/tui/verbs/verbs.go:73-130`

---

## Closed since the audit

Grouped by how they were closed. Every one was verified at its code location.

### Fixed in code (48)

**Navigation & chrome** — the GOTO mode pill renders (`model.go:1102-1112` splices an
undimmed keybar line over the overlay; `chrome.go:386-390` gives it `SelBg`/`AccentHi`,
bold), the HELP pill got the same treatment, `tab` completes the query
(`model.go:696`), 12a's footer carries the "colored first letter ·" clause
(`goto.go:275`), the help overlay's off-by-two truncation is fixed with the reasoning
recorded inline (`help.go:60-68`) **and now has golden coverage**
(`internal/tui/help_golden_test.go`, `test/golden/help/`), 6b's group header renders
right-aligned trouble chips over a `BgChrome` fill (`groupHeaderChips`), 7a has its
NAMESPACE/STATUS column headers (`context.go:22-26`) and a genuinely bordered PROD chip
(`palette.go:588`, `ProdBorder` brackets).

**Resting screens** — CPU/MEM cells render `–` (`browse/view.go:1026`); §15a's
cached-rows-dimmed revisit view exists (`browse/model.go:291-298`, `rowCache` keyed by
kind+namespace).

**Error & connectivity** — the 4a banner has its real `ErrBannerBg` fill plus a border
rule line (`offlineBannerLine`), all four 4b recovery lines carry their explanatory
clauses (`permissionDeniedBody`), and 4c's headline feature shipped: a bordered,
selectable, **pre-probed** SWITCH CONTEXT list with per-context reachability and latency
(`setup/view.go:203-266`, `switchProbeMsg`, `m.probes`). The wrapped-error truncation bug
is fixed at the root — `components.Truncate` now recurses per line and cites §4c for why
(`layout.go:9-33`) — and the raw-error box is bordered in `ErrCardBorder`.

**Pod detail & logs** — title status glyph restored (`titleLine`), MEM bar turns red at
96% via `MiniBarBadAt(…, 0.96)`, `LastTermination.NextBackoff()` estimates kubelet's
backoff (`kube/pods.go:87-93`), poddetail escalates its own Warning events to red
(`view.go:421-431`), CONTROLLER resolves the ReplicaSet→Deployment hop
(`poddetail/model.go:172-173`), and only the latest ERR line gets the full-width tint
(`podlogs/view.go:125-127`).

**YAML, confirm, secrets** — 8b shows the concrete grace period from
`Pod.GracePeriodSeconds` (`delete.go:113-119`); 21a reserves width for the `revealed`
tag.

**Deployments, events, nodes** — 9b's rows are on the spec's 5-column grid (`65c875a`),
the Deployments pill reads `DEPLOY`, IMAGE shows `new ← old` mid-rollout
(`projections.go:286-301`), Nodes' STATUS dims when Ready (`browse/view.go:877`),
11b's pressure line appends age without a dangling separator, NotReady is red in both
places, and ALLOCATED/ALLOCATABLE text turns yellow when hot (`nodedetail/view.go:317`).

**Exec & forwarding** — native sidecars are detected via KEP-753 and labeled
(`kube/pods.go:66-68`), the forward chip is on `execpicker`/`forwardpicker`, and 13c's
breadcrumb says `∗ all namespaces` (`browse/view.go:81-88`).

**Custom resources** — the breadcrumb carries the dim `group/version` tag
(`browse/view.go:75-78`), CRD kinds get capitalized display names
(`capitalizePlural`) with `CRDS`/`DEPLOY`/`HELM` short pills, the neutral fallback glyph
is `TextFaint` and its "no status semantics · NAME + AGE only" strip copy renders
(`browse/view.go:259`), 14b's strip says "N established · M installing" plus
"N definitions · M API groups · sorted by group", and CRD rows genuinely sort by group
(`sort.go:56-60`, `crdGroupCell`).

**Scale, routing** — 17b's HPA note has a real backing kind
(`kube/kinds.go:64-71`, `browse/auxkinds.go:30`), Ingress joined the unhealthy-first set
(`sort.go:25`), TLS-strip secrets are selectable and open on `↵`
(`openSelectedTLSSecret`), and a Gateway listener's `↵` filters to its attached routes
(`selectedListenerRouteFilter`).

**Cross-cutting** — `verbs.Verb.Mutating` has a runtime reader, offline UI reaches
`poddetail`/`nodedetail`/`objectdetail`/`helmhistory`/`timeline`/`secretdata`/`configmapdata`,
`f` (port-forward) is wired in `poddetail:171` and `nodedetail:188`, and the
"N hidden by filter — esc to clear" notice is in all five filterable screens.

### Closed by demo-fixture work (3)

All three "the fake provider must stay feature-complete" invariant violations are gone:
`demoUsageRatio` gives every running demo pod real CPU/MEM in the Fill/Warn/Bad ranges
(`fake/fake.go:748-752`), and `apiPod` gained a `metrics-sidecar` container
(`fake/fixtures.go:54-62`) so §10a's picker is reachable by driving `--demo`. The fake
dialer now has a deliberately flaky pod so 13c/13d's failing/retry/backoff states are
reachable too (`fake/forward.go:91-102`).

### Closed by updating the spec (1)

The CPU/MEM bar glyphs: §11a now specifies `▮▮▮▮▯▯` (`docs/design/README.md:160`),
matching the 0.2.0 mockup's own 25a bars and the shared `components/bar.go`. The audit
flagged code-vs-spec; the spec moved.

### Closed after this delta was written (1 finding, 2 fixes)

Both landed the same day, in response to this report. The first closes an audit finding;
the second hardens the exec/node-shell carve-out this delta had recorded as decided — so
it moves no count, but it does remove the gap between the code and the README's own
promise.

- **§10a's hardcoded shell list** (`3e9e5cd`). `kube.DetectShells` probes each container
  with `command -v` through a non-interactive `kubectl exec` — a real probe, not an
  inference from the image name — fired from the picker's `Init` so rows paint immediately.
  Four honest states: `checking…`, the detected shells in preference order, `no shell` for
  a distroless container (a real answer, and the one worth having before pressing enter),
  and `–` when the probe couldn't run at all. The detected shell is passed into
  `kube.ExecSpec`, so the row, the will-run line, and the command that actually executes
  all name the same shell. `fake.Cluster.DetectShells` keeps demo mode
  feature-complete, and the goldens now pin two different answers.
- **Exec/node-shell/edit not gated while offline** (`8975c5f`). All three carry
  `Mutating: true` and are refused at every dispatch site (`browse` `x`/`s`/`E`,
  `poddetail` `x`/`E`, `nodedetail` `x`/`s`/`E`, and the picker's own enter, which says so
  in its panel since the connection can drop after the picker was pushed). `Edit` was
  included because `docs/design/README.md:53` names it in the same breath as delete and
  exec: "Delete/exec/edit verbs are disabled while offline."

---

## Incidental drift found while triaging

Both of these were fixed (`a5e6fb8`, `c8dc37c`): `setup/view.go`'s `unreachableBody`
comment claiming the SWITCH CONTEXT list was "names only" when it probes, and
`docs/lazy-informers.md`'s header describing the work as an unmerged branch.

Two others found while fixing the above are still open, tracked in
[`beta-plan.md`](beta-plan.md) §7: `execpicker`'s will-run line ellipsizing before its
trailing shell, and `website/index.html:316`'s claim that drain gets a type-the-name modal
(it confirms with `y/N`).
