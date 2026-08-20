# Homepage checkpoint: Helm release history read directly from release Secrets,
# with rollback as the only action that requires the Helm CLI.
Output "docs/assets/home-helm-history.png"

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
Type "h"
Wait+Screen@8s "revisions"
Sleep 700ms
Show
