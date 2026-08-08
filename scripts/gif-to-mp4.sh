#!/usr/bin/env bash
# Derives docs/assets/<name>.mp4 from each docs/assets/<name>.gif.
#
# The website plays these as <video>; the GIFs stay because README.md embeds
# them and GitHub markdown cannot play a local video file. Same recordings,
# two containers, one source of truth in the .tape files.
#
# Settings, all of which were measured on these recordings rather than
# guessed:
#   -fps_mode passthrough  Betamax writes variable-duration frames. Preserving
#                          their timing keeps scripted sleeps from collapsing
#                          into a fast, blinking constant-frame-rate video.
#   -crf 24                Terminal text is unforgiving. At 24 the result is
#                          hard to tell from the GIF; by 30 the dim greys
#                          start to smear.
#   -pix_fmt yuv420p       Required by Safari and by QuickTime.
#   +faststart             Moves the index to the front so playback can begin
#                          before the whole file has arrived.
set -euo pipefail
cd "$(dirname "$0")/.."

command -v ffmpeg >/dev/null 2>&1 || {
	echo "gif-to-mp4: ffmpeg is not installed" >&2
	exit 1
}

shopt -s nullglob
gifs=(docs/assets/*.gif)
[ ${#gifs[@]} -gt 0 ] || {
	echo "gif-to-mp4: no docs/assets/*.gif found" >&2
	exit 1
}

for gif in "${gifs[@]}"; do
	mp4="${gif%.gif}.mp4"
	poster="${gif%.gif}-poster.jpg"
	ffmpeg -y -v error -i "$gif" \
		-c:v libx264 -crf 24 -preset slow \
		-pix_fmt yuv420p -fps_mode passthrough \
		-movflags +faststart -an "$mp4"
	# Nothing autoplays for a visitor who asked for reduced motion, or who
	# has JS off, so without a poster they would get an empty box.
	ffmpeg -y -v error -i "$gif" -frames:v 1 -q:v 4 "$poster"
	printf '%-52s %5s -> %5s (+%s poster)\n' "$(basename "$mp4")" \
		"$(du -h "$gif" | cut -f1)" "$(du -h "$mp4" | cut -f1)" \
		"$(du -h "$poster" | cut -f1 | tr -d ' ')"
done
