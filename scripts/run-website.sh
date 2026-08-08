#!/usr/bin/env bash
# Build a deployment-shaped local website and serve it until interrupted.
#
# Usage:
#   scripts/run-website.sh         # http://127.0.0.1:4174/
#   scripts/run-website.sh 8000    # use another port
#
# Set KUTE_SITE_NO_OPEN=1 to skip opening a browser.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
port="${1:-4174}"
dist="$root/website/dist"
url="http://127.0.0.1:${port}/"

if [[ ! "$port" =~ ^[0-9]+$ ]] || ((port < 1 || port > 65535)); then
	printf 'run-website: invalid port: %s\n' "$port" >&2
	exit 1
fi

cd "$root"
rm -rf "$dist"
go run ./cmd/site

mkdir -p "$dist/assets"
cp -R website/assets/. "$dist/assets/"
cp website/CNAME website/favicon.ico website/install.ps1 website/install.sh "$dist/"

shopt -s nullglob
demo_assets=(docs/assets/*.mp4 docs/assets/*-poster.jpg docs/assets/home-*.png)
if ((${#demo_assets[@]})); then
	cp "${demo_assets[@]}" "$dist/assets/"
fi

printf 'Serving %s at %s\n' "$dist" "$url"
if [[ "${KUTE_SITE_NO_OPEN:-0}" != "1" ]]; then
	if command -v xdg-open >/dev/null 2>&1; then
		xdg-open "$url" >/dev/null 2>&1 &
	elif command -v open >/dev/null 2>&1; then
		open "$url" >/dev/null 2>&1 &
	else
		printf 'No browser opener found; open %s manually.\n' "$url" >&2
	fi
fi

cd "$dist"
exec python3 -m http.server "$port" --bind 127.0.0.1
