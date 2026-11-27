#!/bin/sh
# Installs lazyaria from its GitHub release:
#
#   curl -fsSL https://pranjal-sb.github.io/lazyaria/install.sh | sh
#
# Run it with --help for the options. The whole script is one function that
# runs on the last line, so a download cut short does nothing.
set -eu

usage() {
	cat <<'EOF'
Install lazyaria, a download manager for the terminal.

usage: install.sh [options]

  --version X    install version X (default: the latest release)
  --bin-dir DIR  install into DIR (default: ~/.local/bin)
  --no-laria     do not add the short name laria
  --with-aria2   install aria2 if it is missing, without asking
  --no-aria2     never install aria2 and never ask
  --uninstall    remove lazyaria, laria and the install receipt
  -h, --help     show this help

Environment: LAZYARIA_VERSION, LAZYARIA_BIN_DIR and LAZYARIA_NO_LARIA=1
do the same as --version, --bin-dir and --no-laria.
EOF
}

say() { printf '%s\n' "$*"; }
warn() { printf 'warning: %s\n' "$*" >&2; }
die() {
	printf 'error: %s\n' "$*" >&2
	exit 1
}
has() { command -v "$1" >/dev/null 2>&1; }

# fetch URL FILE downloads over HTTPS only (plain HTTP is allowed only for
# the test override LAZYARIA_BASE_URL).
fetch() {
	if has curl; then
		curl -fsSL --proto "$proto" --proto-redir "$proto" --tlsv1.2 -o "$2" "$1"
	elif has wget; then
		wget -q -O "$2" "$1"
	else
		die "curl or wget is needed to download lazyaria"
	fi
}

sha256() {
	if has sha256sum; then
		sha256sum "$1" | cut -d' ' -f1
	elif has shasum; then
		shasum -a 256 "$1" | cut -d' ' -f1
	elif has sha256; then
		sha256 -q "$1"
	else
		die "sha256sum, shasum or sha256 is needed to check the download"
	fi
}

detect_platform() {
	case $(uname -s) in
	Linux) os=linux ;;
	Darwin) os=darwin ;;
	FreeBSD) os=freebsd ;;
	*) os=unsupported ;;
	esac
	case $(uname -m) in
	x86_64 | amd64) arch=amd64 ;;
	aarch64 | arm64) arch=arm64 ;;
	*) arch=unsupported ;;
	esac
	if [ "$os" = unsupported ] || [ "$arch" = unsupported ] || [ "$os/$arch" = freebsd/arm64 ]; then
		die "no prebuilt lazyaria for $(uname -s) $(uname -m). Build it from source:
  go install github.com/Pranjal-SB/lazyaria@latest"
	fi
}

# aria2_command prints the command that installs aria2 here, or nothing.
aria2_command() {
	if [ "$os" = darwin ]; then
		has brew && echo "brew install aria2"
		return 0
	fi
	if has apt-get; then
		echo "apt-get install -y aria2"
	elif has dnf; then
		echo "dnf install -y aria2"
	elif has pacman; then
		echo "pacman -S --noconfirm aria2"
	elif has zypper; then
		echo "zypper install -y aria2"
	elif has apk; then
		echo "apk add aria2"
	elif has pkg; then
		echo "pkg install -y aria2"
	fi
}

# ensure_aria2 asks before it installs aria2 (unless --with-aria2 or
# --no-aria2 decided already) and otherwise prints the command to run.
ensure_aria2() {
	has aria2c && return 0
	cmd=$(aria2_command)
	if [ -z "$cmd" ]; then
		say "lazyaria needs aria2. Install it with your package manager."
		return 0
	fi
	sudo=
	if [ "$os" != darwin ] && [ "$(id -u)" != 0 ]; then
		sudo=sudo
		if ! has sudo && has doas; then sudo=doas; fi
	fi
	say "lazyaria needs aria2, which is not installed."
	go=$aria2
	if [ "$go" = ask ]; then
		go=no
		# Piped into sh, stdin is the script, so the question uses the terminal.
		if (true </dev/tty) 2>/dev/null; then
			printf 'Install it now with "%s"? [y/N] ' "${sudo:+$sudo }$cmd" >/dev/tty
			read -r answer </dev/tty || answer=
			case $answer in [yY]*) go=yes ;; esac
		fi
	fi
	if [ "$go" = yes ]; then
		if [ "${cmd%% *}" = apt-get ]; then $sudo apt-get update -qq || true; fi
		# shellcheck disable=SC2086 # a fixed command line, split on purpose
		if $sudo $cmd; then return 0; fi
		warn "installing aria2 failed"
	fi
	say "Install aria2 with: ${sudo:+$sudo }$cmd"
}

receipt_file() { echo "${XDG_DATA_HOME:-$HOME/.local/share}/lazyaria/install.json"; }

