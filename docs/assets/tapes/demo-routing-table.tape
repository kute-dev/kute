# Regenerate with `scripts/record-demo.sh docs/assets/demo-routing-table.tape`.
# Demonstrates 23a/23b's routing table (docs/design README.md): an Ingress
# with a healthy and a broken backend side by side, then an HTTPRoute's
# weighted canary split and its Gateway API parent join (`p` opens the
# accepted parent Gateway). Uses fixtures that already exist in
# internal/kube/fake/fixtures.go — no fixture changes needed.
#
# "production/web-secure" (demoIngressWithTLS) has two rules on host
# secure.prod.demo.local: "/" -> Service "web" (● ready) and "/admin" ->
# Service "web-missing", which is never seeded anywhere (✕, red row,
# endpoints cell renders "—"). "production/web-route" is a 90/10 weighted
# split between "web" and "web-canary", accepted by Gateway
# "production/public".
#
# --demo always hardcodes the "default" namespace on launch, so the
# recording's first on-camera beat is the namespace switch to "production".

Output "docs/assets/demos/demo-routing-table.mp4"

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

Type "kute --demo --no-update-check --keycast -n production"
Enter

# Gate on the first list rather than sleeping at the launch: a bare
# sleep raced the TUI's first paint and swallowed the "g" below.
Wait+Screen@8s "web-7c9f8d"
Sleep 700ms

# --- 2) goto Ingress ("i" pins it to rank 1) — "web-secure" (1 ok, 1 fail)
# is already the top/selected row, so ↵ opens it straight away: its 23a
# routing table shows a healthy "/" route next to a broken "/admin" one.
Type "g"
Sleep 700ms
Type "i"
# Wait for the re-rank, not a fixed sleep. "i" only pins Ingresses to
# rank 1 — an alias never fires on its own — and the enter below used
# to land before that settled, confirming whatever was rank 1 first
# and leaving the Pods list up; the next enter then opened a pod, so
# this beat recorded a pod detail screen instead of a routing table.
Wait+Screen@8s "alias match"
Sleep 700ms
Enter
# Gate on the destination, not a sleep. The "i" alias only pins
# Ingresses to rank 1 — it never fires on its own — so an enter that
# lands before the re-rank confirms whatever was rank 1 before it,
# leaving the Pods list up. The next enter then opened a pod and this
# beat recorded a pod detail screen instead of a routing table.
# "Ingresses" is the breadcrumb; "web-secure" is not usable here,
# because a pod detail lists Ingress/web-secure in its RELATED strip
# and would satisfy the wait on exactly the wrong screen.
Wait+Screen@8s "Ingresses"
Sleep 900ms

Enter
Wait+Screen@8s "HOST + PATH"
Sleep 1800ms

# The still: the Ingress routing table with one healthy route beside one broken backend.
# It is guide.html's video poster and the frame README.md shows,
# so it is chosen here rather than left to be whatever the last
# frame happens to be.
Screenshot "docs/assets/demos/demo-routing-table.png"

# --- 3) back out, then open the HTTPRoutes list and from it "web-route"'s
# 23b routing table: the weighted canary split and the parent-Gateway strip.
#
# This goes through the kind rather than fuzzy-jumping to "web-route" by
# name, which is what it used to do. It cannot: informers start lazily, and
# the goto palette's corpus only contains kinds something has already read
# (docs/lazy-informers.md). Nothing in this walkthrough opens HTTPRoutes
# before this point, so the palette answered "no matches", the enter fell
# through to the Pods list underneath, and the recording ended on a pod's
# detail screen with the "p" below doing nothing at all.
Escape
Sleep 800ms

Type "g"
Sleep 700ms
Type "httproutes"
Sleep 900ms
Enter
Wait+Screen@8s "web-route"
Sleep 900ms
Down
Sleep 500ms
Enter
Wait+Screen@8s "web-canary"
Sleep 2000ms

# --- 4) "p" jumps to the accepted parent Gateway ("production/public").
# It lands on the Gateways list with that row selected, so ↵ opens the
# listener view itself — resolving Gateway API's split ownership (platform
# owns the Gateway, app team owns the HTTPRoute) both ways.
Type "p"
Sleep 1200ms
Enter
Sleep 2400ms
