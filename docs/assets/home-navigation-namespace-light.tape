# Homepage checkpoint: the shared palette in Namespace scope, seeded with
# deterministic recents so the previous and numbered-recent affordances show.
Output "docs/assets/home-navigation-namespace-light.png"

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

Hide
Type "mkdir -p \"$XDG_STATE_HOME/kute\" && printf '%s' '{\"version\":2,\"perContext\":{\"demo\":{\"namespace\":\"default\",\"kind\":\"Pod\",\"recentNamespaces\":[\"default\",\"production\",\"monitoring\",\"argocd\"]}}}' > \"$XDG_STATE_HOME/kute/state.json\""
Enter
Type "kute --demo --no-update-check --theme light"
Enter
Wait+Screen@8s "worker-0"
Type "n"
Wait+Screen@8s "all namespaces"
Sleep 700ms
Show
