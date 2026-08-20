# Homepage checkpoint: an HTTPRoute's weighted canary split and parent join.
Output "docs/assets/home-routing-http-light.png"

Require kute

Set Shell "bash"
Set FontFamily "JetBrainsMono Nerd Font Mono"
Set FontSize 24
Set Width 1632
Set Height 1100
Set Padding 10
Set Theme "3024 Day"
Set LineHeight 1.55

Env COLORTERM truecolor

Type "kute --demo --no-update-check --theme light -n production"
Enter
Sleep 1200ms
Type "g"
Sleep 300ms
Type "httproutes"
Sleep 700ms
Enter
Wait+Screen@8s "web-route"
Down
Enter
Wait+Screen@8s "web-canary"
Sleep 500ms
