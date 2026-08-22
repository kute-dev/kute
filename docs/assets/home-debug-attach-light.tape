# Homepage checkpoint: §41b's ephemeral-attach debug panel — 'x' on a
# running, shell-less container skips the exec picker and opens the panel
# directly (browse/debug.go's allShellless fork).
Output "docs/assets/home-debug-attach-light.png"

Require kute

Set Shell "bash"
Set FontFamily "JetBrainsMono Nerd Font Mono"
Set FontSize 24
Set Width 1632
Set Height 900
Set Padding 10
Set Theme "3024 Day"
Set LineHeight 1.55

Env COLORTERM truecolor

Type "kute --demo --no-update-check --theme light -n default"
Enter
Wait+Screen@8s "worker-0"
Type "g"
Sleep 300ms
Type "h2n8k"
Sleep 500ms
Enter
Wait+Screen@8s "gateway-6f9c8d7b4-h2n8k"
Type "x"
Wait+Screen@8s "--target"
Sleep 500ms
