#!/usr/bin/env bash
# Make the README's art from the frames a tape in demo/ recorded: for a clip,
# an animated WebP, which GitHub shows inline, and an MP4; for a still, a
# lossless WebP of its last frame, cropped to what it drew; for a clip in
# parts, its parts' frames joined, each fading into the next, as the boot in
# Terminal hands over to Ghostty.
#
# vhs writes each frame it shows, a layer of text and one of the cursor,
# exactly as the terminal drew them. The art is made from those, not from a
# video vhs encodes: a video's compression shifts every pixel a little from
# frame to frame, which an animated WebP then has to store whole, where from
# the frames themselves it stores only what changed, losslessly.
#
# Usage:  demo/finalize.sh <name>...                    a clip each
#         demo/finalize.sh --still <name>...            a still each
#         demo/finalize.sh --join <clip> <part>...      a clip of its parts
# Input:  demo/out/<name>/   the frames vhs demo/<name>.tape wrote
# Output: art/<name>.webp, and for a clip, art/<name>.mp4
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
art="$(dirname "$here")/art"
fps=30  # the tapes' Framerate
pad=40  # the margin round the screen, in px
fade=12 # the frames one part takes to fade into the next, 0.4s

mode=clip
case "${1:-}" in
--still | --join)
	mode="${1#--}"
	shift
	;;
esac
[ $# -gt 0 ] || {
	echo "usage: demo/finalize.sh [--still | --join <clip>] <name>..." >&2
	exit 2
}

# drawn_rows <frame> <width> <height> prints the first and last rows of the
# frame with anything drawn on them, counted from 0: rows holding a pixel
# unlike the frame's top-left one, its margin. Every such pixel is made
# non-zero and the rest zero, then each byte is read. Exact to the pixel,
# where ffmpeg's cropdetect is not: it averages a row, so the few pixels of a
# row holding only the prompt read as none.
drawn_rows() {
	local frame="$1" width="$2" height="$3"
	ffmpeg -v error -i "$frame" \
		-filter_complex "[0]format=gbrp,split[f][c];[c]crop=1:1:0:0,scale=$width:$height:flags=neighbor[bg];[f][bg]blend=all_mode=difference,lutrgb=r='if(val,255,0)':g='if(val,255,0)':b='if(val,255,0)',format=gray" \
		-f rawvideo -pix_fmt gray - |
		od -An -v -tu1 |
		awk -v width="$width" '
			/[1-9]/ {
				for (i = 1; i <= NF; i++) if ($i) {
					at = 16 * (NR - 1) + i - 1
					if (first == "") first = at
					last = at
				}
			}
			END { print int(first / width), int(last / width) }'
}

# framed <name> <dir> writes the frames the tape named recorded into dir,
# f00001.png on, each the cursor's layer over the text's, on a margin of the
# theme's background, read from the screen's top-right pixel.
framed() {
	local src="$here/out/$1" dir="$2" size w h
	[ -f "$src/frame-text-00001.png" ] || {
		echo "no frames in demo/out/$1: run 'demo/record.sh $1' first" >&2
		exit 1
	}
	mkdir -p "$dir"
	size="$(ffprobe -v error -select_streams v:0 -show_entries stream=width,height -of csv=p=0 "$src/frame-text-00001.png")"
	w="${size%,*}" h="${size#*,}"
	ffmpeg -v error -y -framerate "$fps" -i "$src/frame-text-%05d.png" -framerate "$fps" -i "$src/frame-cursor-%05d.png" \
		-filter_complex "[0][1]overlay=format=rgb,split[screen][corner];[corner]crop=1:1:$((w - 1)):0,scale=$((w + 2 * pad)):$((h + 2 * pad)):flags=neighbor[margin];[margin][screen]overlay=$pad:$pad:format=rgb" \
		-pix_fmt rgb24 "$dir/f%05d.png"
}

# still <name> <dir> makes the still from the last of dir's frames. Its
# screen is as tall as the run's tallest moment, so its last frame can end
# in empty rows: cut them, leaving under the last row drawn the margin
# above the first.
still() {
	local shot last size w h top bottom
	shot=("$2"/f*.png)
	last="${shot[${#shot[@]} - 1]}"
	size="$(ffprobe -v error -select_streams v:0 -show_entries stream=width,height -of csv=p=0 "$last")"
	w="${size%,*}" h="${size#*,}"
	read -r top bottom <<<"$(drawn_rows "$last" "$w" "$h")"
	cwebp -quiet -lossless -m 6 -crop 0 0 "$w" $((bottom + 1 + top)) "$last" -o "$art/$1.webp"
	printf "   %-30s %s\n" "art/$1.webp" "$(du -h "$art/$1.webp" | cut -f1)"
}

# clip <name> <dir> makes the clip from dir's frames.
clip() {
	img2webp -loop 0 -lossless -q 80 -m 6 -d $((1000 / fps)) "$2"/f*.png -o "$art/$1.webp" >/dev/null
	ffmpeg -v error -y -framerate "$fps" -i "$2/f%05d.png" \
		-c:v libx264 -preset slow -crf 20 -pix_fmt yuv420p -movflags +faststart "$art/$1.mp4"
	local f
	for f in "$1.webp" "$1.mp4"; do
		printf "   %-30s %s\n" "art/$f" "$(du -h "$art/$f" | cut -f1)"
	done
}

# joined <dir> <part>... writes the parts' frames into dir, one after
# another, each part's last frames fading into the next's first.
joined() {
	local dir="$1" so_far part n next
	shift
	so_far="$frames/part-0"
	framed "$1" "$so_far"
	shift
	for part in "$@"; do
		framed "$part" "$frames/$part"
		n="$(find "$so_far" -name 'f*.png' | wc -l | tr -d ' ')"
		next="$frames/upto-$part"
		mkdir -p "$next"
		ffmpeg -v error -y -framerate "$fps" -i "$so_far/f%05d.png" -framerate "$fps" -i "$frames/$part/f%05d.png" \
			-filter_complex "[0][1]xfade=transition=fade:duration=$(awk "BEGIN { print $fade / $fps }"):offset=$(awk "BEGIN { print ($n - $fade) / $fps }"),format=rgb24" \
			"$next/f%05d.png"
		so_far="$next"
	done
	mv "$so_far" "$dir"
}

frames="$(mktemp -d)"
trap 'rm -rf "$frames"' EXIT
mkdir -p "$art"

if [ "$mode" = join ]; then
	name="$1"
	shift
	echo "==> $name, of $*"
	joined "$frames/$name" "$@"
	clip "$name" "$frames/$name"
	exit 0
fi
for name in "$@"; do
	echo "==> $name"
	framed "$name" "$frames/$name"
	"$mode" "$name" "$frames/$name"
done
