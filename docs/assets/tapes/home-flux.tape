# Homepage checkpoint: Flux's source-to-reconciler tree.
Output "docs/assets/shots/home-flux.png"

Require kute

Set Shell "bash"
Set FontFamily "JetBrainsMono Nerd Font Mono"
Set FontSize 24
Set Width 1632
Set Height 1100
Set Padding 10
Set Theme "3024 Night"
Set LineHeight 1.55

Env COLORTERM truecolor

Type "kute --demo --no-update-check --theme dark"
Enter
Sleep 1000ms
Type "g"
Sleep 300ms
Type "flux"
Sleep 500ms
Enter
Wait+Screen@8s "nebula-workers"
Sleep 700ms
