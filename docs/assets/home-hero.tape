# Homepage hero: all-namespaces triage from the real demo binary.
Output "docs/assets/home-hero.png"

Require kute

Set Shell "bash"
Set FontSize 24
Set FontFamily "JetBrainsMono Nerd Font Mono"
Set Width 2228
Set Height 700
Set Padding 20
Set Theme "3024 Night"
Set LineHeight 1.55

Env COLORTERM truecolor

Type "kute --demo --no-update-check -n production"
Enter
Sleep 1200ms
Type "a"
Wait+Screen@8s "worker-0"
Sleep 500ms
