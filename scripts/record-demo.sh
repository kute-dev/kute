#!/usr/bin/env bash
# Regenerates a docs/assets/*.gif from a docs/assets/*.tape (default:
# demo-all-namespaces.tape, the one embedded in README.md).
# Requires vhs (pinned in mise.toml) plus its own ffmpeg/ttyd dependencies.
#
# The recorded `kute --demo` process runs with an isolated XDG_STATE_HOME so
# it never reads or writes the real ~/.local/state/kute/state.json (the same
# file real, non-demo kute usage on this machine persists to) — recordings
# stay reproducible and side-effect-free regardless of what's in that file.
set -euo pipefail
cd "$(dirname "$0")/.."

tape="${1:-docs/assets/demo-all-namespaces.tape}"
vhs_bin="$(mise which vhs)"

tmpdir="$(mktemp -d)"
statedir="$(mktemp -d)"
trap 'rm -rf "$tmpdir" "$statedir"' EXIT

go build -o "$tmpdir/kute" ./cmd/kute
PATH="$tmpdir:$PATH" XDG_STATE_HOME="$statedir" "$vhs_bin" "$tape"
