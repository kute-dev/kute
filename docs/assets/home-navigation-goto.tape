# Homepage checkpoint: the shared palette in Goto scope.
Output "docs/assets/home-navigation-goto.png"

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

Type "kute --demo --theme dark"
Enter
Wait+Screen@8s "worker-0"
Type "g"
Wait+Screen@8s "alias"
Sleep 500ms
