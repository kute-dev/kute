#!/usr/bin/env bash
# Regenerates media from one docs/assets/tapes/*.tape (default:
# demo-all-namespaces.tape, the one embedded in README.md).
# Requires betamax (pinned in mise.toml) — renders through libghostty-vt
# in-process, no browser/ttyd stack.
#
# A home-* checkpoint tape produces both PNG themes: the tape is the dark
# capture, and the light one is derived from it. See scripts/lib/record.sh.
set -euo pipefail
cd "$(dirname "$0")/.."
# shellcheck source=lib/record.sh
source scripts/lib/record.sh

tape="${1:-docs/assets/tapes/demo-all-namespaces.tape}"

record::setup
trap record::teardown EXIT
record::tape "$tape"
