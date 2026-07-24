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

Output "docs/assets/namespace-palette-demo.gif"

Require kute

Set Shell "bash"
Set FontSize 14
Set Width 1200
Set Height 720
Set Padding 10
Set WindowBar Colorful
Set BorderRadius 8
Set Framerate 24
Set TypingSpeed 140ms

Hide
Type "kute --demo --keycast"
Enter
Sleep 900ms
Show

Sleep 1200ms

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
