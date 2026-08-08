#!/usr/bin/env bash
# Regenerates media from every docs/assets/*.tape, sharing one
# `kute` build and one betamax lookup across all of them (see record-demo.sh
# for the single-tape version this wraps the same isolation approach from).
# Requires betamax (pinned in mise.toml) — renders through libghostty-vt
# in-process, no browser/ttyd stack.
#
# Each recorded `kute --demo` process runs with isolated HOME and
# XDG_STATE_HOME so it never reads or writes the real config or session state.
# Recordings stay reproducible and independent of each other across the batch.
# Each isolated home links only the user's font directory so Betamax can
# resolve the font family declared by the tape.
set -euo pipefail
cd "$(dirname "$0")/.."

betamax_bin="$(mise which betamax)"

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

go build -o "$tmpdir/kute" ./cmd/kute

for tape in docs/assets/*.tape; do
	echo "== $tape =="
	statedir="$(mktemp -d)"
	homedir="$(mktemp -d)"
	if [[ -d "$HOME/.local/share/fonts" ]]; then
		mkdir -p "$homedir/.local/share"
		ln -s "$HOME/.local/share/fonts" "$homedir/.local/share/fonts"
	fi
	PATH="$tmpdir:$PATH" HOME="$homedir" XDG_STATE_HOME="$statedir" "$betamax_bin" run "$tape"
	rm -rf "$statedir" "$homedir"
done

scripts/gif-to-mp4.sh