uninstall() {
	receipt=$(receipt_file)
	if [ -z "${bin_dir_set:-}" ] && [ -f "$receipt" ]; then
		from=$(sed -n 's/.*"path": *"\(.*\)\/lazyaria".*/\1/p' "$receipt")
		[ -n "$from" ] && bin_dir=$from
	fi
	unit=${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user/lazyaria.service
	[ -f "$unit" ] && warn "a systemd user service is set up; run \"lazyaria service uninstall\" first"
	[ -e "$bin_dir/lazyaria" ] || warn "no lazyaria in $bin_dir"
	rm -f "$bin_dir/lazyaria" "$receipt"
	if [ -L "$bin_dir/laria" ] && [ "$(readlink "$bin_dir/laria")" = lazyaria ]; then
		rm -f "$bin_dir/laria"
	fi
	say "Removed lazyaria from $bin_dir."
	say "Your settings, job list and logs are still in:"
	say "  ${XDG_CONFIG_HOME:-$HOME/.config}/lazyaria"
	say "  ${XDG_STATE_HOME:-$HOME/.local/state}/lazyaria"
	say "Delete those folders to remove them too."
}

main() {
	version=${LAZYARIA_VERSION:-}
	bin_dir=${LAZYARIA_BIN_DIR:-}
	[ -n "$bin_dir" ] && bin_dir_set=1
	laria=yes
	[ "${LAZYARIA_NO_LARIA:-}" = 1 ] && laria=no
	aria2=ask
	action=install
	while [ $# -gt 0 ]; do
		case $1 in
		--version)
			[ $# -ge 2 ] || die "--version needs a value"
			version=$2
			shift
			;;
		--version=*) version=${1#*=} ;;
		--bin-dir)
			[ $# -ge 2 ] || die "--bin-dir needs a value"
			bin_dir=$2
			bin_dir_set=1
			shift
			;;
		--bin-dir=*)
			bin_dir=${1#*=}
			bin_dir_set=1
			;;
		--no-laria) laria=no ;;
		--with-aria2) aria2=yes ;;
		--no-aria2) aria2=no ;;
		--uninstall) action=uninstall ;;
		-h | --help)
			usage
			return 0
			;;
		*) die "unknown option $1 (see --help)" ;;
		esac
		shift
	done
	[ -n "${HOME:-}" ] || die "HOME is not set"
	bin_dir=${bin_dir:-$HOME/.local/bin}
	if [ "$action" = uninstall ]; then
		uninstall
		return 0
	fi

	detect_platform
	has tar || die "tar is needed to unpack lazyaria"
	version=${version#v}
	base=${LAZYARIA_BASE_URL:-https://github.com/Pranjal-SB/lazyaria/releases}
	proto="=https"
	[ -n "${LAZYARIA_BASE_URL:-}" ] && proto="=http,https"
	if [ -n "$version" ]; then url=$base/download/v$version; else url=$base/latest/download; fi
	asset=lazyaria_${os}_${arch}.tar.gz

	tmp=$(mktemp -d)
	trap 'rm -rf "$tmp"' EXIT
	say "Downloading $asset ${version:+(v$version) }..."
	fetch "$url/$asset" "$tmp/$asset" || die "could not download $url/$asset"
	fetch "$url/checksums.txt" "$tmp/checksums.txt" || die "could not download $url/checksums.txt"
	want=$(awk -v f="$asset" '$2 == f { print $1 }' "$tmp/checksums.txt")
	[ -n "$want" ] || die "$asset is not listed in checksums.txt"
	got=$(sha256 "$tmp/$asset")
	[ "$got" = "$want" ] || die "checksum mismatch for $asset (got $got, want $want); nothing was installed"
	tar -xzf "$tmp/$asset" -C "$tmp" lazyaria || die "could not unpack $asset"

	mkdir -p "$bin_dir"
	old=
	[ -x "$bin_dir/lazyaria" ] && old=$("$bin_dir/lazyaria" version 2>/dev/null | cut -d' ' -f2) || true
	cp "$tmp/lazyaria" "$bin_dir/.lazyaria.new"
	chmod 755 "$bin_dir/.lazyaria.new"
	# A rename in the same folder is atomic, even over a running binary.
	mv -f "$bin_dir/.lazyaria.new" "$bin_dir/lazyaria"
	new=$("$bin_dir/lazyaria" version | cut -d' ' -f2)
	if [ -n "$old" ] && [ "$old" != "$new" ]; then
		say "Upgraded lazyaria $old to $new in $bin_dir."
	else
		say "Installed lazyaria $new in $bin_dir."
	fi

	if [ "$laria" = yes ]; then
		if [ ! -e "$bin_dir/laria" ] && [ ! -L "$bin_dir/laria" ]; then
			ln -s lazyaria "$bin_dir/laria"
		elif [ "$(readlink "$bin_dir/laria" 2>/dev/null)" != lazyaria ]; then
			warn "$bin_dir/laria exists and is not ours, so it was left alone"
		fi
	fi

	receipt=$(receipt_file)
	mkdir -p "${receipt%/*}"
	esc=$(printf '%s' "$bin_dir/lazyaria" | sed 's/[\\"]/\\&/g')
	printf '{"source": "script", "path": "%s", "version": "%s"}\n' "$esc" "$new" >"$receipt"

	found=$(command -v lazyaria 2>/dev/null || true)
	case ":$PATH:" in
	*":$bin_dir:"*)
		if [ -n "$found" ] && [ "$found" != "$bin_dir/lazyaria" ]; then
			warn "$found comes first on your PATH, so \"lazyaria\" runs that one"
		fi
		;;
	*)
		say "$bin_dir is not on your PATH. Add this line to your shell's startup file:"
		say "  export PATH=\"$bin_dir:\$PATH\""
		;;
	esac

	ensure_aria2
	say "Run \"lazyaria doctor\" to check the setup."
}

main "$@"
