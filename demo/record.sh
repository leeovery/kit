#!/usr/bin/env bash
# Record the README's demos and make their art: the art named, or all of
# it, through vhs, then finalize.sh. A tape's first line says what it makes:
# "# art: still" a still; "# art: part of <clip>" a part of a clip, the
# clip's parts recorded in their names' order and joined; any other line a
# clip of its own. Run it from anywhere; it works from the repository's
# root, where the tapes' paths start.
#
# Every tape draws kit's screens through the capture tool (cmd/capturetool),
# built afresh into demo/out first: never kit itself, which changes the Mac
# it runs on.
#
# vhs records a tape's frames into a directory under $TMPDIR, then moves it
# to the tape's Output. Where $TMPDIR is on another volume than the
# repository, the move fails without a word and no frames come, so vhs is
# given a TMPDIR in demo/out. A tape's old frames are deleted first, so a
# recording that fails without a word can't leave them to be taken for new.
#
# vhs's browser reaches ttyd over loopback: run this with an agent's
# sandbox off. It needs vhs, ffmpeg, libwebp's tools, Go and the font the
# tapes draw in, and stops first, naming what's missing, without them.
#
# Usage:  demo/record.sh               all the art
#         demo/record.sh new-mac       the art named: a tape's, or a clip's
#                                      whose parts are tapes
set -euo pipefail

cd "$(dirname "$0")/.."

# missing prints each of what record.sh and finalize.sh need that isn't
# here, and how to get it.
missing() {
	local program
	for program in vhs ffmpeg ffprobe img2webp cwebp go; do
		command -v "$program" >/dev/null || echo "  $program: brew install $(case "$program" in ffprobe) echo ffmpeg ;; img2webp | cwebp) echo webp ;; *) echo "$program" ;; esac)"
	done
	compgen -G "$HOME/Library/Fonts/JetBrainsMono-Regular.*" >/dev/null ||
		compgen -G "/Library/Fonts/JetBrainsMono-Regular.*" >/dev/null ||
		echo "  JetBrains Mono: brew install --cask font-jetbrains-mono"
}

if needed="$(missing)" && [ -n "$needed" ]; then
	echo "demo/record.sh needs:" >&2
	echo "$needed" >&2
	exit 1
fi

# art_of prints what the tape named makes, as its first line says.
art_of() {
	sed -n '1s/^# art: //p' "demo/$1.tape"
}

# parts_of prints the tapes that are parts of the clip named, in order.
parts_of() {
	local tape
	for tape in demo/*.tape; do
		tape="$(basename "$tape" .tape)"
		[ "$(art_of "$tape")" != "part of $1" ] || echo "$tape"
	done
}

record() {
	rm -rf "demo/out/$1"
	TMPDIR="$PWD/demo/out/.vhs" vhs --quiet "demo/$1.tape"
	[ -f "demo/out/$1/frame-text-00001.png" ] || {
		echo "demo/$1.tape recorded no frames" >&2
		exit 1
	}
}

names=("$@")
if [ ${#names[@]} -eq 0 ]; then
	for tape in demo/*.tape; do
		name="$(basename "$tape" .tape)"
		case "$name" in look | look-*) continue ;; esac
		art="$(art_of "$name")"
		[[ "$art" != "part of "* ]] || name="${art#part of }"
		[[ " ${names[*]-} " == *" $name "* ]] || names+=("$name")
	done
fi

mkdir -p demo/out/.vhs
go build -o demo/out/capturetool ./cmd/capturetool
for name in "${names[@]}"; do
	if [ -f "demo/$name.tape" ]; then
		record "$name"
		case "$(art_of "$name")" in
		still) demo/finalize.sh --still "$name" ;;
		"part of "*) ;;
		*) demo/finalize.sh "$name" ;;
		esac
		continue
	fi
	parts=()
	while read -r part; do
		parts+=("$part")
	done < <(parts_of "$name")
	[ ${#parts[@]} -gt 0 ] || {
		echo "no tape demo/$name.tape, nor tapes that are parts of $name" >&2
		exit 1
	}
	for part in "${parts[@]}"; do
		record "$part"
	done
	demo/finalize.sh --join "$name" "${parts[@]}"
done
