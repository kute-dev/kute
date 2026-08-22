# Homepage checkpoint: §41d's node debug panel — 'x' on a Node replaces the
# old, confirmation-less 's' NodeShell verb with a visible
# `kubectl debug node/...` command.
Output "docs/assets/home-debug-node.png"

Require kute

Set Shell "bash"
Set FontFamily "JetBrainsMono Nerd Font Mono"
Set FontSize 24
Set Width 1632
Set Height 800
Set Padding 10
Set Theme "3024 Night"
Set LineHeight 1.55

Env COLORTERM truecolor

Type "kute --demo --no-update-check --theme dark -n default"
Enter
Wait+Screen@8s "worker-0"
Type "g"
Sleep 300ms
Type "node-a"
Sleep 500ms
Enter
Wait+Screen@8s "node-a"
Type "x"
Wait+Screen@8s "node/node-a"
Sleep 500ms
