#!/usr/bin/env bash
set -euo pipefail

# Expected location:
#   <repo>/scripts/browse-goldens
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "$SCRIPT_DIR/.." && pwd)"
GOLDEN_DIR="$REPO_ROOT/test/golden"

if command -v fd >/dev/null 2>&1; then
  FD=fd
elif command -v fdfind >/dev/null 2>&1; then
  FD=fdfind
else
  printf 'error: required command not found: fd or fdfind\n' >&2
  exit 1
fi

for command in "$FD" fzf less; do
  if ! command -v "$command" >/dev/null 2>&1; then
    printf 'error: required command not found: %s\n' "$command" >&2
    exit 1
  fi
done

if [[ ! -d "$GOLDEN_DIR" ]]; then
  printf 'error: golden directory not found: %s\n' "$GOLDEN_DIR" >&2
  exit 1
fi

"$FD" \
  --type f \
  --extension golden \
  --print0 \
  . "$GOLDEN_DIR" |
fzf \
  --read0 \
  --layout=reverse \
  --border \
  --prompt='golden> ' \
  --preview='cat -- {}' \
  --preview-window='right,70%,wrap' \
  --bind='enter:execute(less -R -- {})' \
  >/dev/null
