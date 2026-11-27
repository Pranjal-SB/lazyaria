# Installation

lazyaria is pre-release. Install it with one command:

```sh
curl -fsSL https://pranjal-sb.github.io/lazyaria/install.sh | sh
```

The script downloads the release for your system from GitHub, checks it
against the release's `checksums.txt`, and puts `lazyaria` and the short
name `laria` in `~/.local/bin`. It needs no root. If aria2 is missing, it
asks whether to install it with your package manager (with `sudo`), and
otherwise prints the command to run. It never edits your shell's startup
files: when `~/.local/bin` is not on your `PATH`, it prints the line to
add. There are builds for Linux (amd64, arm64), macOS (amd64, arm64) and
FreeBSD (amd64).

Options go after `sh -s --`:

```sh
curl -fsSL https://pranjal-sb.github.io/lazyaria/install.sh | sh -s -- --version 0.1.0
```

| Option | What it does |
|---|---|
| `--version X` | install version X, not the latest (`LAZYARIA_VERSION`) |
| `--bin-dir DIR` | install into DIR, not `~/.local/bin` (`LAZYARIA_BIN_DIR`) |
| `--no-laria` | do not add `laria` (`LAZYARIA_NO_LARIA=1`) |
| `--with-aria2` | install aria2 if it is missing, without asking |
| `--no-aria2` | never install aria2 and never ask |
| `--uninstall` | remove `lazyaria`, `laria` and the install record |
| `--help` | list the options |

An existing `laria` that is not a link to lazyaria is left alone. To
upgrade, run the same command again. `--uninstall` keeps your settings,
job list and logs, and prints where they are.

If the docs site is down, the same script is in the repository:

```sh
curl -fsSL https://raw.githubusercontent.com/Pranjal-SB/lazyaria/master/install.sh | sh
```

### Other ways to install

- **mise:** `mise use -g github:Pranjal-SB/lazyaria`
- **Go:** `go install github.com/Pranjal-SB/lazyaria@latest` (needs Go
  1.27 or newer; it reports the version it was installed at)
- **By hand** from a release, or **from source**: see below.

These do not add `laria` or check for aria2. Add the link yourself if you
want it (`ln -s lazyaria "$(dirname "$(command -v lazyaria)")/laria"`),
and install aria2 as shown next.

## What you need

- **Linux.** lazyaria is developed and tested on Linux. It also compiles
  for macOS and FreeBSD, but nobody tests it there yet. Windows is not
  supported.
- **Go 1.27 or newer**, only to build from source or use `go install`.
- **aria2 1.35 or newer.** aria2 is the program that does the downloading
  (see [The engine](../concepts/the-engine.md)). lazyaria is developed
  against aria2 1.37. It looks for `aria2c` on your `PATH`.

Install aria2 with your package manager:

| System | Command |
|---|---|
| Arch | `sudo pacman -S aria2` |
| Debian, Ubuntu | `sudo apt install aria2` |
| Fedora | `sudo dnf install aria2` |
| openSUSE | `sudo zypper install aria2` |
| Alpine | `sudo apk add aria2` |
| macOS (Homebrew) | `brew install aria2` |
| FreeBSD | `sudo pkg install aria2` |

Check the version:

```sh
aria2c --version | head -1
```

```text
aria2 version 1.37.0
```

## Install from a release

Releases have binaries for Linux (amd64, arm64), macOS (amd64, arm64) and
FreeBSD (amd64). Download the tarball for your platform and `checksums.txt`
from the [releases page](https://github.com/Pranjal-SB/lazyaria/releases).
The tarball is named `lazyaria_<os>_<arch>.tar.gz`, for example
`lazyaria_linux_amd64.tar.gz`.

Verify it and unpack it:

```sh
sha256sum --ignore-missing -c checksums.txt
tar xzf lazyaria_linux_amd64.tar.gz lazyaria
```

On macOS use `shasum -a 256 -c checksums.txt` in place of `sha256sum`. On
FreeBSD, compare the output of `sha256 <file>` with `checksums.txt`.

Put the binary on your `PATH` and, if you like, add the short name:

```sh
install -D -m 755 lazyaria ~/.local/bin/lazyaria
ln -s lazyaria ~/.local/bin/laria
```

aria2 must be installed too (see above). A release binary reports the
release version from `lazyaria version`.

## Build from source

```sh
git clone https://github.com/Pranjal-SB/lazyaria.git
cd lazyaria
go build -o lazyaria .
```

Put the binary somewhere on your `PATH`, for example:

```sh
install -D -m 755 lazyaria ~/.local/bin/lazyaria
```

If you like a shorter name, add `laria` as a link to the same binary. It
works the same, and its messages say `laria`:

```sh
ln -s lazyaria ~/.local/bin/laria
```

Check that it runs:

```sh
lazyaria version
```

```text
lazyaria dev (protocol 1)
```

A build from source reports its version as `dev`.

## Check your setup

`lazyaria doctor` checks aria2, the folders lazyaria uses, the download
directory and a few other things. Each line that fails comes with a fix.

```sh
lazyaria doctor
```

```text
✓ aria2c        aria2 1.37.0 at /usr/bin/aria2c
✓ runtime dir   /run/user/1000/lazyaria
✓ socket        no socket at /run/user/1000/lazyaria/lazyaria.sock (daemon not running)
✓ daemon        not running; it starts when a client needs it
✓ database      not created yet; the daemon creates /home/you/.local/state/lazyaria/lazyaria.db
✓ download dir  /home/you/Downloads, writable, 120.4 GiB free, ext4
...
```

`doctor` exits with 1 when any check fails, so you can use it in a script.

There is nothing to start by hand. The first command that needs the
background process starts it. See [The daemon](../concepts/the-daemon.md).

## Next

[Your first download](first-download.md), or run `lazyaria` with no
command to open [the terminal interface](../guides/terminal-interface.md).
