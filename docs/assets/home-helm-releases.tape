# Homepage checkpoint: Helm releases as an ordinary kind, with the deployed
# chart beside the newest version known to the local repo cache.
Output "docs/assets/home-helm-releases.png"

Require kute

Set Shell "bash"
Set FontFamily "JetBrainsMono Nerd Font Mono"
Set FontSize 24
Set Width 1632
Set Height 700
Set Padding 10
Set Theme "3024 Night"
Set LineHeight 1.55

Env COLORTERM truecolor

Hide
Type "kute --demo --no-update-check --theme dark -n production"
Enter
Sleep 1000ms
Type "g"
Sleep 300ms
Type "helm releases"
Sleep 500ms
Enter
Wait+Screen@8s "repo cache"
Wait+Screen@8s "postgresql"
Wait+Screen@8s "redis"
Wait+Screen@8s "12.2.1"
Sleep 700ms
Show
