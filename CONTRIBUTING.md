# Contributing to kute

Thank you for wanting to help. Experience reports, bug reports, feature ideas,
documentation, tests, and code can all improve kute.

## Before you start

Please do not start work on a pull request before discussing it with the maintainer.
This avoids duplicated work and confirms that the problem, scope, and proposed approach
fit the project before you invest time in an implementation.

- Use [GitHub Discussions](https://github.com/kute-dev/kute/discussions) for questions,
  general feedback, experience reports, and early-stage ideas.
- Use the [bug form](https://github.com/kute-dev/kute/issues/new?template=bug_report.yml)
  for a reproducible bug.
- Use the [actionable request form](https://github.com/kute-dev/kute/issues/new?template=actionable_request.yml)
  for a well-scoped change that is ready for issue tracking.
- Report security vulnerabilities privately through
  [GitHub Security Advisories](https://github.com/kute-dev/kute/security/advisories/new),
  following [SECURITY.md](SECURITY.md). Do not open a public issue for them.

Wait for agreement on the scope and approach before writing code. Agreement to explore
an idea is not necessarily agreement to a particular implementation.

## Share your experience

Reports from real environments are useful even when nothing is broken. When relevant,
include:

- kute version and installation method
- Kubernetes version and distribution
- cloud provider or on-premises environment
- operating system and architecture
- terminal emulator and terminal size
- authentication method, such as a client certificate, OIDC, or exec plugin
- whether metrics-server and the resource kinds involved are available

Omit credentials, tokens, Secret values, and any cluster, context, namespace, or resource
names you cannot share. Crash reports and `--log-file` output contain context, cluster,
and namespace names, so review and redact them before posting.

## Development setup

The toolchain is pinned with [mise](https://mise.jdx.dev/):

```sh
mise install
go run ./cmd/kute --demo
```

Demo mode requires no cluster or kubeconfig. To run against a cluster, use
`go run ./cmd/kute`; the normal kubeconfig resolution rules apply.

Before changing behavior, read the relevant design and architecture documentation:

- [`docs/design/README.md`](docs/design/README.md) is the source of truth for screens,
  layout, keys, tokens, and glyphs.
- [`docs/lazy-informers.md`](docs/lazy-informers.md) is required reading before changing
  informer startup, cache reads, discovery, or loading states.

Keep changes focused on the agreed issue or Discussion. In particular:

- Route Kubernetes access through the existing interfaces; UI code must not import
  client-go directly.
- Register verbs and resource kinds in their shared registries rather than wiring them
  into one view.
- Use semantic theme tokens in views, with both dark and light variants. Do not add inline
  color literals.
- Keep render functions pure: no I/O, clock reads, or global state.
- Add or update tests for behavior changes. UI changes must cover both themes and update
  golden fixtures only when the rendering change is intentional.

## Validate your change

Run the tests relevant to your change while developing. Before opening a pull request,
run the checks used by CI:

```sh
go test ./...
go vet ./...
golangci-lint run
```

For UI changes, run the affected package's golden tests in both themes. Regenerate an
intentional golden change with `UPDATE_GOLDEN=1 go test ./path/to/package`, then review
the fixture diff.

Changes to informer startup, cache behavior, discovery, or other concurrency-sensitive
Kubernetes code should also pass:

```sh
go test -race ./internal/kube
```

The full real-cluster E2E suite runs in CI. If your agreed change needs local E2E coverage,
follow [`docs/e2e-plan.md`](docs/e2e-plan.md) and use the repository's kind cluster scripts.

## Pull requests

Once implementation has been agreed:

- Link the Discussion or issue that established the scope.
- Explain the user-visible behavior and any deliberate tradeoffs.
- Keep unrelated refactors out of the change.
- Include tests and documentation needed to keep the behavior accurate.
- Call out checks you could not run and why.

Review may ask for a different approach or a smaller scope. A prior discussion makes that
less likely, but does not guarantee that a pull request will be merged.
