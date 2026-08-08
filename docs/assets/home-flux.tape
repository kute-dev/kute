# Homepage checkpoint: Flux's source-to-reconciler tree.
Output "docs/assets/home-flux.png"

Require kute

Set Shell "bash"
Set FontFamily "JetBrainsMono Nerd Font Mono"
Set FontSize 12
Set Width 816
Set Height 550
Set Padding 10
Set Theme "3024 Night"
Set LineHeight 1.55

Env COLORTERM truecolor

Type "kute --demo --theme dark"
Enter
Sleep 1000ms
Type "g"
Sleep 300ms
Type "flux"
Sleep 500ms
Enter
Wait+Screen@8s "nebula-workers"
Sleep 700ms
