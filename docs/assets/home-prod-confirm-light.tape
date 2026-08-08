# Homepage checkpoint: a real type-the-name confirmation. record-demo.sh gives
# this tape an isolated HOME, so the config written here never touches a user's.
Output "docs/assets/home-prod-confirm-light.png"

Require kute

Set Shell "bash"
Set FontFamily "JetBrainsMono Nerd Font Mono"
Set FontSize 12
Set Width 816
Set Height 550
Set Padding 10
Set Theme "3024 Day"
Set LineHeight 1.55

Env COLORTERM truecolor

Hide
Type "mkdir -p ~/.config/kute && printf 'prodContexts: [demo]\\nupdate: {check: false}\\n' > ~/.config/kute/config.yaml"
Enter
Sleep 200ms
Type "kute --demo --theme light -n production"
Enter
Wait+Screen@8s "web-7c9f8d"
Ctrl+D
Wait+Screen@8s "PROD CONTEXT"
Type "web-7c9f8d-"
Sleep 500ms
Show
