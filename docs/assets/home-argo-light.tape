# Homepage checkpoint: Argo CD Applications keep sync and health independent.
Output "docs/assets/home-argo-light.png"

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
Type "mkdir -p ~/.config/kute && printf 'update: {check: false}\n' > ~/.config/kute/config.yaml"
Enter
Sleep 200ms
Type "kute --demo --theme light -n argocd"
Enter
Sleep 1000ms
Type "g"
Sleep 300ms
Type "applications"
Sleep 500ms
Enter
Wait+Screen@8s "OutOfSync"
Sleep 700ms
Show
