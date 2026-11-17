# Troubleshooting

## Start with doctor

```sh
lazyaria doctor
```

`doctor` checks aria2, the runtime folder, the socket, the daemon, the
database, the download folder and a few other things. Each failing line
(`✗`) is followed by a `fix:` line that says what to do. It does not start
the daemon. `lazyaria logs` shows the daemon's log.

The rest of this page lists the messages you are most likely to see.

## The daemon

**`cannot reach the lazyaria daemon; run `lazyaria doctor``** (exit code 3)

No daemon answered, and none could be started. The text after it gives
the reason. The usual ones:

- aria2 is missing or too old. The message then ends with
  `aria2c not found at "aria2c": install aria2 (...)`. Install aria2 1.35
  or newer, or point `engine.aria2c_path` at it.
- `daemon.auto_start` is `false` (or `LAZYARIA_DAEMON_AUTO_START=false`)
  and no daemon is running. Start one with `lazyaria daemon --detach`.
- A socket file is left over from a daemon that was killed. `doctor` says
  `no daemon is listening; remove the stale socket <path>`. Remove that
  file.

**`... is served by uid 1001, not you; refusing to use it`**

The socket belongs to another user. lazyaria never talks to another
user's daemon. Pick another `advanced.socket_path`, or find out why that
socket is in your folder.

**`socket dir ... must be a directory owned by you with mode 0700`**

The runtime folder has loose permissions. Run the `chmod 700` the message
shows, or remove the folder; the daemon creates it again.

**`the daemon restarted in the middle of this command, so it may or may not have taken effect`**

Check with `lazyaria list` before you run the command again.

## aria2

**`aria2c not found at "aria2c"`** or **`aria2c 1.34.0 is too old; need ≥ 1.35.0`**

Install or upgrade aria2 (see [Installation](../getting-started/installation.md)),
or set `engine.aria2c_path` to the binary you want.

**`doctor` shows `engine failed`**

aria2 kept crashing, and the daemon stopped restarting it. Look at
`lazyaria logs` and `aria2.log` in the state folder (see
[Files and paths](files-and-paths.md)). Once you fixed the cause, run:

```sh
lazyaria doctor --restart-engine
```

## Adding downloads

**`"ubuntu.iso" is already in the list (done); add it with --force to download it again`** (exit code 4)

The link is already a job. Use `--force` to download it again under a new
name. See [Files and safety](../concepts/files-and-safety.md).

**`.../notes.pdf already exists; add it with --force to download it again, or choose another name`** (exit code 4)

A file with that name is in the folder. Use `--force` for a numbered name,
or `--out` to choose one.

**`not enough free space in /home/you/Downloads: need ... bytes, ... free`** (exit code 4)

Free some space, or use `--dir` to download somewhere else.

**`"nope" is not a link or a .torrent file`** (exit code 2)

`add` takes links with a scheme (`https://...`, `ftp://...`,
`magnet:...`), paths to `.torrent` files, or `-` to read links from
standard input.

## IDs

**`id prefix "01M7" matches 4 jobs, give more characters`**

IDs that were created close together start the same. Give more of the ID,
as shown by `lazyaria list`.

**`no job id starts with "..."`**

The job does not exist, or it was removed. Removed jobs are not listed.

## Failed downloads

A failed job has the state `error` in `lazyaria list`, with aria2's message
in brackets. `lazyaria list --json` adds a short `error_kind`.

| What you see | What it means | What to do |
|---|---|---|
| `status=403` or `status=410`, kind `link-expired` | The link stopped working. | Get a new link and run `lazyaria refresh`. See [Refresh an expired link](../guides/refresh-an-expired-link.md). |
| `Reached max-file-not-found count=3`, kind `not-found` | The server says the file does not exist. | Check the link. |
| kind `rate-limited` | The server said "too many requests" (HTTP 429). | Nothing: lazyaria retries later by itself, a few times. |
| `Invalid range header` | The server ignored the request to continue from the middle, so the download cannot resume. | Remove the job with `--delete-files` and add the link again, or use another server. |
| kind `disk-full` | The disk filled up. lazyaria paused the other downloads to the same folder. | Free some space, then `lazyaria resume` them. |
| `... is missing or on a different drive; mount it and retry`, kind `dir-missing` | The drive the download was writing to is not mounted. | Mount it, then `lazyaria resume` the job. |

`lazyaria resume` on a failed job tries it again from where it stopped.

## Remove and undo

**`[-32098] nothing to undo: the undo window has passed`** (exit code 1)

Undo works for 10 seconds after a remove (`remove.undo_seconds`). Files
moved to the trash are still in the trash; restore them from there.

**`no trash is available for ...; the files are untouched and the job was kept`**

lazyaria found no trash folder it can use on that disk, and it never
deletes files for real. Run `remove` without `--delete-files`, then move or
delete the file yourself.

**`... already exists; move it aside and retry (the trashed copy is ...)`**

Undo cannot put a file back because something else now has its name. Move
that file away and run the same undo command again, within the window.

## Settings

**`unknown setting "..." (run `lazyaria config list` to see all keys)`**

Check the spelling against [Settings](settings.md).

**`downloads.max_concurrent: invalid value 0, allowed 1 or more; default "5"`**

The value is out of range or of the wrong type. The message names the
allowed values and the default.

**`downloads.max_concurrent from LAZYARIA_DOWNLOADS_MAX_CONCURRENT: expected a whole number`**

An environment variable holds a bad value. Every command fails until you
fix or unset it.

**A setting I changed has no effect**

Settings marked *restart* in [Settings](settings.md) take effect the next
time the daemon starts. `lazyaria config set` says so when it saves one.
If you edited `config.toml` with your own editor, see
[The daemon](../concepts/the-daemon.md#settings-and-the-daemon).

## Other

**`list` shows old progress**

When no client is watching closely, the daemon asks aria2 for progress
every 15 seconds (`advanced.sentinel_interval`). The `DONE` column of
`lazyaria list` can be that far behind.

**The machine went to sleep during a download**

`doctor` shows whether logind is there. Without systemd-logind or
elogind, lazyaria cannot keep the machine awake.

**`no log at ... yet`**

The daemon has not run yet. If it runs as a systemd service, its log is in
the journal: `journalctl --user -u lazyaria`.
