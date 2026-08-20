# Homepage checkpoint: a real type-the-name confirmation. record-demo.sh gives
# this tape an isolated HOME, so the config written here never touches a user's.
Output "docs/assets/home-prod-confirm.png"

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
Type "mkdir -p ~/.config/kute && printf 'prodContexts: [demo]\\n' > ~/.config/kute/config.yaml"
Enter
Sleep 200ms
Type "kute --demo --no-update-check --theme dark -n production"
Enter
Wait+Screen@8s "web-7c9f8d"
Type "D"
Wait+Screen@8s "PROD CONTEXT"
Type "web-7c9f8d-"
Sleep 500ms
Show
