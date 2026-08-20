# Homepage checkpoint: setting an image offers the tags from the
# workload's own rollout history first (§24a).
Output "docs/assets/home-actions-setimage.png"

Require kute

Set Shell "bash"
Set FontFamily "JetBrainsMono Nerd Font Mono"
Set FontSize 24
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
Type "i"
Wait+Screen@8s "pick from history"
Down
Sleep 500ms
Show
