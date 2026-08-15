# Homepage checkpoint: a failed CronJob promotes the controller's reason above
# its associated scheduled and manual Job history.
Output "docs/assets/home-batch-cronjob.png"

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

Hide
Type "mkdir -p ~/.config/kute && printf 'update: {check: false}\n' > ~/.config/kute/config.yaml"
Enter
Sleep 200ms
Type "kute --demo --theme dark -n default"
Enter
Sleep 1000ms
Type "g"
Sleep 300ms
Type "nightly-cleanup"
Sleep 500ms
Enter
Wait+Screen@8s "LAST RUN"
Enter
Wait+Screen@8s "BackoffLimitExceeded"
Sleep 700ms
Show
