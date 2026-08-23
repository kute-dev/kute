# Homepage checkpoint: requests and limits edit next to the live usage
# bar for that exact container (§25a).
Output "docs/assets/shots/home-actions-resources.png"

Require kute

Set Shell "bash"
Set FontFamily "JetBrainsMono Nerd Font Mono"
Set FontSize 22
Set Width 1632
Set Height 800
Set Padding 10
Set Theme "3024 Night"
Set LineHeight 1.55

Env COLORTERM truecolor

Hide
Type "kute --demo --no-update-check --theme dark -n default"
Enter
Sleep 1000ms
Type "g"
Sleep 300ms
Type "d"
Sleep 500ms
Enter
Wait+Screen@8s "ROLLOUT"
Sleep 1200ms
Type "r"
Wait+Screen@8s "usage: from the metrics poll"
Sleep 400ms
Type "++"
Wait+Screen@8s "kubectl set resources"
Sleep 600ms
Show
