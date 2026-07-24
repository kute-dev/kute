# kute

**Modern Kubernetes TUI**

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
- **Deliberate friction on destructive actions** — reversible verbs like cordon execute immediately; delete and rollout-restart are tiered, with inline y/N confirmation normally and a type-the-name modal when the context is explicitly tagged as production (via kubeconfig annotation, never guessed from a name). Drain and force-delete always require the modal.
- **CRDs work without a configuration project** — kinds discovered from the API automatically get columns, status, and detail views. No plugins, no per-CRD setup.

## Resilience & safety

When the API server connection drops, kute keeps showing your last known state (desaturated, with an age stamp) instead of blanking the screen — and disables mutating actions until the connection is back.

## Install

```sh
curl -fsSL https://kute.dev/install.sh | sh
```

Or with Homebrew:

```sh
brew install kute-dev/tap/kute
```

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
