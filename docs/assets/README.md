# Demos

## Layout

Source is separated from output, and one tape owns every file named after it:

| Directory | Holds |
| --- | --- |
| `tapes/` | The source. One `.tape` per capture — 25 `home-*` homepage checkpoints, 5 `demo-*` clips |
| `shots/` | `home-<stem>.png` and `home-<stem>-light.png` for each `home-*` tape |
| `demos/` | `demo-<stem>.mp4` and `demo-<stem>.png` for each `demo-*` tape, both written by the same recording |

The **tape stem is the output stem**, with no exceptions: `tapes/home-timeline.tape`
writes exactly `shots/home-timeline.png` and `shots/home-timeline-light.png`;
`tapes/demo-routing-table.tape` writes exactly `demos/demo-routing-table.mp4` and
`demos/demo-routing-table.png`.
`TestRecordingTapesAndAssetsAgree` (`cmd/site/site_test.go`) enforces the pairing in
both directions, so a tape with no committed output and an output with no tape are
both build failures — as is a `Set Width`/`Set Height` that disagrees with the
`width=`/`height=` on that checkpoint's `<img>` tags in `website/pages/index.html`.

The deploy flattens `shots/` and `demos/` into one `assets/` directory
(`.github/workflows/deploy-pages.yml`), which is why a page always references
`/assets/<name>` regardless of which directory the file is in.

**To add a homepage checkpoint:** write `tapes/home-<stem>.tape`, record it, and add
the dark/light `<img>` pair to `website/pages/index.html`. The tape tree is the
inventory — `TestHomeScreenshotsHaveThemePairs` globs it, so forgetting the HTML
fails the build rather than silently shipping a screenshot nobody renders.

## Homepage Explorer

The `home-*.tape` files render the real, static product checkpoints used by
the hero and task menu on kute.dev. The dedicated `home-hero.tape` captures the
larger hero surface; the task menu captures cover all-namespaces
triage, the Goto/Namespace/Context palettes, pod detail, timeline, CronJob and
Job-attempt diagnosis, Helm releases and release history, Ingress and HTTPRoute
routing, Flux, Argo CD, non-PROD/PROD confirmations, certificate failure, and
the kubectl-debug panel (ephemeral attach, copy mode, node debug).
Every checkpoint has a dark and a light capture so the website screenshot
follows its selected theme. The tapes output PNG directly; the incident and
routing recordings remain optional clips shown from those screenshots.

### One tape, both themes

A `home-*` tape is authored as the **dark** capture and stays directly runnable
(`betamax run docs/assets/tapes/home-triage.tape` writes the dark PNG). The light
PNG is derived by `scripts/lib/record.sh` from three substitutions on a temp copy:

| In the tape | Becomes |
| --- | --- |
| `Output ".../shots/<stem>.png"` | `.../shots/<stem>-light.png` |
| `Set Theme "3024 Night"` | `Set Theme "3024 Day"` |
| `--theme dark` | `--theme light` |

Every `home-*` tape must therefore declare that theme and pass that flag, which
the test asserts. This replaced 25 checked-in `-light` twins that differed from
their partner by exactly those three lines — near-duplicates that had already
drifted: `home-prod-confirm` was 800px tall in one theme and 1100 in the other,
and two tapes relied on terminal auto-detection instead of `--theme`.

The preamble is deliberately *not* shared between tapes. betamax 0.1.15 has no
include directive, so a shared fragment would have to be concatenated into a temp
file, and then neither `betamax run` nor `betamax validate` would work on the
checked-in tape. Only about half the preamble is constant anyway — `Set Width`,
`Set Height` and `Set FontSize` are per-checkpoint, and `home-actions-scale.tape`
explains why one of them departs from the gallery default.

### Isolation

The recording scripts isolate both `HOME` and `XDG_STATE_HOME`. This is
load-bearing for `home-prod-confirm.tape`, which writes a temporary config that
marks the demo context as production without reading or changing a user's real
configuration. They expose only `~/.local/share/fonts` on Linux and
`~/Library/Fonts` on macOS inside the temporary home so Betamax can resolve the
font family declared by each tape.
The navigation palette tapes also seed recents and remembered namespaces in
that isolated state directory. The Context tape writes an isolated kubeconfig
and PROD marker so it can show the real lazy-probe state without reading or
probing any context configured on the recording machine.

