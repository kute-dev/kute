# Homepage checkpoint: all-namespaces triage from the real demo binary.
Output "docs/assets/home-triage-light.png"

Require kute

Set Shell "bash"
Set FontSize 24
Set FontFamily "JetBrainsMono Nerd Font Mono"
Set Width 1632
Set Height 1100
Set Padding 10
Set Theme "3024 Day"
Set LineHeight 1.55

Env COLORTERM truecolor

Type "kute --demo --no-update-check --theme light -n production"
Enter
Sleep 1200ms
Type "a"
Wait+Screen@8s "worker-0"
Sleep 500ms
