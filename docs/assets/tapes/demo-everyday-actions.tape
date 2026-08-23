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
# on next sync" warning before the user applies.

Output "docs/assets/demos/demo-everyday-actions.mp4"

Require kute

Set Shell "bash"
Set FontSize 16
Set FontFamily "JetBrainsMono Nerd Font Mono"
Set Width 1040
Set Height 616
Set Padding 10
Set Framerate 24
Set Theme "3024 Night"
Set LineHeight 1.55

Env COLORTERM truecolor

Type "kute --demo --no-update-check --keycast"
Enter

# Every beat below gates on the screen it needs rather than sleeping at it.
# Bare sleeps raced the app: the set-image history arrives asynchronously, so
# the ↓ below hit an empty list and no-oped, ↵ then no-oped too (apply is
# suppressed when nothing changed), the panel stayed open, and every later
# keystroke was typed into the image field — "api:2.1++mademotrue".
Wait+Screen@8s "api"

# --- 1) goto Deployments: "d" pins Deployments to rank 1 (still needs ↵),
# landing on "api" — the only Deployment in "default", already selected.
Type "g"
Sleep 700ms
Type "d"
Sleep 900ms
Enter
Wait+Screen@8s "ROLLOUT"
Sleep 900ms

# --- 2) scale: "+" opens 17b's inline prompt, pre-filled to current+1. The
# HPA warning takes the will-run line's slot instead — let it sit on screen
# before applying.
Type "+"
Wait+Screen@8s "SCALE"
Sleep 1400ms
Enter
Sleep 1200ms

# --- 3) set image: "i" opens 24a's panel with a rollout-history dropdown,
# starting on the current tag (index 0). ↓ steps to the next history entry
# (the canary's 2.2), ↵ applies immediately (non-prod).
Type "i"
Wait+Screen@8s "2.2"
Sleep 700ms
Down
Sleep 900ms
Enter
# The apply lands but 24a's panel stays open on commit, unlike 25a's.
# Without this esc the next beat's "r" is typed into the tag field
# ("api:2.2r") and every beat after it is swallowed by the same buffer.
Sleep 1600ms
Escape
Sleep 800ms

# --- 4) set resources: "r" opens 25a's panel. ↓ moves to a field, "+"
# nudges it up twice (64Mi per step), ↵ applies the changed field.
Type "r"
Wait+Screen@8s "usage: from the metrics poll"
Sleep 700ms
Down
Sleep 500ms
Type "++"
Sleep 700ms
Enter
Sleep 1400ms

# --- 5) labels & annotations: "m" opens 26a's panel. "a" inserts a new
# label row, tab moves from key to value, ↵ commits — an ordinary edit
# applies instantly (TierNone). Then select that row and "D" removes it,
# which escalates to an inline y/N with a real "will run: kubectl label
# ..." line rendered inside the still-open panel — the one beat here that
# shows the command-first preview.
Type "m"
Wait+Screen@8s "LABELS"
Sleep 700ms
Type "a"
Sleep 500ms
Type "demo"
Tab
Type "true"
Sleep 500ms
Enter
Sleep 1400ms
# Removal is "D" inside the panel, not ctrl-d: the keybar renders
# "D remove key · y/N". A ctrl-d here is simply ignored, and the still
# below then captured "no changes — has nothing to apply" instead of
# the will-run line this beat exists to show.
Type "D"
Wait+Screen@8s "kubectl label"
Sleep 900ms

# The still: the command-first beat: the will-run line inside the still-open labels panel.
# It is guide.html's video poster and the frame README.md shows,
# so it is chosen here rather than left to be whatever the last
# frame happens to be.
Screenshot "docs/assets/demos/demo-everyday-actions.png"
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
Wait+Screen@8s "ports"
Sleep 900ms
Enter
Wait+Screen@8s "⇄ 1"
Sleep 2000ms
