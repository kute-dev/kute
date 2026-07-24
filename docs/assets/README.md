# Demos

## All Namespaces
The recording, using kute --demo, shows an incident from a clean namespace to root cause:

1. All-namespaces reveal (§6b): opens hidden on "production" (clean) — "default" already seeds the
   CrashLoopBackOff pod, and opening there would spoil the reveal — then a switches to the all-namespaces view,
   the actual on-camera "here's what's wrong" beat, surfacing worker-0 crash-looping amid otherwise-healthy
   namespaces.
2. Pod detail & logs (§5a/§5b): ↵ opens the pod's detail screen, l opens its log stream showing the actual crash
   cause, esc backs out to the detail screen.
3. Delete confirm (§8b): ctrl-d opens the inline y/N delete confirm (non-prod tier) — explicit confirmation is
   required, never automatic — then n cancels it rather than actually deleting the pod, and esc backs out to the
   all-namespaces list.
4. Namespace-scoped timeline (§16a/16b): g → nam jumps to the cluster-scoped Namespaces kind so t falls back to
   browse's own namespace-scoped timeline (Namespace is excluded from object-scoped timelines) instead of a
   single pod's; t opens the incident timeline, correlating the crash to a rollout ten minutes earlier.

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

## Everyday Actions
The recording, using kute --demo, shows a single continuous flow on Deployment "api" (default namespace):

1. Scale (§17b): + opens the inline prompt pre-filled to current+1. "api" is HPA-managed, so the keybar shows the
   yellow "managed by hpa/api-hpa — scaling overridden on next sync" warning in place of the usual will-run line —
   kute catches a no-op scale before it's committed, not after — then ↵ applies anyway.
2. Set image (§24a): i opens the panel's rollout-history dropdown; ↓ steps to the canary's previously-seen tag
   (api:2.2), ↵ applies immediately (non-prod, no confirm).
3. Set resources (§25a): R opens the panel; ↓ selects the cpu limit field, + nudges it up twice (50m per step),
   ↵ applies the one changed field.
4. Labels & annotations (§26a): m opens the panel; a inserts a new label (demo=true), tab moves from key to value,
   ↵ commits instantly (an ordinary edit is reversible, no confirm). Selecting that row and ctrl-d removing it
   escalates to an inline y/N with a real "will run: kubectl label deploy/api demo- -n default" line rendered
   inside the still-open panel — the one beat that shows the command-first preview.
5. Port-forward (§13a): the Deployment's own pod template declares no ports, so ↵ drills into its Pods list first;
   f pushes the picker on the one pod, which discovers 8080/http; ↵ starts the forward and pops back to browse
   immediately — no confirm, forwards aren't tiered. The header's purple "⇄ 1" chip stays visible while browsing
   continues, showing the forward is non-blocking.

## Routing Table
The recording, using kute --demo, shows Ingress and Gateway API resolving their backends live in "production":

1. Ingress (§23a): g → i opens Ingresses, landing on "web-secure" (1 ok · 1 fail). Its routing table shows a
   healthy "/" route (→ web:80, 2 ready) next to a broken "/admin" route (→ web-missing:80, red, no such Service
   is ever seeded) — plus the TLS strip with the certificate's real expiry.
2. HTTPRoute (§23b): g → web-route fuzzy-jumps straight to the HTTPRoute by name (Gateway API kinds have no alias
   letter). Its table shows the 90/10 weighted canary split (web:80 / web-canary:80) and the parent strip
   (gw/public · listener HTTPS:443 · secret/gw-tls expiry).
3. Parent Gateway join: p jumps from the HTTPRoute to its accepted parent Gateway's own listener view — resolving
   Gateway API's split ownership (platform owns the Gateway, app team owns the HTTPRoute) both ways.