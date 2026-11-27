#!/bin/sh
# Tests install.sh against a release built from this checkout and served
# from a local HTTP server, inside fresh containers.
#
#   e2e/install-test.sh [image...]     (default: the images below)
#
# Needs go, python3 and docker. Inside a container the script runs itself
# with --in-container to make the checks.
set -eu

images="ubuntu:24.04 debian:stable-slim fedora:latest alpine:latest archlinux:latest"
version=0.0.1

fail() {
	printf 'FAIL: %s\n' "$*" >&2
	exit 1
}

# check runs inside a container, as root, with /install.sh and the server
# at $LAZYARIA_BASE_URL.
check() {
	# Ubuntu and Debian images have neither curl nor wget: test both.
	if command -v apt-get >/dev/null; then
		apt-get update -qq >/dev/null
		if grep -q ubuntu /etc/os-release; then dl=curl; else dl=wget; fi
		DEBIAN_FRONTEND=noninteractive apt-get install -y -qq "$dl" ca-certificates >/dev/null
	fi
	bin=$HOME/.local/bin
	base=$LAZYARIA_BASE_URL

	echo "== a tampered checksum aborts"
	if out=$(LAZYARIA_BASE_URL=$base/tampered sh /install.sh --bin-dir /tmp/t 2>&1 </dev/null); then
		fail "tampered install succeeded: $out"
	fi
	echo "$out" | grep -q "checksum mismatch" || fail "no mismatch message: $out"
	[ ! -e /tmp/t/lazyaria ] || fail "tampered binary installed"

	echo "== --no-laria"
	sh /install.sh --bin-dir /tmp/n --no-laria --no-aria2 </dev/null >/dev/null || fail "--no-laria install"
	[ -x /tmp/n/lazyaria ] && [ ! -e /tmp/n/laria ] || fail "--no-laria made laria"

	echo "== a laria that is not ours stays"
	mkdir -p /tmp/f && echo mine >/tmp/f/laria
	sh /install.sh --bin-dir /tmp/f --no-aria2 </dev/null >/dev/null 2>&1 || fail "install next to a foreign laria"
	[ "$(cat /tmp/f/laria)" = mine ] || fail "foreign laria overwritten"

	echo "== a pinned version"
	sh /install.sh --version "v$version" --bin-dir /tmp/p --no-aria2 </dev/null >/dev/null || fail "pinned install"
	/tmp/p/lazyaria version | grep -q "^lazyaria $version " || fail "pinned: wrong version"

	# Last, so the receipt points here for --uninstall.
	echo "== install, piped into sh, no terminal"
	out=$(cat /install.sh | sh 2>&1) || fail "install: $out"
	echo "$out"
	"$bin/lazyaria" version | grep -q "^lazyaria $version " || fail "wrong version"
	[ "$(readlink "$bin/laria")" = lazyaria ] || fail "laria is not a link to lazyaria"
	"$bin/laria" version >/dev/null || fail "laria does not run"
	grep -q '"source": "script"' "$HOME/.local/share/lazyaria/install.json" || fail "no receipt"
	echo "$out" | grep -q "Install aria2 with: " || fail "no aria2 hint"
	echo "$out" | grep -q "is not on your PATH" || fail "no PATH hint"

	echo "== --uninstall"
	out=$(sh /install.sh --uninstall 2>&1) || fail "uninstall: $out"
	echo "$out"
	[ ! -e "$bin/lazyaria" ] && [ ! -L "$bin/laria" ] || fail "binary or laria left behind"
	[ ! -e "$HOME/.local/share/lazyaria/install.json" ] || fail "receipt left behind"
	echo "$out" | grep -q ".config/lazyaria" || fail "config path not printed"
	echo "PASS"
}

if [ "${1:-}" = --in-container ]; then
	check
	exit 0
fi

root=$(cd "$(dirname "$0")/.." && pwd)
[ $# -gt 0 ] && images=$*
work=$(mktemp -d)
trap 'kill "$server" 2>/dev/null; rm -rf "$work"' EXIT

# A release the way GoReleaser lays it out, under both the latest and the
# tagged path, plus a copy whose checksums.txt does not match.
srv=$work/srv
rel=$srv/latest/download
mkdir -p "$rel" "$srv/download" "$srv/tampered/latest/download" "$work/pkg"
(cd "$root" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
	-ldflags "-X github.com/Pranjal-SB/lazyaria/internal/buildinfo.Version=$version" \
	-o "$work/pkg/lazyaria" .)
tar -czf "$rel/lazyaria_linux_amd64.tar.gz" -C "$work/pkg" lazyaria
(cd "$rel" && sha256sum lazyaria_linux_amd64.tar.gz >checksums.txt)
cp -r "$rel" "$srv/download/v$version"
cp "$rel/lazyaria_linux_amd64.tar.gz" "$srv/tampered/latest/download/"
sed "s/^[0-9a-f]*/$(printf '%064d' 0)/" "$rel/checksums.txt" >"$srv/tampered/latest/download/checksums.txt"

port=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1])')
python3 -m http.server -b 127.0.0.1 -d "$srv" "$port" >/dev/null 2>&1 &
server=$!
i=0
until curl -fs "http://127.0.0.1:$port/" >/dev/null; do
	i=$((i + 1))
	[ $i -lt 50 ] || fail "the test server did not start"
	sleep 0.1
done

for image in $images; do
	echo "### $image"
	docker run --rm --network host \
		-e LAZYARIA_BASE_URL="http://127.0.0.1:$port" \
		-v "$root/install.sh:/install.sh:ro" \
		-v "$root/e2e/install-test.sh:/install-test.sh:ro" \
		"$image" sh /install-test.sh --in-container || fail "$image"
done
echo "all images passed"
