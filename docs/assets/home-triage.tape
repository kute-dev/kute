# Homepage checkpoint: all-namespaces triage from the real demo binary.
Output "docs/assets/home-triage.png"

Require kute

Set Shell "bash"
Set FontSize 26
Set FontFamily "JetBrainsMono Nerd Font Mono"
Set LetterSpacing 2
Set Width 2228
Set Height 909
Set Padding 20
Set Theme "3024 Night"
Set LineHeight 1.55

Env COLORTERM truecolor

Type "kute --demo -n production"
Enter
Sleep 1200ms
Type "a"
Wait+Screen@8s "worker-0"
Sleep 500ms
