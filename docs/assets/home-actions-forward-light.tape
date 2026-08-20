# Homepage checkpoint: a port-forward starts without suspending the
# program — the header keeps a live chip while browsing continues (§13a/§13d).
Output "docs/assets/home-actions-forward-light.png"

Require kute

Set Shell "bash"
Set FontFamily "JetBrainsMono Nerd Font Mono"
Set FontSize 24
Set Width 1632
Set Height 800
Set Padding 10
Set Theme "3024 Day"
Set LineHeight 1.55

Env COLORTERM truecolor

Hide
Type "kute --demo --no-update-check --theme light -n default"
Enter
Sleep 1000ms
Type "g"
Sleep 300ms
Type "d"
Sleep 500ms
Enter
Wait+Screen@8s "ROLLOUT"
Sleep 1200ms
Enter
Wait+Screen@8s "NODE"
Sleep 800ms
Type "f"
Wait+Screen@8s "ports"
Enter
Wait+Screen@8s "⇄ 1"
Type "g"
Sleep 300ms
Type "fo"
Sleep 500ms
Enter
Wait+Screen@8s "LOCAL"
Sleep 800ms
Show
