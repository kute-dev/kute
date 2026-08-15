# Homepage checkpoint: a failed Job's retained pod attempts, exit codes and
# durations share one ledger beneath the authoritative Job condition.
Output "docs/assets/home-batch-attempts.png"

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
Type "kute --demo --theme dark -n default"
Enter
Sleep 1000ms
Type "g"
Sleep 300ms
Type "nightly-cleanup-29070330"
Sleep 500ms
Enter
Wait+Screen@8s "COMPL"
Enter
Wait+Screen@8s "same failure all attempts"
Sleep 700ms
Show
