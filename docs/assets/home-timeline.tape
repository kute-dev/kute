# Homepage checkpoint: namespace incident timeline with its widest time window.
Output "docs/assets/home-timeline.png"

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
Wait+Screen@8s "worker-0"
Type "g"
Sleep 300ms
Type "nam"
Sleep 300ms
Enter
Wait+Screen@8s "Namespaces"
Type "t"
Sleep 700ms
Type "t"
Sleep 500ms
Type "t"
Sleep 500ms
Type "t"
Wait+Screen@8s "TIMELINE"
Sleep 700ms
