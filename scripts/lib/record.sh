# Shared recording machinery for record-demo.sh and record-all-demos.sh.
# Sourced, not executed. The caller must already have cd'd to the repo root.
#
# This file exists because the two scripts used to carry their own copy of the
# isolation block, and the copies drifted: the batch one had lost the macOS
# font symlink, so a `record-all-demos.sh` run on a Mac resolved a different
# font from a `record-demo.sh` run of the same tape.
#
# Layout it assumes (docs/assets/README.md is the reference):
#   docs/assets/tapes/    source, one tape per capture
#   docs/assets/shots/    home-<stem>.png and the derived home-<stem>-light.png
#   docs/assets/demos/    demo-<stem>.mp4 (re-timed here) and demo-<stem>.png

# record::setup builds the kute binary every tape runs against and locates
# betamax. Call once; both are shared across a whole batch.
record::setup() {
	record_betamax="$(mise which betamax)"
	record_bindir="$(mktemp -d)"
	go build -o "$record_bindir/kute" ./cmd/kute
}

record::teardown() {
	[[ -n "${record_bindir:-}" ]] && rm -rf "$record_bindir"
}

# record::run_isolated <tape> runs one tape under a throwaway HOME and
# XDG_STATE_HOME, so the recorded `kute --demo` never reads or writes the
# operator's real kubeconfig, config or session state. That isolation is
# load-bearing rather than tidiness: home-prod-confirm.tape writes a config
# marking the demo context as production, and the navigation tapes seed
# recents and an unroutable kubeconfig of their own.
#
# Only the platform font directories are exposed inside the fake home, so
# betamax can resolve the font family the tape declares.
record::run_isolated() {
	local tape="$1"
	local statedir homedir
	statedir="$(mktemp -d)"
	homedir="$(mktemp -d)"

	if [[ -d "$HOME/.local/share/fonts" ]]; then
		mkdir -p "$homedir/.local/share"
		ln -s "$HOME/.local/share/fonts" "$homedir/.local/share/fonts"
	fi
	if [[ -d "$HOME/Library/Fonts" ]]; then
		mkdir -p "$homedir/Library"
		ln -s "$HOME/Library/Fonts" "$homedir/Library/Fonts"
	fi

	PATH="$record_bindir:$PATH" HOME="$homedir" XDG_STATE_HOME="$statedir" \
		"$record_betamax" run "$tape"

	rm -rf "$statedir" "$homedir"
}

# record::tape <tape> records a tape, and for a homepage checkpoint records its
# light counterpart too.
#
# A home-* tape is authored as the dark capture and stays directly runnable
# (`betamax run docs/assets/tapes/home-triage.tape` writes the dark PNG). The
# light PNG used to be a second checked-in tape that differed from its twin by
# exactly these three lines — 25 near-duplicate files, which is how
# home-prod-confirm ended up 800px tall in one theme and 1100 in the other.
# Deriving it means the two captures cannot disagree about anything else.
#
# Substituting into a temp copy rather than sharing a fragment between tapes is
# deliberate: betamax 0.1.15 has no include directive, so a fragment would have
# to be concatenated in, and then neither `betamax run` nor `betamax validate`
# would work on the checked-in file. The three substitutions are asserted to be
# sufficient by TestRecordingTapesAndAssetsAgree, which requires every home-*
# tape to declare `Set Theme "3024 Night"` and pass `--theme dark`.
record::tape() {
	local tape="$1" stem
	stem="$(basename "$tape" .tape)"

	if [[ "$stem" == demo-* ]]; then
		record::demo "$tape" "$stem"
		return 0
	fi

	record::run_isolated "$tape"

	[[ "$stem" == home-* ]] || return 0

	# Kept next to the built binary (cleaned up by record::teardown) and named
	# .tape so betamax sees the extension it expects. Every Output path in a
	# tape is relative to the repo root, not to the tape, so a temp copy still
	# writes into docs/assets/shots/.
	local light="$record_bindir/${stem}-light.tape"
	sed -e 's|^\(Output "docs/assets/shots/[^"]*\)\.png"|\1-light.png"|' \
		-e 's|^Set Theme "3024 Night"|Set Theme "3024 Day"|' \
		-e 's|--theme dark|--theme light|' \
		"$tape" >"$light"
	record::run_isolated "$light"
	rm -f "$light"
}

