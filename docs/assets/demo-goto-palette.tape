# Regenerate with `scripts/record-demo.sh docs/assets/demo-goto-palette.tape`.
# Demonstrates the 12a/12b goto palette (docs/design README.md §12a "Goto
# palette on open (alias letters)" and the fuzzy-corpus behavior under
# "Interactions & Behavior"): a single-letter alias jump that pins a kind
# to rank 1 (aliases never fire instantly — a second keystroke or ↵ is
# still required), the same bare `g <enter>` alt-tab toggle the namespace/
# context palettes use (internal/tui/goto.go's gotoBrowseSelection), this
# time flipping between kinds, and a fuzzy resource-name jump that lands
# directly on one specific pod, switching kind as a side effect.
#
# --demo always opens on the Pods list in the "default" namespace
# (app.go's demo branch hardcodes Location{Namespace: "default", Kind:
# KindPod}). "default" already seeds a Deployment named "api" and a
# StatefulSet named "worker" alongside its pods (fixtures.go), so both the
# alias jump and the alt-tab flips stay inside "default" — no namespace
# switch needed for either gesture.
#
# The pod-name jump targets "cache-0" (a Pending pod) specifically because
# "cache" is the only fixture name containing that substring in "default":
# "worker" also matches the StatefulSet and its ControllerRevisions, and
# "api" also matches the Deployment — either would land on an ambiguous
# top match instead of the one pod.
#
# FontSize/Width/Height are a matched set, not free knobs. Betamax draws
# U+2500 at the font's own advance rather than stretching it to the cell, so
# at most sizes every horizontal rule in kute's chrome comes out dashed —
# one dark pixel per cell boundary. At 11px the cell was 8px wide against
# ~6.8px of ink, which is what made the header, strip and palette rules read
# as "_ _ _" instead of solid lines. 16px is the smallest size where the two
# line up (measured across 8-24px; 21px and 24px are the only other clean
# ones, and both blow up the GIF). 1040x615 at 16px reproduces exactly the
# 102x23 grid 840x434 gave at 11px, so the framing is unchanged.

Output "docs/assets/goto-palette-demo.gif"

Require kute

Set Shell "bash"
Set FontSize 16
Set FontFamily "JetBrainsMono Nerd Font Mono"
Set Width 1040
Set Height 615
Set Padding 10
Set Framerate 24
Set Theme "3024 Night"
Set LineHeight 1.55

Env COLORTERM truecolor

Type "kute --demo --no-update-check --keycast"
Enter

Sleep 1200ms

# --- 1) one-letter alias switch: "d" pins Deployments to rank 1 — still
# requires ↵ to actually confirm the jump (Pods -> Deployments, same
# "default" namespace).
Type "g"
Sleep 700ms
Type "d"
Sleep 900ms
Enter
Sleep 1200ms

# --- 2) alt-tab between kinds: bare "g <enter>" toggles straight back to
# whichever kind was active immediately before. Flip twice to show it's a
# real toggle, not a one-way back button.
Type "g"
Sleep 700ms
Enter
Sleep 1200ms

Type "g"
Sleep 700ms
Enter
Sleep 1200ms

# --- 3) individual pod navigation: the fuzzy corpus searches resource
# names too, not just kinds — typing a specific pod's name jumps straight
# to it, switching kind (Deployments -> Pods) as a side effect and landing
# already selected on that one row.
Type "g"
Sleep 700ms
Type "cache"
Sleep 900ms
Enter
Sleep 1200ms

# Open it — the jump lands selected on the row, not inside the object yet.
Enter
Sleep 1600ms
