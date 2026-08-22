# Homepage checkpoint: §41c's copy-mode debug panel — 'x' on a pod that
# won't stay running (CrashLoopBackOff) skips straight to copy mode, since
# neither exec nor an ephemeral attach is possible either.
Output "docs/assets/home-debug-copy-light.png"

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
Type "canary"
Sleep 500ms
Enter
Wait+Screen@8s "gateway-canary"
Type "x"
Wait+Screen@8s "--copy-to"
Sleep 500ms
