#!/usr/bin/env bash
# release-notes.sh — generate the two 28a/28b changelog artifacts for a tag, via
# git-cliff (see cliff.toml), for the release workflow to hand to goreleaser.
#
# Usage: scripts/release-notes.sh <tag>
#
# Writes:
#   .release/notes.md       — pretty GitHub release body (goreleaser --release-notes)
#   .release/changelog.json — [{type, text}], type in new|fix|perf (goreleaser release.extra_files)
#
# Must run with HEAD checked out at <tag> and full history/tags available
# (`git fetch --tags` / `actions/checkout` with fetch-depth: 0) — --current
# processes exactly the commits git-cliff attributes to the tag pointing at
# HEAD, not just whatever tag happens to sort highest.

set -euo pipefail

TAG="${1:?usage: release-notes.sh <tag>}"

log() { printf '\033[1;34m==>\033[0m %s\n' "$*" >&2; }

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONFIG="${REPO_ROOT}/cliff.toml"
OUT_DIR="${REPO_ROOT}/.release"

mkdir -p "$OUT_DIR"

log "Generating release notes for ${TAG}"
git-cliff --config "$CONFIG" --current --strip header --output "${OUT_DIR}/notes.md"

log "Generating changelog.json for ${TAG}"
git-cliff --config "$CONFIG" --current --context | jq '
  [
    .[0].commits[]
    | select(.group != null)
    | {
        _rank: (.group | capture("<!-- (?<n>[0-9]+) -->").n | tonumber),
        type: (.group | sub("^<!-- [0-9]+ --> *"; "")),
        text: .message
      }
  ]
  | sort_by(._rank)
  | map(del(._rank))
' > "${OUT_DIR}/changelog.json"
