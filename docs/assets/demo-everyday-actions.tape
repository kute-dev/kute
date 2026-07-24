# Regenerate with `scripts/record-demo.sh docs/assets/demo-everyday-actions.tape`.
# Bundles kute's everyday Deployment actions into one continuous flow on a
# single object (docs/design README.md §17b scale, §24a set image, §25a set
# resources, §26a labels/annotations, §13a port-forward) rather than five
# thin standalone demos — each of these is a few keystrokes and an inline
# prompt, not enough on its own to carry a whole demo.html section.
#
# --demo always opens on the Pods list in the "default" namespace (app.go's
# demo branch hardcodes Location{Namespace: "default", Kind: KindPod}), and
# "default" seeds exactly one Deployment: "api" (fixtures.go's
# demoMidRolloutDeployment) — image api:2.1, fronted by Service "api", and
# targeted by a HorizontalPodAutoscaler ("api-hpa"). No namespace/context
# switch is needed for any beat below.
#
# The scale beat deliberately keeps "api"'s HPA in place rather than picking
# an unmanaged Deployment: browse/keys.go's scale keybar replaces the usual
# will-run line with a yellow "managed by hpa/api-hpa — scaling overridden
# on next sync" warning, which is itself the beat worth showing — kute
# catches a no-op scale before it's committed, not after.

Output "docs/assets/demo-everyday-actions.gif"

Require kute

Set Shell "bash"
Set FontSize 14
Set Width 1200
Set Height 720
Set Padding 10
Set WindowBar Colorful
Set BorderRadius 8
Set Framerate 24
Set TypingSpeed 140ms

Hide
Type "kute --demo --keycast"
Enter
Sleep 900ms
Show

Sleep 1200ms

# --- 1) goto Deployments: "d" pins Deployments to rank 1 (still needs ↵),
# landing on "api" — the only Deployment in "default", already selected.
Type "g"
Sleep 700ms
Type "d"
Sleep 900ms
Enter
Sleep 1200ms

# --- 2) scale: "+" opens 17b's inline prompt, pre-filled to current+1. The
# HPA warning takes the will-run line's slot instead — let it sit on screen
# before applying.
Type "+"
Sleep 1800ms
Enter
Sleep 1200ms

# --- 3) set image: "i" opens 24a's panel with a rollout-history dropdown,
# starting on the current tag (index 0). ↓ steps to the next history entry
# (the canary's 2.2), ↵ applies immediately (non-prod).
Type "i"
Sleep 900ms
Down
Sleep 900ms
Enter
Sleep 1200ms

# --- 4) set resources: "R" opens 25a's panel. ↓ moves to a field, "+"
# nudges it up twice (64Mi per step), ↵ applies the changed field.
Type "R"
Sleep 900ms
Down
Sleep 500ms
Type "++"
Sleep 700ms
Enter
Sleep 1200ms

# --- 5) labels & annotations: "m" opens 26a's panel. "a" inserts a new
# label row, tab moves from key to value, ↵ commits — an ordinary edit
# applies instantly (TierNone). Then select that row and ctrl-d removes it,
# which escalates to an inline y/N with a real "will run: kubectl label
# ..." line rendered inside the still-open panel — the one beat here that
# shows the command-first preview.
Type "m"
Sleep 900ms
Type "a"
Sleep 500ms
Type "demo"
Tab
Type "true"
Sleep 500ms
Enter
Sleep 1400ms
Ctrl+D
Sleep 1200ms
Type "y"
Sleep 1400ms
Escape
Sleep 800ms

# --- 6) port-forward: the Deployment's own pod template declares no ports
# (only the live Pod does), so drill in first — ↵ opens "api"'s Pods list,
# landing on its one pod. "f" pushes 13a's picker; ↵ starts the forward on
# the discovered 8080/http candidate and pops back to browse immediately —
# no confirm, forwards aren't tiered. Linger afterward so the header's
# purple "⇄ 1" chip is visibly still there while browsing continues (the
# "non-blocking" claim).
Enter
Sleep 1200ms
Type "f"
Sleep 1200ms
Enter
Sleep 2200ms
