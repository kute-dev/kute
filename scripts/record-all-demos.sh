#!/usr/bin/env bash
# Regenerates media from every docs/assets/tapes/*.tape, sharing one `kute`
# build and one betamax lookup across all of them (see record-demo.sh for the
# single-tape version; both drive scripts/lib/record.sh, which is where the
# isolation and the dark/light derivation live).
# Requires betamax (pinned in mise.toml) — renders through libghostty-vt
# in-process, no browser/ttyd stack.
set -euo pipefail
cd "$(dirname "$0")/.."
# shellcheck source=lib/record.sh
source scripts/lib/record.sh

record::setup
trap record::teardown EXIT

for tape in docs/assets/tapes/*.tape; do
	echo "== $tape =="
	record::tape "$tape"
done
