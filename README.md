# lazyaria

A download manager for the terminal, built on aria2.

> **Status: pre-release.** lazyaria is not ready for use yet. The terminal
> interface and the command line described here work, but commands, keys
> and settings may still change.

<!-- demo: a recording of the terminal interface goes here -->

## Highlights

- **A terminal interface and a command line over the same downloads.**
  `lazyaria` opens a live list with tabs, speeds, a piece map and mouse
  support; every command works next to it, from any terminal or script.
- **Nothing runs when nothing downloads.** The background daemon starts
  with the first command that needs it and exits about 30 seconds after
  the last download ends. There is no service to set up.
- **Unfinished downloads stay out of the way.** A download runs in a
  hidden `.laria` folder and appears in the download folder when it is
  finished. Meanwhile a `name.laria` link points at the unfinished file.
- **Refresh an expired link.** When a download link stops working halfway,
  `lazyaria refresh` gives the download a new link and keeps the part it
  already has.
- **Survives crashes and restarts.** The job list lives in a database. If
  aria2 crashes, the daemon starts it again and hands back the unfinished
  downloads; if the daemon stops, the next command picks them up.
- **Made for scripts.** Clear exit codes, `add --wait`, links from
  standard input, and `--json` output.
- **One tool for HTTP, HTTPS, FTP, SFTP, torrents and magnet links,**
  through aria2.
- **`lazyaria doctor`** checks the setup and prints a fix for each problem
  it finds.

## Install

```sh
curl -fsSL https://pranjal-sb.github.io/lazyaria/install.sh | sh
```

The script installs `lazyaria` and the short name `laria` to
`~/.local/bin`, checks the download's checksum, and offers to install
aria2 (1.35 or newer) if it is missing. There are builds for Linux, macOS
and FreeBSD; lazyaria is tested on Linux. [Installation](docs/getting-started/installation.md) has its
options and the other ways: mise, `go install`, a release by hand, or
from source.

## Quick start

Run `lazyaria` with no command to open the terminal interface. Paste a
link, or press `a`, to add a download. Quitting leaves the downloads
running. See [The terminal interface](docs/guides/terminal-interface.md).

Give `laria` a link, a magnet or a `.torrent` file to download it:

```sh
laria https://example.com/files/ubuntu.iso
```

The same works with the full commands:

```sh
lazyaria add https://example.com/files/ubuntu.iso
```

```text
added #1 ubuntu.iso
```

```sh
lazyaria list
```

```text
#   STATE   DONE  SIZE  NAME
#1  active  -     -     ubuntu.iso
```

Pause, resume and remove take the job number:

```sh
lazyaria pause 1
lazyaria resume 1
lazyaria remove 1
```

Wait for a download in a script; the exit code is 0 when it finished and
1 when it failed:

```sh
lazyaria add --wait https://example.com/files/notes.pdf
```

More in [Your first download](docs/getting-started/first-download.md).

## Coming next

- browser capture;
- video downloads through yt-dlp.

## Documentation

The [documentation](docs/README.md) has four parts:

- [Getting started](docs/README.md#getting-started): installation and a
  first download.
- [Guides](docs/README.md#guides): the terminal interface, refresh an
  expired link, use lazyaria from scripts.
- [Concepts](docs/README.md#concepts): the daemon, where files go, the
  engine, and [how lazyaria compares](docs/comparison.md) with other
  download managers.
- [Reference](docs/README.md#reference): [commands](docs/reference/commands.md),
  [keys](docs/reference/keys.md), [settings](docs/reference/settings.md),
  [exit codes](docs/reference/exit-codes.md),
  [files and paths](docs/reference/files-and-paths.md) and
  [troubleshooting](docs/reference/troubleshooting.md).

`lazyaria help` lists every command.

## Contributing

lazyaria is pre-release, so expect things to move. Bug reports and ideas
are welcome as issues. Please open an issue before a large change. See
[CONTRIBUTING.md](CONTRIBUTING.md). To report a security problem, see
[SECURITY.md](SECURITY.md).

To work on the code:

```sh
go vet ./...
go test -race ./...
go test -race -tags e2e ./e2e/    # end-to-end tests; need aria2c
```

The command, key and settings reference pages are generated from the
code. After you change a command's help, a flag, a key or a setting, run:

```sh
go generate .
```

A test fails when the generated pages are out of date.

## Licence

lazyaria is licensed under the [Apache License 2.0](LICENSE). See
[NOTICE](NOTICE).