Both recording entry points drive the same `scripts/lib/record.sh`, so the
isolation cannot differ between a single-tape and a batch run:

```sh
scripts/record-demo.sh docs/assets/tapes/home-timeline.tape   # one tape
scripts/record-all-demos.sh                                   # all 30
```

## Demo clips

Each `demo-*.tape` produces both its files from one recording — the clip from
its `Output`, the still from a `Screenshot` placed at the beat worth freezing:

```
Output "docs/assets/demos/demo-routing-table.mp4"       # what guide.html plays
Screenshot "docs/assets/demos/demo-routing-table.png"   # the chosen frame
```

The PNG does double duty — it is the `poster` on the `<video>`, so it is what a
`prefers-reduced-motion` visitor sees, and it is the still README.md embeds.
README gets a still rather than the clip because **GitHub strips `<video>` and
will not play an mp4 committed to a repo**; only files uploaded through a
comment box render there, and such a URL cannot be regenerated from a tape. The
README stills link to the matching section of the guide, which does play.

### Why the mp4 is encoded by the script, not by betamax

betamax captures one frame per damage event and keeps a real duration next to
each one, but its video writer discards those durations: it dumps the frames as
a numbered PNG sequence and hands ffmpeg `-framerate <Set Framerate>`, giving
every frame an identical 1/24s slot however long it was actually on screen. A
tape's `Sleep`s then contribute *nothing* to the clip. All five demos shipped
this way once — `demo-goto-palette`'s ~15s walkthrough was 58 frames of 2.4s,
every pause gone and every keystroke a blur. Its GIF writer, over the same
frames, honours the durations exactly.

So `scripts/lib/record.sh` asks the recording for what betamax gets right —
full-colour PNG frames, plus a GIF whose only job is to carry the per-frame
delays — by swapping the `.mp4` `Output` line for those two in a temp copy of
the tape, the same substitution trick the dark/light derivation uses. It then
joins the two through ffmpeg's concat demuxer. The frame counts cannot
disagree: both betamax writers iterate the same captured slice.

This is not the pre-betamax `gif-to-mp4.sh` returning. That transcoded the
GIF's *pixels*, publishing video already quantised to 256 colours; here the
pixels are the untouched PNGs and the GIF contributes nothing but its delay
table. The clip is written constant-framerate rather than the variable-framerate
stream concat yields on its own, because it plays in a `<video>` on kute.dev and
seeking a VFR stream with multi-second frames is browser-dependent; the
duplicated frames of a still terminal cost almost nothing through x264.

A checked-in tape stays directly runnable (`betamax run
docs/assets/tapes/demo-routing-table.tape`), but the mp4 that writes has the
collapsed timing — record through the script.

One constraint follows from the encoder: **`Set Width` and `Set Height` must
both be even**, because libx264 rejects an odd dimension. It does not fail
loudly — a direct `betamax run` leaves a zero-byte mp4 — so
`TestRecordingTapesAndAssetsAgree` checks the parity. `demo-goto-palette.tape`
is 1040x616 for exactly this reason and says so in its own header.

## All Namespaces
The recording, using kute --demo, shows an incident from a clean namespace to root cause:

1. All-namespaces reveal (§6b): opens hidden on "production" (clean) — "default" already seeds the
   CrashLoopBackOff pod, and opening there would spoil the reveal — then a switches to the all-namespaces view,
   the actual on-camera "here's what's wrong" beat, surfacing worker-0 crash-looping amid otherwise-healthy
   namespaces.
2. Pod detail & logs (§5a/§5b): ↵ opens the pod's detail screen, l opens its log stream showing the actual crash
   cause, esc backs out to the detail screen.
