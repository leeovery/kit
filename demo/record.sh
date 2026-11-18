#!/usr/bin/env bash
# Record the README's and the docs' art: the art named, or all of it,
# through vhs, then finalize.sh. A tape's first line says what it makes:
# "# art: still" a still, cut to what it draws; "# art: whole" a still, the
# window kept whole, as the README's screens are; "# art: part of <clip>" a
# part of a clip, the clip's parts recorded in their names' order and
# joined; any other line a clip of its own. A still that's a window of its own has no tape in demo/:
# its tape is made from what capturetool --stills says of it, into
# demo/out/tapes. Run it from anywhere; it works from the repository's
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
# sandbox off. It needs vhs, ffmpeg, libwebp's tools, apngasm, Go and the
# font the tapes draw in, and stops first, naming what's missing, without
# them.
#
# Usage:  demo/record.sh               all the art
#         demo/record.sh new-mac       the art named: a tape's, a still's
#                                      or a clip's whose parts are tapes
set -euo pipefail

cd "$(dirname "$0")/.."

# missing prints each of what record.sh and finalize.sh need that isn't
# here, and how to get it.
missing() {
	local program
	for program in vhs ffmpeg ffprobe cwebp apngasm go; do
		command -v "$program" >/dev/null || echo "  $program: brew install $(case "$program" in ffprobe) echo ffmpeg ;; cwebp) echo webp ;; *) echo "$program" ;; esac)"
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

# record <name> [tape] records the tape, demo/<name>.tape unless it's
# named, into demo/out/<name>.
record() {
	local tape="${2:-demo/$1.tape}"
	rm -rf "demo/out/$1"
	TMPDIR="$PWD/demo/out/.vhs" vhs --quiet "$tape"
	[ -f "demo/out/$1/frame-text-00001.png" ] || {
		echo "$tape recorded no frames" >&2
		exit 1
	}
}

# still_of prints what capturetool --stills says of the still named: its
# look, columns, lines, the words it shows and its art, still or whole,
# tab-separated; nothing for another name.
still_of() {
	demo/out/capturetool --stills | awk -F '\t' -v name="$1" '$1 == name { print $2 "\t" $3 "\t" $4 "\t" $5 "\t" $6 }'
}

# still_tape <name> <look> <columns> <lines> <shows> <art> writes the tape
# of a still that's a window of its own, and prints its path: the window its
# size, in cells of look.tape's 13 × 29 px; the capture tool drawing it on
# a cleared screen, recorded once it shows what it shows, and stopped a
# moment after the recording is, as vhs can take a frame late.
still_tape() {
	local look=look tape="demo/out/tapes/$1.tape"
	[ "$2" = ghostty ] || look="look-$2"
	mkdir -p demo/out/tapes
	cat >"$tape" <<-TAPE
		# art: $6, made by demo/record.sh from capturetool --stills
		Output "demo/out/$1/"
		Source "demo/$look.tape"
		Set Width $(($3 * 13 + 60))
		Set Height $(($4 * 29 + 44))
		Hide
		Type "clear && demo/out/capturetool --fixture $1"
		Enter
		Wait+Screen /$5/
		Show
		Sleep 500ms
		Hide
		Sleep 300ms
		Enter
	TAPE
	echo "$tape"
}

mkdir -p demo/out/.vhs
go build -o demo/out/capturetool ./cmd/capturetool

names=("$@")
if [ ${#names[@]} -eq 0 ]; then
	while IFS=$'\t' read -r name _; do
		names+=("$name")
	done < <(demo/out/capturetool --stills)
	for tape in demo/*.tape; do
		name="$(basename "$tape" .tape)"
		case "$name" in look | look-*) continue ;; esac
		art="$(art_of "$name")"
		[[ "$art" != "part of "* ]] || name="${art#part of }"
		[[ " ${names[*]-} " == *" $name "* ]] || names+=("$name")
	done
fi

for name in "${names[@]}"; do
	if [ -f "demo/$name.tape" ]; then
		record "$name"
		case "$(art_of "$name")" in
		still) demo/finalize.sh --still "$name" ;;
		whole) demo/finalize.sh --whole "$name" ;;
		"part of "*) ;;
		*) demo/finalize.sh "$name" ;;
		esac
		continue
	fi
	still="$(still_of "$name")"
	if [ -n "$still" ]; then
		IFS=$'\t' read -r look columns lines shows art <<<"$still"
		record "$name" "$(still_tape "$name" "$look" "$columns" "$lines" "$shows" "$art")"
		demo/finalize.sh --"$art" "$name"
		continue
	fi
	parts=()
	while read -r part; do
		parts+=("$part")
	done < <(parts_of "$name")
	[ ${#parts[@]} -gt 0 ] || {
		echo "no tape demo/$name.tape, nor a still of capturetool's, nor tapes that are parts of $name" >&2
		exit 1
	}
	for part in "${parts[@]}"; do
		record "$part"
	done
	demo/finalize.sh --join "$name" "${parts[@]}"
done
