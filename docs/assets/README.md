# Demos

## All Namespaces


## Namespace Palette
The recording, using kute --demo, shows:

1. Alt-tab: n → filter to ingress-nginx → switch, then bare n + ↵ twice to toggle back and forth between it and default
   with no typing — the "toggles last" gesture from §6a.
2. Digit recall: after visiting production, monitoring, argocd, logging to populate the RECENT row's numbered entries,
   opens the palette, types 2 (jumps straight to production, footer confirms "↵ switches to production"), then reopens
   and types 1 (jumps to argocd) — showing the digit assignment shifting correctly as current/previous change.

## Goto Palette
The recording, using kute --demo, shows:

1. One-letter alias switch: g → d pins Deployments to rank 1 (aliases never fire instantly — ↵ still confirms),
   jumping from Pods to Deployments within the same "default" namespace.
2. Alt-tab: bare g + ↵ twice to toggle back and forth between Deployments and Pods with no typing — the same
   "toggles last" gesture the namespace palette uses (§6a), applied to kinds (§12a).
3. Individual pod navigation: g → cache jumps straight to the cache-0 pod by name — the fuzzy corpus (§12b) matches
   resource names too, not just kinds, switching kind (Deployments → Pods) as a side effect — then ↵ again opens its
   detail screen.