3. Delete confirm (§8b): ctrl-d opens the inline y/N delete confirm (non-prod tier) — explicit confirmation is
   required, never automatic — then n cancels it rather than actually deleting the pod, and esc backs out to the
   all-namespaces list.
4. Namespace-scoped timeline (§16a/16b): g → nam jumps to the cluster-scoped Namespaces kind so t falls back to
   browse's own namespace-scoped timeline (Namespace is excluded from object-scoped timelines) instead of a
   single pod's; t opens the incident timeline, correlating the crash to a rollout ten minutes earlier.

## Namespace Palette
The recording, using kute --demo, shows:

1. Alt-tab: n → filter to ingress-nginx → switch, then bare n + ↵ twice to toggle back and forth between it and default
   with no typing — the "toggles last" gesture from §6a.
2. Digit recall: after visiting production, monitoring, argocd, logging to populate the RECENT row's numbered entries,
   opens the palette, types 2 (jumps straight to production, footer confirms "↵ switches to production"), then reopens
   and types 1 (jumps to argocd) — showing the digit assignment shifting correctly as current/previous change.

## Goto Palette
The recording, using kute --demo, shows:

1. One-letter alias switch: g → d pins Deployments to rank 1 (aliases never fire instantly — ↵ still confirms),
   jumping from Pods to Deployments within the same "default" namespace.
2. Alt-tab: bare g + ↵ twice to toggle back and forth between Deployments and Pods with no typing — the same
   "toggles last" gesture the namespace palette uses (§6a), applied to kinds (§12a).
3. Individual pod navigation: g → cache jumps straight to the cache-0 pod by name — the fuzzy corpus (§12b) matches
   resource names too, not just kinds, switching kind (Deployments → Pods) as a side effect — then ↵ again opens its
   detail screen.

## Everyday Actions
The recording, using kute --demo, shows a single continuous flow on Deployment "api" (default namespace):

1. Scale (§17b): + opens the inline prompt pre-filled to current+1. "api" is HPA-managed, so the keybar warns that
   the HPA will override the change on its next sync before ↵ applies it.
2. Set image (§24a): i opens the panel's rollout-history dropdown; ↓ steps to the canary's previously-seen tag
   (api:2.2), ↵ applies immediately (non-prod, no confirm).
3. Set resources (§25a): r opens the panel; ↓ selects the cpu limit field, + nudges it up twice (50m per step),
   ↵ applies the one changed field.
4. Labels & annotations (§26a): m opens the panel; a inserts a new label (demo=true), tab moves from key to value,
   ↵ commits instantly (an ordinary edit is reversible, no confirm). Selecting that row and ctrl-d removing it
   escalates to an inline y/N with a real "will run: kubectl label deploy/api demo- -n default" line rendered
   inside the still-open panel — the one beat that shows the command-first preview.
5. Port-forward (§13a): the Deployment's own pod template declares no ports, so ↵ drills into its Pods list first;
   f pushes the picker on the one pod, which discovers 8080/http; ↵ starts the forward and pops back to browse
   immediately — no confirm, forwards aren't tiered. The header's purple "⇄ 1" chip stays visible while browsing
   continues, showing the forward is non-blocking.

## Routing Table
The recording, using kute --demo, shows Ingress and Gateway API resolving their backends live in "production":

1. Ingress (§23a): g → i opens Ingresses, landing on "web-secure" (1 ok · 1 fail). Its routing table shows a
   healthy "/" route (→ web:80, 2 ready) next to a broken "/admin" route (→ web-missing:80, red, no such Service
   is ever seeded) — plus the TLS strip with the certificate's real expiry.
2. HTTPRoute (§23b): g → web-route fuzzy-jumps straight to the HTTPRoute by name (Gateway API kinds have no alias
   letter). Its table shows the 90/10 weighted canary split (web:80 / web-canary:80) and the parent strip
   (gw/public · listener HTTPS:443 · secret/gw-tls expiry).
3. Parent Gateway join: p jumps from the HTTPRoute to its accepted parent Gateway's own listener view — resolving
   Gateway API's split ownership (platform owns the Gateway, app team owns the HTTPRoute) both ways.
