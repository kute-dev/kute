#!/usr/bin/env bash
# Regenerates media from a docs/assets/*.tape (default:
# demo-all-namespaces.tape, the one embedded in README.md).
# Requires betamax (pinned in mise.toml) — renders through libghostty-vt
# in-process, no browser/ttyd stack.
#
# The recorded `kute --demo` process runs with isolated HOME and XDG_STATE_HOME
# so it never reads or writes the real config or session state. Recordings stay
# reproducible and side-effect-free regardless of what is on the host. The
# isolated home links only the user's platform-specific font directories so
# Betamax can resolve the font family declared by the tape.
set -euo pipefail
cd "$(dirname "$0")/.."

tape="${1:-docs/assets/demo-all-namespaces.tape}"
betamax_bin="$(mise which betamax)"

tmpdir="$(mktemp -d)"
statedir="$(mktemp -d)"
homedir="$(mktemp -d)"
trap 'rm -rf "$tmpdir" "$statedir" "$homedir"' EXIT

if [[ -d "$HOME/.local/share/fonts" ]]; then
	mkdir -p "$homedir/.local/share"
	ln -s "$HOME/.local/share/fonts" "$homedir/.local/share/fonts"
fi
if [[ -d "$HOME/Library/Fonts" ]]; then
	mkdir -p "$homedir/Library"
	ln -s "$HOME/Library/Fonts" "$homedir/Library/Fonts"
fi

go build -o "$tmpdir/kute" ./cmd/kute
PATH="$tmpdir:$PATH" HOME="$homedir" XDG_STATE_HOME="$statedir" "$betamax_bin" run "$tape"
# scripts/gif-to-mp4.sh
