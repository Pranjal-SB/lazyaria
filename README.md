# lazyaria

A download manager for the terminal, built on aria2, that never writes
over your files.

> **Status: pre-release.** lazyaria is not ready for use yet. The command
> line described here works, but commands and settings may still change,
> and the terminal interface (TUI) is not released.

<!-- demo: added when the TUI ships -->

## Highlights

These work today:

- **Nothing runs when nothing downloads.** A background daemon starts with
  the first command that needs it and exits about 30 seconds after the last
  download ends.
- **Never overwrites a file.** A link that is already in the list, or a
  file that already exists, is refused. `--force` downloads it again under
  a new name such as `ubuntu.1.iso`.
- **Removing files goes to the trash, with undo.** `remove --delete-files`
  moves only the files the job downloaded to your desktop's trash, and
  `remove` prints an `undo` command that brings back the job and its
  files.
- **Refresh an expired link.** `lazyaria refresh` gives a failed or
  running download a new link and keeps the part it already has.
- **Survives restarts and crashes.** The job list lives in a database.
  If aria2 crashes, the daemon starts it again and hands back the
  unfinished downloads.
- **Made for scripts.** Clear exit codes, `add --wait`, links from
  standard input, and `--json` output.
- **HTTP, HTTPS, FTP, SFTP, BitTorrent and magnet links,** through aria2.
- **Tells you how to fix things.** `lazyaria doctor` checks the setup and
  prints a fix for each problem it finds.

## Install

lazyaria builds from source for now. You need Linux, Go 1.27 or newer, and
aria2 1.35 or newer (developed against 1.37).

```sh
sudo apt install aria2        # or pacman, dnf, zypper, apk, brew
git clone https://github.com/Pranjal-SB/lazyaria.git
cd lazyaria
go build -o lazyaria .
install -D -m 755 lazyaria ~/.local/bin/lazyaria
lazyaria doctor
```

See [Installation](docs/getting-started/installation.md) for details.

## Quick start

```sh
lazyaria add https://example.com/files/ubuntu.iso
```

```text
added 01M7MCXK5GYY1BXGRJA7MD9R97 ubuntu.iso
```

```sh
lazyaria list
```

```text
ID        STATE   DONE  SIZE  NAME
01M7MCXK  active  -     -     ubuntu.iso
```

Pause, resume and remove take the ID, or its first characters:

```sh
lazyaria pause 01M7MCXK
lazyaria resume 01M7MCXK
lazyaria remove --delete-files 01M7MCXK
```

```text
removed 01M7MCXK5GYY1BXGRJA7MD9R97
undo: lazyaria undo KJY3SB2H4I2VYVUFLL4DC3QIXK
```

Wait for a download in a script; the exit code is 0 when it finished and
1 when it failed:

```sh
lazyaria add --wait https://example.com/files/notes.pdf
```

More in [Your first download](docs/getting-started/first-download.md).

## How it compares

lazyaria is young, and other tools do a lot that it does not. This is an
honest summary at the time of writing.

- **Plain `aria2c`.** lazyaria runs aria2 for you, so the downloading
  itself is the same. On its own, aria2c is lighter and gives you every
  option, but you look after its session file, its remote-control
  interface and its file naming yourself. If you only need one download
  now and then, `aria2c <link>` is enough.
- **Motrix.** A graphical desktop app, also built on aria2. Choose it if
  you want windows and buttons. lazyaria is for the terminal and for
  machines without a desktop.
- **Internet Download Manager (IDM).** A paid Windows app with its own
  engine, deep browser integration and video capture. lazyaria runs on
  Linux, has no browser integration and cannot download from video pages.
- **Surge.** A terminal download manager written in Go, with its own HTTP
  engine, a terminal interface you can use today, and a browser extension.
  lazyaria has no terminal interface yet. It adds torrents and magnet
  links through aria2, and the safety rules above.

What lazyaria does not do today:

- no terminal interface (TUI) yet, only the command line;
- no browser integration, and no downloads from video sites;
- no graphical app, and no Windows support;
- no packages or prebuilt binaries;
- no control over the network: the daemon only listens on a local socket
  that only your user can open.

lazyaria makes no claim about download speed. It uses aria2's transfer
code, with the same limits, such as at most 16 connections per server.

## Roadmap

Planned, not built yet:

- the terminal interface (TUI), with mouse support;
- a separate folder for unfinished downloads, so a partial file never sits
  under its final name;
- browser capture;
- video downloads through yt-dlp.

## Documentation

The [documentation](docs/README.md) has four parts:

- [Getting started](docs/README.md#getting-started): installation and a
  first download.
- [Guides](docs/README.md#guides): refresh an expired link, use lazyaria
  from scripts.
- [Concepts](docs/README.md#concepts): the daemon, files and safety, the
  engine.
- [Reference](docs/README.md#reference): [commands](docs/reference/commands.md),
  [settings](docs/reference/settings.md),
  [exit codes](docs/reference/exit-codes.md),
  [files and paths](docs/reference/files-and-paths.md) and
  [troubleshooting](docs/reference/troubleshooting.md).

`lazyaria help` lists every command.

## Contributing

lazyaria is pre-release, so expect things to move. Bug reports and ideas
are welcome as issues. Please open an issue before a large change.

To work on the code:

```sh
go vet ./...
go test -race ./...
go test -race -tags e2e ./e2e/    # end-to-end tests; need aria2c
```

The command and settings reference pages are generated from the code.
After you change a command's help, a flag or a setting, run:

```sh
go generate .
```

A test fails when the generated pages are out of date.

## Licence

lazyaria is licensed under the [Apache License 2.0](LICENSE). See
[NOTICE](NOTICE).
