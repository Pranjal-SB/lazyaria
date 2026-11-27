---
title: Files and paths
parent: Reference
nav_order: 3
---

# Files and paths

lazyaria follows the XDG base directory rules: it puts each kind of file
where the matching `XDG_*` variable points, and uses the usual default
when the variable is not set (or is not an absolute path).

`lazyaria config path` prints where the config file is. `lazyaria doctor`
prints the other locations it checks.

## Config

| File | Default location |
|---|---|
| Config file | `$XDG_CONFIG_HOME/lazyaria/config.toml`, by default `~/.config/lazyaria/config.toml` |

The file is optional. Without it, every setting has its default. See
[Settings](settings.md).

Values saved with `lazyaria config set` are not written to this file. They
are kept in the database, and `lazyaria config reset` removes them.

## State

The state folder is `$XDG_STATE_HOME/lazyaria`, by default
`~/.local/state/lazyaria`.

| File | What it is |
|---|---|
| `lazyaria.db` | The job list and the values saved with `config set` (SQLite). |
| `lazyaria.log` | The daemon's log, rotated at `log.max_size`. `log.file` moves it. |
| `aria2.log` | aria2's own log. Emptied when aria2 starts if it is over 4 MiB. |
| `dht.dat` | The BitTorrent DHT cache, kept between runs. It appears once you download a torrent. |

## Runtime

Files that only exist while the daemon runs go in the runtime folder:
`$XDG_RUNTIME_DIR/lazyaria`, usually `/run/user/<your user id>/lazyaria`.

| File | What it is |
|---|---|
| `lazyaria.sock` | The socket the commands use to talk to the daemon. Mode 0600. |
| `daemon.lock` | Makes sure only one daemon runs. Holds its process ID. |
| `aria2.conf` | aria2's options, written by the daemon at each start. Mode 0600, as it holds aria2's secret. |

The folder has mode 0700, so only you can open it.

If `XDG_RUNTIME_DIR` is not set, the runtime folder is
`~/.local/state/lazyaria/run`. If the socket path would be too long for the
system (longer than 103 bytes), the folder is `/tmp/lazyaria-<your user id>`
instead, and lazyaria checks that it is yours and has mode 0700.

The `advanced.socket_path` setting puts the socket somewhere else. The
other runtime files stay in the runtime folder.

## Downloads

Downloads go to the first of these that is set:

1. the `--dir` flag of `lazyaria add`;
2. the `downloads.dir` setting;
3. `XDG_DOWNLOAD_DIR` in `$XDG_CONFIG_HOME/user-dirs.dirs` (by default
   `~/.config/user-dirs.dirs`), the download folder
   your desktop uses;
4. `~/Downloads`.

Inside the download folder:

| Path | What it is |
|---|---|
| `.laria/<job id>/` | A download that is not finished yet, with aria2's `.aria2` control file. Removed when the download is moved into the download folder. |
| `<name>.laria` | A marker for an unfinished download: a symbolic link to its file in `.laria`. Removed when the download finishes. `downloads.marker` turns markers off. |
| `.<name>.laria-copy` | A copy in progress, when a finished download has to be copied into the download folder instead of moved. |

See [Where files go](../concepts/files.md#unfinished-downloads).

## Trash

`lazyaria remove --delete-files` moves files to the trash:

- `$XDG_DATA_HOME/Trash`, by default `~/.local/share/Trash`, when the file
  is on the same disk as that folder;
- otherwise `.Trash-<your user id>` at the top of the file's disk.

See [Where files go](../concepts/files.md).

## systemd user service

`lazyaria service install` writes
`~/.config/systemd/user/lazyaria.service`. `lazyaria service uninstall`
removes it.
