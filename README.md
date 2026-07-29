# kute

**Modern Kubernetes TUI**

[kute.dev](https://kute.dev)

### The incident console for Kubernetes.

See what broke. Understand why. Act safely.

kute is a keyboard-driven Kubernetes console built with [Bubble Tea](https://github.com/charmbracelet/bubbletea) and [Lip Gloss](https://github.com/charmbracelet/lipgloss), designed around the first 15 minutes of an incident rather than plain object browsing.

![kute: an incident walkthrough — a clean production namespace, then all-namespaces reveals a CrashLoopBackOff pod, pod detail and logs show the actual cause, delete asks for explicit confirmation, and the timeline correlates the crash to a rollout 10 minutes earlier](docs/assets/incident-walkthrough-all-namespaces.gif)

*Recorded against `kute --demo`'s built-in fake cluster — regenerate with `scripts/record-demo.sh docs/assets/demo-all-namespaces.tape`.*

## Try it

```sh
go run ./cmd/kute --demo
```

No cluster or kubeconfig required — `--demo` runs against a built-in in-memory fake cluster so you can explore every screen immediately.

## What makes it different

- **Unhealthy-first triage** — every list sorts broken workloads to the top; fully healthy groups collapse to a single line instead of burying the incident in green rows.
- **Restart-aware logs and an incident timeline** — pod logs draw a boundary at every container restart; the timeline screen merges events, restarts, and rollout revisions into one newest-first feed answering "what changed recently."
- **Termination causes and conditions shown verbatim** — the actual condition message is the diagnosis, never paraphrased or summarized away.
- **Every mutating action shows its command first** — exec, port-forward, scale, image/resource changes, label edits, rollout restarts, and Helm rollbacks all print the exact command that's about to run before it runs. Copyable documentation, not a black box.
- **Deliberate friction on destructive actions** — reversible verbs like cordon execute immediately; delete and rollout-restart are tiered, with inline y/N confirmation normally and a type-the-name modal when the context is explicitly listed as production in your [config file](#configuration), never guessed from a name. Drain always confirms, and force-delete needs the typed name in a prod context.
- **CRDs work without a configuration project** — kinds discovered from the API automatically get columns, status, and detail views. No plugins, no per-CRD setup.
- **Alt-tab namespace/context switching** — the same palette that jumps to any resource kind also toggles between your last two namespaces or contexts with no typing, and recalls recent ones by number.
- **One palette jumps to anything** — a kind's alias letter, a fuzzy kind name, or a specific resource by name all live in the same `g` palette; typing a pod's name jumps straight to it, switching kind and namespace as a side effect.

<details>
<summary>See it: namespace palette alt-tab + digit recall</summary>

![kute: the namespace palette alt-tabbing to ingress-nginx and back to default with no typing, then recalling production and argocd from the RECENT row by their assigned digit](docs/assets/namespace-palette-demo.gif)

*Recorded against `kute --demo` — regenerate with `scripts/record-demo.sh docs/assets/demo-namespace-palette.tape`.*

</details>

<details>
<summary>See it: goto palette alias switch, alt-tab, and jump-to-pod</summary>

![kute: the goto palette pinning Deployments to rank 1 with the "d" alias, alt-tabbing back and forth between Pods and Deployments with no typing, then typing "cache" to jump straight to the cache-0 pod and open its detail](docs/assets/goto-palette-demo.gif)

*Recorded against `kute --demo` — regenerate with `scripts/record-demo.sh docs/assets/demo-goto-palette.tape`.*

</details>

## Resilience & safety

When the API server connection drops, kute keeps showing your last known state (desaturated, with an age stamp) instead of blanking the screen — and disables every mutating action until the connection is back, including the ones that hand off to `kubectl`: exec, node shell, and edit.

## Install

```sh
curl -fsSL https://kute.dev/install.sh | sh
```

Or with Homebrew:

```sh
brew install kute-dev/tap/kute
```

## Usage

Run `kute` with no arguments and it opens on the context you last used, in the namespace you left it in. The flags below pin a starting point instead — useful in a script, an alias, or when reproducing something for a bug report.

| Flag | What it does |
| --- | --- |
| `--context <name>` | Launch against a specific kubeconfig context, instead of the last-used one (or the kubeconfig's `current-context` on a first run). |
| `-n`, `--namespace <name>` | Launch in a specific namespace. Outranks both the remembered namespace and the context's own default. |
| `--kubeconfig <path>` | Read a specific kubeconfig file. Takes precedence over `$KUBECONFIG`, which takes precedence over `~/.kube/config`. |
| `--theme dark\|light` | Force a theme instead of detecting it from the terminal background. |
| `--demo` | Run against a built-in in-memory fake cluster. No kubeconfig or cluster needed. |
| `--version` | Print version, commit and build date, then exit. |

Everything else is a keystroke: `?` from any screen lists the keys for that screen plus the global ones.

### Authentication

kute authenticates exactly the way `kubectl` does, from the same kubeconfig: client certificates, bearer tokens, OIDC (Rancher, Dex, Keycloak), and exec credential plugins — `gke-gcloud-auth-plugin` for GKE, `aws eks get-token` for EKS, `kubelogin` for AAD-authenticated AKS. The plugin binary has to be on your `PATH`, as it does for `kubectl`; kute bundles no cloud SDKs.

One difference worth knowing about: kute holds the terminal, so a credential plugin can never prompt you. When a plugin's own login has expired, kute says so — with the plugin's own message — and stops retrying rather than re-running it every couple of seconds. Re-authenticate in another shell (`aws sso login`, `gcloud auth login`, …) and press `r`.

## Configuration

kute reads `~/.config/kute/config.yaml`. Every key is optional — with no file at all, kute runs with the defaults below.

```yaml
# Contexts to treat as production. This is the ONLY source of PROD status —
# kute never guesses from a context's name. Toggle with ctrl+p in the
# context palette (c) instead of hand-editing this list.
prodContexts:
  - prod-eks
  - aks-prod-eastus2

# Force a theme: dark | light. Omit to detect from the terminal background.
# The --theme flag overrides this.
theme: dark

# Image kubectl debug uses for the node-shell verb ('s' on a node).
# Override it for clusters that can't pull from Docker Hub.
# Default: busybox:1.37
nodeShellImage: my-registry.internal/busybox:1.37

update:
  # Set false to disable the once-per-24h release-feed check that drives the
  # update-available chip. Useful behind egress-flagging proxies.
  check: false
```

### `prodContexts` and destructive actions

`prodContexts` is what escalates kute's confirmation friction, so it's worth setting before you need it:

| | Non-prod context | Context listed in `prodContexts` |
| --- | --- | --- |
| Delete, rollout restart | inline `y/N` | type the resource's name to confirm |
| Force delete (`ctrl+k`) | staged inside the inline `y/N` | type the resource's name |
| Drain | `y/N` confirm | `y/N` confirm |
| Edit, set image, set resources | applies on save | inline `y/N` first |
| Cordon / uncordon | applies immediately | applies immediately |

A context you haven't listed gets the lighter confirmations — including a production cluster kute has no way of recognizing. You can also mark or unmark the selected context with `ctrl+p` in the context palette (`c`), which writes back to this same file, so every other kute session picks it up.

## Running From Source

```sh
mise install               # install deps
go run ./cmd/kute          # against your current kubeconfig
go run ./cmd/kute --demo   # demo mode, no cluster needed
```

## Platforms

Prebuilt binaries (with checksums) are published for Linux, macOS, and Windows on amd64/arm64. Exact toolchain versions for building from source are pinned in `mise.toml` — `mise install` picks them up automatically.

## Project status

kute is pre-1.0 and under active development. Interfaces, keybindings, and screens may still change between releases.

## License

Apache License 2.0 — see [LICENSE](LICENSE) and [NOTICE](NOTICE).

## Contributing

Contributions are not accepted at this time.

Use [GitHub Discussions](https://github.com/kute-dev/kute/discussions) for questions, general feedback, and early-stage feature ideas.

Use [issues](https://github.com/kute-dev/kute/issues/new/choose) for confirmed bugs or well-scoped, actionable requests.
