# Homepage checkpoint: a non-production delete stays in the table with an
# inline y/N confirmation and the exact command that will run.
Output "docs/assets/home-nonprod-confirm.png"

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
Type "mkdir -p ~/.config/kute && printf 'update: {check: false}\n' > ~/.config/kute/config.yaml"
Enter
Sleep 200ms
Type "kute --demo --theme dark -n production"
Enter
Wait+Screen@8s "web-7c9f8d"
Type "D"
Wait+Screen@8s "force delete"
Sleep 500ms
Show
