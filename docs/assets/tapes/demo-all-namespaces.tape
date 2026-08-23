# Regenerate with `scripts/record-demo.sh docs/assets/demo-all-namespaces.tape`.
# Same incident story as demo.tape, but opens on browse's all-namespaces view
# (`a`, the resting screen — docs/design README.md §6b) instead of the 19a
# Cluster Overview screen. To reach the same rollout-correlated timeline
# without visiting Overview, it switches to the Namespaces kind (cluster-
# scoped, so `t` there falls back to browse's own namespace-scoped timeline
# — internal/tui/tasks/browse/update.go's isObjectTimelineKind excludes
# Namespace, and the active namespace is still the "" all-namespaces
# sentinel from `a`) rather than a Pod's object-scoped one.
#
# --demo always hardcodes the "default" namespace on launch (app.go's demo
# branch never reads persisted PerContext), and "default" happens to already
# contain the CrashLoopBackOff pod — opening there would spoil the reveal.
# So the recording opens hidden on "production" (clean) via the namespace
# palette, then `a` is the actual on-camera "here's what's wrong" beat.

Output "docs/assets/demos/demo-all-namespaces.mp4"

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

Type "kute --demo --no-update-check --keycast -n production"
Enter

# Gate on the list actually being on screen rather than sleeping at it. A bare
# Sleep raced the TUI's first paint: the "a" below landed before anything was
# listening for it, the all-namespaces reveal never happened, and the rest of
# the walkthrough played out against production's healthy pods with nothing
# saying so.
Wait+Screen@8s "web-7c9f8d"

Type "a"
Wait+Screen@8s "worker-0"
Sleep 1200ms

# The still: the all-namespaces reveal: worker-0 crash-looping among otherwise-healthy namespaces.
# It is guide.html's video poster and the frame README.md shows,
# so it is chosen here rather than left to be whatever the last
# frame happens to be.
Screenshot "docs/assets/demos/demo-all-namespaces.png"

Enter
Sleep 1500ms

Type "l"
Sleep 2000ms
Escape
Sleep 800ms

# Delete is "D", not ctrl-d. With ctrl-d the confirm never opened and the
# "n" below fell through to the namespace palette, so this beat showed a
# palette flash where the walkthrough claims an explicit confirmation.
# The wait doubles as the assertion: no confirm on screen, no recording.
Type "D"
Wait+Screen@8s "force delete"
Sleep 1400ms
Type "n"
Sleep 900ms

Escape
Sleep 800ms

Type "g"
Sleep 300ms
Type "nam"
Sleep 300ms
Enter
Sleep 1200ms

Type "t"
Sleep 900ms
Type "t"
Sleep 900ms
Type "t"
Sleep 900ms
Type "t"
Sleep 2600ms
