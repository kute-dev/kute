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

Output "docs/assets/incident-walkthrough-all-namespaces.gif"

Require kute

Set Shell "bash"
Set FontSize 16
Set FontFamily "JetBrainsMono Nerd Font Mono"
Set Width 1220
Set Height 770
Set Padding 10
Set Framerate 24
Set TypingSpeed 140ms
Set Theme "Catppuccin Macchiato"
Set LineHeight 1.55

Env COLORTERM truecolor

Hide
Type "kute --demo --keycast"
Enter
Sleep 900ms
Type "n"
Sleep 300ms
Type "p"
Sleep 300ms
Enter
Sleep 900ms
Show

Sleep 1200ms

Type "a"
Sleep 1800ms

Enter
Sleep 1500ms

Type "l"
Sleep 2000ms
Escape
Sleep 800ms

Ctrl+D
Sleep 1600ms
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
