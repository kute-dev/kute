# Homepage checkpoint: a non-production delete stays in the table with an
# inline y/N confirmation and the exact command that will run.
Output "docs/assets/home-nonprod-confirm-light.png"

Require kute

Set Shell "bash"
Set FontFamily "JetBrainsMono Nerd Font Mono"
Set FontSize 24
Set Width 1632
Set Height 700
Set Padding 10
Set Theme "3024 Day"
Set LineHeight 1.55

Env COLORTERM truecolor

Hide
Type "kute --demo --no-update-check --theme light -n production"
Enter
Wait+Screen@8s "web-7c9f8d"
Type "D"
Wait+Screen@8s "force delete"
Sleep 500ms
Show
