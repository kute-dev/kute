#!/usr/bin/env bash
# Regenerates every docs/assets/*.gif from its docs/assets/*.tape, sharing one
# `kute` build and one betamax lookup across all of them (see record-demo.sh
# for the single-tape version this wraps the same isolation approach from).
# Requires betamax (pinned in mise.toml) — renders through libghostty-vt
# in-process, no browser/ttyd stack.
#
# Each recorded `kute --demo` process runs with its own isolated
# XDG_STATE_HOME so it never reads or writes the real
# ~/.local/state/kute/state.json (the same file real, non-demo kute usage on
# this machine persists to) — recordings stay reproducible and
# side-effect-free regardless of what's in that file, and independent of
# each other across the batch.
set -euo pipefail
cd "$(dirname "$0")/.."

betamax_bin="$(mise which betamax)"

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

go build -o "$tmpdir/kute" ./cmd/kute

for tape in docs/assets/*.tape; do
	echo "== $tape =="
	statedir="$(mktemp -d)"
	PATH="$tmpdir:$PATH" XDG_STATE_HOME="$statedir" "$betamax_bin" run "$tape"
	rm -rf "$statedir"
done
