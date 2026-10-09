#!/bin/sh
# kit's install script: a new Mac's first step, pasted into Terminal.
#
#   /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/leeovery/kit/main/install.sh)"
#
# It downloads kit's newest release for this Mac, checks it against its
# checksum, puts it in ~/.local/bin, and starts kit bootstrap. KIT_FROM names
# a build to use instead: a path or an address, of kit itself or a release's
# archive.
set -eu

repo=leeovery/kit
bin="$HOME/.local/bin"

fail() {
	printf 'kit install: %s\n' "$1" >&2
	exit 1
}

[ "$(uname -s)" = Darwin ] || fail "kit sets up Macs, and this is $(uname -s)"
case "$(uname -m)" in
arm64) arch=arm64 ;;
x86_64) arch=amd64 ;;
*) fail "there's no kit for $(uname -m)" ;;
esac

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

if [ -n "${KIT_FROM-}" ]; then
	case "$KIT_FROM" in
	http://* | https://*) curl -fsSL "$KIT_FROM" -o "$tmp/download" || fail "couldn't download $KIT_FROM" ;;
	*) cp "$KIT_FROM" "$tmp/download" || fail "there's no build at $KIT_FROM" ;;
	esac
	checked="from $KIT_FROM"
else
	# The newest release, as GitHub's latest redirect names it: no API, no
	# sign-in.
	latest=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$repo/releases/latest") ||
		fail "couldn't reach GitHub"
	tag=${latest##*/}
	[ "$tag" != latest ] || fail "kit has no release yet"
	version=${tag#v}
	archive="kit_${version}_darwin_${arch}.tar.gz"
	from="https://github.com/$repo/releases/download/$tag"
	curl -fsSL "$from/$archive" -o "$tmp/download" || fail "couldn't download $archive"
	curl -fsSL "$from/kit_${version}_checksums.txt" -o "$tmp/checksums" || fail "couldn't download its checksums"
	want=$(awk -v f="$archive" '$2 == f { print $1 }' "$tmp/checksums")
	got=$(shasum -a 256 "$tmp/download" | awk '{ print $1 }')
	[ -n "$want" ] && [ "$want" = "$got" ] || fail "$archive doesn't match its checksum"
	checked="checksum ok"
fi

if tar -tzf "$tmp/download" kit >/dev/null 2>&1; then
	tar -xzf "$tmp/download" -C "$tmp" kit
else
	mv "$tmp/download" "$tmp/kit"
fi
chmod 755 "$tmp/kit"
mkdir -p "$bin"
mv "$tmp/kit" "$bin/kit"

size=$(wc -c <"$bin/kit" | awk '{ printf "%.1f", $1 / 1000000 }')
version=$("$bin/kit" version | sed 's/^kit version /kit /')
printf '%s for %s · %s MB · %s\n' "$version" "$arch" "$size" "$checked"
exec "$bin/kit" bootstrap
