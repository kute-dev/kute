# Homepage checkpoint: an Ingress with one missing backend.
Output "docs/assets/home-routing-ingress.png"

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

Type "kute --demo --theme dark -n production"
Enter
Sleep 1200ms
Type "g"
Sleep 400ms
Type "i"
Sleep 500ms
Enter
Sleep 900ms
Enter
Wait+Screen@8s "web-missing"
Sleep 500ms
