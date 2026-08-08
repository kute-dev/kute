# Homepage checkpoint: cert-manager's issuance chain at its deepest failure.
Output "docs/assets/home-certificate-failure-light.png"

Require kute

Set Shell "bash"
Set FontFamily "JetBrainsMono Nerd Font Mono"
Set FontSize 12
Set Width 816
Set Height 550
Set Padding 10
Set Theme "3024 Day"
Set LineHeight 1.55

Env COLORTERM truecolor

Type "kute --demo --theme light"
Enter
Sleep 1000ms
Type "g"
Sleep 300ms
Type "certificates"
Sleep 500ms
Enter
Wait+Screen@8s "web-tls"
Enter
Wait+Screen@8s "NXDOMAIN"
Sleep 700ms
