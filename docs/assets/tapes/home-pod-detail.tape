# Homepage checkpoint: the failing demo pod's promoted termination detail.
Output "docs/assets/shots/home-pod-detail.png"

Require kute

Set Shell "bash"
Set FontFamily "JetBrainsMono Nerd Font Mono"
Set FontSize 24
Set Width 1632
Set Height 1000
Set Padding 10
Set Theme "3024 Night"
Set LineHeight 1.55

Env COLORTERM truecolor

Type "kute --demo --no-update-check --theme dark"
Enter
Wait+Screen@8s "worker-0"
Enter
Wait+Screen@8s "Last termination"
Sleep 500ms
