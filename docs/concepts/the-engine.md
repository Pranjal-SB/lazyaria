---
title: The engine
parent: Concepts
nav_order: 3
---

# The engine

lazyaria does not download anything itself. The downloading is done by
[aria2](https://aria2.github.io/), a free, long-standing command-line
download tool. In these pages, aria2 is called the engine.

## Why aria2

aria2 already handles the hard parts, and has done so for years:

- HTTP, HTTPS, FTP and SFTP;
- BitTorrent and magnet links;
- several connections to one server, each fetching a different piece of
  the file;
- resuming an unfinished download from its `.aria2` control file.

lazyaria adds what aria2 leaves to you: a job list that survives
restarts, file naming and removal, clear errors, and a process that looks
after itself.

## How lazyaria runs aria2

The [daemon](the-daemon.md) starts `aria2c` as its own child process and
stops it when it exits. You do not run or configure aria2 yourself.

- **Control.** The daemon talks to aria2 over aria2's remote-control
  interface (JSON-RPC over a WebSocket). That interface listens only on
  the local machine, on a free port, and needs a random secret that is
  created at each start. The secret is in a config file only your user
  can read, never on the command line where other users could see it.
- **Settings.** The daemon writes aria2's options from lazyaria's
  settings, such as `downloads.max_concurrent`, `downloads.split` and
  `limits.global_down`. Settings marked *now* in the
  [settings reference](../reference/settings.md) are passed to the running
  aria2 at once; the others need a daemon restart.
- **The job list.** lazyaria keeps the list in its own database, not in
  an aria2 session file. When aria2 starts again, the daemon hands it the
  unfinished jobs.
- **Crashes.** If aria2 exits unexpectedly, the daemon starts it again,
  waiting a little longer each time. If it keeps exiting
  (`engine.crash_loop_exits` times within `engine.crash_loop_window`),
  the daemon gives up and marks the engine as failed. `lazyaria doctor`
  then shows it, and `lazyaria doctor --restart-engine` tries again.
- **Log.** aria2 writes its own log to `aria2.log`, next to lazyaria's log
  (see [Files and paths](../reference/files-and-paths.md)). When aria2
  starts and the file is over 4 MiB, it is emptied first.

## What lazyaria changes

- **File names.** aria2 runs with overwriting turned off and automatic
  renaming turned on, and lazyaria checks names itself before it adds a
  download.
- **Where aria2 writes.** aria2 writes each download into its own hidden
  folder, `.laria/<job id>/` in the download folder, and lazyaria moves
  the finished download out of it. See [Where files go](files.md).
- **Errors.** lazyaria turns aria2's error numbers into short kinds such as
  `link-expired`, `not-found`, `rate-limited` or `disk-full`.
  `lazyaria list --json` shows them in `error_kind`.
- **Retries.** aria2 retries each connection a few times
  (`retry.max_tries`). On top of that, lazyaria retries a whole download
  later for the failure kinds in `retry.auto_retry`, waiting longer each
  time (`retry.backoff_base`, `retry.backoff_factor`). By default it does
  this only when the server says it is busy (HTTP 429, "too many
  requests").

## What lazyaria does not change

- **Download speed.** lazyaria uses aria2's own transfer code. A download
  is as fast or as slow as it would be in plain aria2 with the same
  options. aria2 allows at most 16 connections per server.
- **Protocols.** lazyaria supports what aria2 supports, and nothing more.
  For example, aria2 does not speak HTTP/2 or HTTP/3, and it cannot read
  video pages; it needs a direct link to the file.

## Passing other options to aria2

`engine.aria2_extra_args` passes extra options straight to aria2, in its
long form:

```toml
[engine]
aria2_extra_args = ["--max-overall-download-limit=0"]
```

lazyaria refuses options that would break its control of aria2: every
`rpc-*` option, and the options that would let aria2 overwrite files or
remove its control files.

To use another aria2 binary, set `engine.aria2c_path`.
