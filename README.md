# kute

A TUI Kubernetes console, built with [Bubble Tea](https://github.com/charmbracelet/bubbletea) and [Lip Gloss](https://github.com/charmbracelet/lipgloss).

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

## License

Apache License 2.0 — see [LICENSE](LICENSE) and [NOTICE](NOTICE).

## Contributing

Contributions are not accepted at this time.

Use [GitHub Discussions](https://github.com/kute-dev/kute/discussions) for questions, general feedback, and early-stage feature ideas.

Use [issues](https://github.com/kute-dev/kute/issues/new/choose) for confirmed bugs or well-scoped, actionable requests.
