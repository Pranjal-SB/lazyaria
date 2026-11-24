# lazyaria

A download manager for the terminal, built on aria2.

> **Status: pre-release.** lazyaria is not ready for use yet. The command
> line described here works, but commands and settings may still change.

<!-- demo: added when the TUI ships -->

## Highlights

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
lazyaria remove 01M7MCXK
```

Wait for a download in a script; the exit code is 0 when it finished and
1 when it failed:

```sh
lazyaria add --wait https://example.com/files/notes.pdf
```

More in [Your first download](docs/getting-started/first-download.md).

## Coming next

- the terminal interface (TUI), with mouse support;
- browser capture;
- video downloads through yt-dlp.

## Documentation

The [documentation](docs/README.md) has four parts:

- [Getting started](docs/README.md#getting-started): installation and a
  first download.
- [Guides](docs/README.md#guides): refresh an expired link, use lazyaria
  from scripts.
- [Concepts](docs/README.md#concepts): the daemon, where files go, the
  engine, and [how lazyaria compares](docs/comparison.md) with other
  download managers.
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
