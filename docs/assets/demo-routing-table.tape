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

Output "docs/assets/demo-routing-table.gif"

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

# --- 1) switch to "production", the namespace holding both fixtures below.
Type "n"
Sleep 500ms
Type "prod"
Sleep 400ms
Enter
Sleep 1200ms

# --- 2) goto Ingress ("i" pins it to rank 1) — "web-secure" (1 ok, 1 fail)
# is already the top/selected row, so ↵ opens it straight away: its 23a
# routing table shows a healthy "/" route next to a broken "/admin" one.
Type "g"
Sleep 700ms
Type "i"
Sleep 900ms
Enter
Sleep 1200ms

Enter
Sleep 2200ms

# --- 3) back out, then jump straight to the "web-route" HTTPRoute by name
# (Gateway API kinds have no alias letter — the fuzzy corpus matches
# resource names) and open its 23b routing table: the weighted canary
# split and the parent-Gateway strip.
Escape
Sleep 800ms

Type "g"
Sleep 700ms
Type "web-route"
Sleep 900ms
Enter
Sleep 1000ms
Enter
Sleep 2200ms

# --- 4) "p" jumps to the accepted parent Gateway ("production/public"),
# landing on its own listener view — resolving Gateway API's split
# ownership (platform owns the Gateway, app team owns the HTTPRoute) both
# ways.
Type "p"
Sleep 2000ms