# record::demo <tape> <stem> records a demo clip and encodes its mp4 here rather
# than letting betamax write it.
#
# betamax captures a frame per damage event and carries a real duration
# alongside each one, but `write_video_inner` (betamax-core 0.1.11) drops those
# durations on the floor: it dumps the frames as a numbered PNG sequence and
# hands ffmpeg `-framerate <Set Framerate>`, so every frame gets an identical
# 1/24s slot no matter how long it was actually on screen. A tape's Sleeps then
# contribute nothing at all to the clip — demo-goto-palette's ~15s walkthrough
# came out as 58 frames of 2.4s, every pause gone and every keystroke a blur.
# The GIF writer, on the same frames, honors the durations exactly.
#
# So the recording asks for what betamax gets right — full-color PNG frames, and
# a GIF whose only job is to carry the per-frame delays — and this encodes the
# two together through ffmpeg's concat demuxer. Frame counts cannot disagree:
# both writers iterate the same captured slice, one entry per frame.
#
# This is not the pre-betamax `gif-to-mp4.sh` coming back. That transcoded the
# GIF's *pixels*, publishing video quantised to 256 colours; here the pixels are
# the untouched PNGs and the GIF contributes nothing but its delay table.
record::demo() {
	local tape="$1" stem="$2"
	local work="$record_bindir/$stem" retimed="$record_bindir/$stem-frames.tape"
	local framerate
	mkdir -p "$work"

	framerate="$(awk '$1 == "Set" && $2 == "Framerate" { rate = $3 } END { print rate }' "$tape")"
	framerate="${framerate:-24}"

	# The tape stays the checked-in one in every respect except where the video
	# goes; the mid-tape `Screenshot` that writes demos/<stem>.png is untouched
	# and still lands in the repo, as do the Output paths' repo-relative
	# semantics (betamax resolves them against the cwd, not against the tape).
	awk -v frames="$work/frames" -v gif="$work/timing.gif" '
		/^Output "docs\/assets\/demos\/.*\.mp4"$/ {
			print "Output \"" frames "\""
			print "Output \"" gif "\""
			next
		}
		{ print }
	' "$tape" >"$retimed"

	record::run_isolated "$retimed"
	record::encode_demo "$work" "$framerate" "docs/assets/demos/$stem.mp4"
	rm -rf "$work" "$retimed"
}

# record::encode_demo <workdir> <framerate> <out.mp4> joins the captured PNG
# frames to the delays recorded in the GIF and encodes the clip.
#
# Output is constant-framerate rather than the variable-framerate stream concat
# produces on its own: this plays in a <video> on kute.dev, and a browser
# seeking a VFR stream with multi-second frames is the kind of thing that works
# on the machine you tested it on. The duplicated frames of a still terminal
# cost almost nothing through x264.
record::encode_demo() {
	local work="$1" framerate="$2" out="$3"

	ffprobe -v error -select_streams v -show_entries frame=duration_time \
		-of csv=p=0 "$work/timing.gif" >"$work/delays.txt"
	(cd "$work/frames" && ls -1 -- *.png) >"$work/frames.txt"

	awk -v out="$out" '
		NR == FNR { delay[FNR] = $0; delays = FNR; next }
		{ frame[FNR] = $0; frames = FNR }
		END {
			if (delays != frames) {
				printf "%s: betamax captured %d frames but %d delays\n",
					out, frames, delays > "/dev/stderr"
				exit 1
			}
			print "ffconcat version 1.0"
			for (i = 1; i <= frames; i++) {
				printf "file '\''frames/%s'\''\nduration %s\n", frame[i], delay[i]
			}
			# concat drops the final entry'\''s duration, so the last frame is
			# repeated to give the closing Sleep its time on screen.
			printf "file '\''frames/%s'\''\n", frame[frames]
		}
	' "$work/delays.txt" "$work/frames.txt" >"$work/concat.txt"

	ffmpeg -y -loglevel error -f concat -safe 0 -i "$work/concat.txt" \
		-fps_mode cfr -r "$framerate" \
		-c:v libx264 -crf 20 -preset slow \
		-pix_fmt yuv420p -movflags +faststart "$out"
	echo "wrote $out"
}
