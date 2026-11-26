# Changelog

All notable changes to this project are listed here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

First public work. lazyaria is pre-release, so commands, keys and settings
may still change.

### Added

- A background daemon that supervises aria2. It starts with the first
  command that needs it, exits about 30 seconds after the last download
  ends, and restarts aria2 if it crashes.
- A job list stored in SQLite, with job numbers (`#1`, `#2`, ...) used by
  every command. Unfinished downloads resume after a crash or restart.
- Command line: `add` (with `--wait`, links from standard input and
  `--json` output), `list`, `pause`, `resume`, `remove`, `refresh`,
  `doctor`, `version` and `help`, with documented exit codes.
- A terminal interface, opened by running `lazyaria` with no command: live
  list with tabs, speeds, a piece map and mouse support.
- A staging folder: a download runs in a hidden `.laria` folder, moves to
  the download folder when finished, and leaves a `name.laria` link to the
  unfinished file meanwhile.
- A trash for removed downloads, with undo.
- `lazyaria refresh` to give a download a new link and keep the part it
  already has.
- Downloads over HTTP, HTTPS, FTP, SFTP, torrents and magnet links,
  through aria2.
- Prebuilt release binaries for Linux, macOS and FreeBSD.
