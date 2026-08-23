# Regenerate with `scripts/record-demo.sh docs/assets/demo-namespace-palette.tape`.
# Demonstrates the 6a namespace palette's two recall gestures (docs/design
# README.md §6a): the bare `n <enter>` alt-tab toggle back to whichever
# namespace was active immediately before, and `n` + digit + `<enter>` to
# jump straight to an older RECENT-row namespace once there's more than
# one other namespace in history.
#
# --demo always hardcodes the "default" namespace on launch (app.go's demo
# branch never reads persisted PerContext). pushRecentNamespace seeds both
# the destination and the namespace being left on every switch, so the
# very first hop below (to "ingress-nginx") already puts "default" in
# RecentNamespaces as the alt-tab target — no separate warm-up hop needed
# before the showcase. "staging" is deliberately skipped throughout —
# fixtures.go seeds it with zero pods for the 10c empty-namespace preview,
# which makes for a flat-looking recall demo.

Output "docs/assets/demos/demo-namespace-palette.mp4"

Require kute

Set Shell "bash"
Set FontSize 16
Set FontFamily "JetBrainsMono Nerd Font Mono"
Set Width 1040
Set Height 616
Set Padding 10
Set Framerate 24
Set Theme "3024 Night"
Set LineHeight 1.55

Env COLORTERM truecolor

Type "kute --demo --no-update-check --keycast"
Enter

# Gate on the first list rather than sleeping at the launch: a bare
# sleep races the TUI's first paint, and a swallowed opening
# keystroke sends the whole walkthrough somewhere else silently.
Wait+Screen@8s "worker-0"
Sleep 700ms

# First hop away from the launch namespace ("default"), so there's a
# "previous" to alt-tab back to below.
Type "n"
Sleep 500ms
Type "ingr"
Sleep 400ms
Enter
Sleep 1200ms

# --- 1) alt-tab between namespaces: bare "n <enter>" toggles straight
# back to whichever namespace was active immediately before, no typing.
Type "n"
Sleep 700ms
Enter
Sleep 1200ms

# Build up a few more distinct namespaces so RECENT grows numbered
# entries beyond current/previous (each already has its own row tag).
Type "n"
Sleep 400ms
Type "prod"
Sleep 300ms
Enter
Sleep 900ms

Type "n"
Sleep 400ms
Type "mon"
Sleep 300ms
Enter
Sleep 900ms

Type "n"
Sleep 400ms
Type "argo"
Sleep 300ms
Enter
Sleep 900ms

Type "n"
Sleep 400ms
Type "log"
Sleep 300ms
Enter
Sleep 900ms

# --- 2) numbered-recent pick: open the palette, let the numbered RECENT
# rows sit on screen, then type a bare digit to jump Sel to that entry
# ("production") and confirm with enter.
Type "n"
Sleep 1500ms

# The still: the palette open on the RECENT row, each remembered namespace carrying its digit.
# It is guide.html's video poster and the frame README.md shows,
# so it is chosen here rather than left to be whatever the last
# frame happens to be.
Screenshot "docs/assets/demos/demo-namespace-palette.png"
Type "2"
Sleep 900ms
Enter
Sleep 1200ms

# Same gesture again, a different digit ("argocd") now that the numbering
# has shifted with the new current/previous pair.
Type "n"
Sleep 1500ms
Type "1"
Sleep 900ms
Enter
Sleep 1600ms
