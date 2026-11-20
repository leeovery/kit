#!/bin/sh
# kit's install script: a new Mac's first step, pasted into Terminal.
#
#   /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/leeovery/kit/main/install.sh)"
#
# It downloads kit's newest release for this Mac, checks it against its
# checksum, and runs kit bootstrap from a temporary folder, removed once the
# boot has ended: the boot installs kit through Homebrew, which keeps it up
# to date, and hands over to that kit. KIT_FROM names a build to use
# instead: a path or an address, of kit itself or a release's archive. It's
# put in ~/.local/bin, the kit.app an archive carries in ~/.local/libexec,
# and the boot runs it throughout.
set -eu

repo=leeovery/kit

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

# kit goes in bin, and the kit.app a release ships beside it in libexec,
# where kit looks for it.
mkdir "$tmp/bin" "$tmp/libexec"
if tar -tzf "$tmp/download" kit >/dev/null 2>&1; then
	tar -xzf "$tmp/download" -C "$tmp/bin" kit
	if tar -tzf "$tmp/download" kit.app >/dev/null 2>&1; then
		tar -xzf "$tmp/download" -C "$tmp/libexec" kit.app
	fi
else
	mv "$tmp/download" "$tmp/bin/kit"
fi
chmod 755 "$tmp/bin/kit"

size=$(wc -c <"$tmp/bin/kit" | awk '{ printf "%.1f", $1 / 1000000 }')
version=$("$tmp/bin/kit" --version | sed 's/^kit version /kit /')
printf '%s for %s · %s MB · %s\n' "$version" "$arch" "$size" "$checked"
if [ -n "${KIT_FROM-}" ]; then
	# The boot reads KIT_FROM to run this build throughout.
	export KIT_FROM
	mkdir -p "$HOME/.local/bin"
	mv "$tmp/bin/kit" "$HOME/.local/bin/kit"
	# Another build's kit.app goes, so this kit never takes it for its own.
	rm -rf "$HOME/.local/libexec/kit.app"
	if [ -d "$tmp/libexec/kit.app" ]; then
		mkdir -p "$HOME/.local/libexec"
		mv "$tmp/libexec/kit.app" "$HOME/.local/libexec/kit.app"
	fi
	rm -rf "$tmp"
	exec "$HOME/.local/bin/kit" bootstrap
fi
# A child, not exec'd, so the trap removes the download once the boot ends.
"$tmp/bin/kit" bootstrap
