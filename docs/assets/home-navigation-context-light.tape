# Homepage checkpoint: the shared palette in Context scope. The recording
# script isolates HOME and XDG state, so this fixture never touches real
# kubeconfig, context recents, or PROD markings.
Output "docs/assets/home-navigation-context-light.png"

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
Type "mkdir -p ~/.kube ~/.config/kute \"$XDG_STATE_HOME/kute\""
Enter
Type "printf '%s' '{\"version\":2,\"recentContexts\":[\"demo\",\"prod-eks\",\"stage-gke\",\"ops-aks\"],\"perContext\":{\"demo\":{\"namespace\":\"default\"},\"prod-eks\":{\"namespace\":\"production\"},\"stage-gke\":{\"namespace\":\"staging\"},\"ops-aks\":{\"namespace\":\"monitoring\"}}}' > \"$XDG_STATE_HOME/kute/state.json\""
Enter
Type "printf 'prodContexts: [prod-eks]\\n' > ~/.config/kute/config.yaml"
Enter
Type "printf 'apiVersion: v1\\nkind: Config\\ncurrent-context: demo\\ncontexts:\\n- name: demo\\n  context: {cluster: demo, namespace: default}\\n- name: prod-eks\\n  context: {cluster: prod, namespace: production}\\n- name: stage-gke\\n  context: {cluster: stage, namespace: staging}\\n- name: ops-aks\\n  context: {cluster: ops, namespace: monitoring}\\nclusters:\\n- name: demo\\n  cluster: {server: http://10.255.255.1}\\n- name: prod\\n  cluster: {server: http://10.255.255.2}\\n- name: stage\\n  cluster: {server: http://10.255.255.3}\\n- name: ops\\n  cluster: {server: http://10.255.255.4}\\nusers: []\\n' > ~/.kube/config"
Enter
Type "export KUBECONFIG=$HOME/.kube/config"
Enter
Type "kute --demo --no-update-check --theme light"
Enter
Wait+Screen@8s "worker-0"
Type "c"
Wait+Screen@8s "PROD"
Sleep 500ms
Show
