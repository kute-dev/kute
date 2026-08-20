# Homepage checkpoint: scaling an HPA-managed Deployment shows the
# managed-by-hpa warning instead of a plain command preview (§17b).
#
# FontSize is 22 here, not the gallery's usual 24: at 24 the keybar's left
# group ("SCALE 4| replicas +/- nudge ↵ apply esc cancel") plus the HPA
# warning text together are 4 columns too wide for a 1632px terminal, and
# insetChromeLine (internal/tui/chrome.go) drops the whole right side
# instead of truncating it — so the warning this screenshot exists to show
# never renders and the Wait+Screen below times out. Width/Height stay at
# the gallery's 1632x800 (matching website/pages/index.html's declared img
# dimensions); only the font shrinks enough to fit both sides.
Output "docs/assets/home-actions-scale.png"

Require kute

Set Shell "bash"
Set FontFamily "JetBrainsMono Nerd Font Mono"
Set FontSize 22
Set Width 1632
Set Height 600
Set Padding 10
Set Theme "3024 Night"
Set LineHeight 1.55

Env COLORTERM truecolor

Hide
Type "kute --demo --no-update-check --theme dark -n default"
Enter
Sleep 1000ms
Type "g"
Sleep 300ms
Type "d"
Sleep 500ms
Enter
Wait+Screen@8s "ROLLOUT"
Sleep 1200ms
Type "+"
Wait+Screen@8s "managed by hpa/api-hpa"
Sleep 500ms
Show
