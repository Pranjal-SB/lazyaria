---
title: Use lazyaria from scripts
parent: Guides
nav_order: 3
---

# Use lazyaria from scripts

Every lazyaria command works without a terminal, prints plain lines, and
says what happened through its exit code. This page shows the parts that
matter for scripts.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | It worked. |
| 1 | Something failed, for example a download under `--wait`. |
| 2 | The command line was wrong. Nothing was done. |
| 3 | lazyaria could not reach or start its daemon. |
| 4 | lazyaria refused: a duplicate, a file that exists, or not enough disk space. |

When `add` gets several links and more than one thing goes wrong, it exits
with the highest code. The full list is in [Exit codes](../reference/exit-codes.md).

## Wait until downloads finish

`add --wait` returns when every link it added is finished:

```sh
lazyaria add --wait https://example.com/a.bin https://example.com/b.bin
```

```text
added #4 a.bin
added #5 b.bin
done #4 a.bin
done #5 b.bin
```

- It exits with 0 when all of them finished.
- It exits with 1 when one failed or was removed. The reason goes to
  standard error:

  ```text
  lazyaria add: #8 e.bin failed: Reached max-file-not-found count=3
  ```

- If lazyaria pauses a download because the disk is full, `--wait` stops
  waiting, says so, and exits with 1.
- If you pause one of the downloads yourself, `--wait` keeps waiting until
  you resume it.
- `--wait` and `--paused` cannot be combined, because a paused download
  never finishes.

Stopping the script (for example with Ctrl+C) only stops the waiting. The
downloads carry on in the daemon.

## Read links from standard input

Give `-` instead of a link, and `add` reads links from standard input. It
picks out everything that looks like a link and ignores the rest, so you
can feed it a text file or a web page:

```sh
lazyaria add --wait - < links.txt
```

```sh
printf 'see https://example.com/a.bin and\nhttps://example.com/b.bin\n' | lazyaria add -
```

If standard input holds no link at all, `add` exits with 2.

## Read the state as JSON

`list`, `status`, `doctor` and `config list` take `--json`. The output is
one JSON document:

```sh
lazyaria list --all --json | jq -r '.jobs[] | select(.state == "error") | "#\(.num) \(.name)"'
```

```text
#8 e.bin
```

Each job has `num`, the number the other commands take, and `id`, a long
ID that never changes either.

A job in the list is `queued`, `active`, `paused`, `retry-wait` (waiting
to try again after a temporary error), `seeding` (a finished torrent that
is still uploading), `done` or `error`. `list` without `--all` leaves out
`done` jobs. Removed jobs are not listed.

`doctor --json` prints one object per check, with `check`, `ok`,
`detail` and `fix`, and exits with 1 when a check failed:

```sh
lazyaria doctor --json | jq -r '.[] | select(.ok | not) | .check + ": " + .fix'
```

## Duplicates in scripts

By default, adding a link that is already in the list exits with 4. That
stops a script that runs twice from downloading everything twice. To
change it:

- `--force` downloads it again, under a new file name.
- `lazyaria config set reliability.duplicates skip` makes `add` print
  `skipped ...` and exit with 0 instead. With `--wait`, a skipped link
  counts as finished once its existing job is.

See [Where files go](../concepts/files.md).

## If the connection drops

If the daemon goes away while a command runs (it crashed, or someone
stopped it), a command that only reads, such as `list`, is sent again
once the daemon is back. A command that changes something is not sent
again, because it may already have taken effect. It exits with 1 and
says:

```text
lazyaria add: https://example.com/a.bin: the connection to the daemon dropped during this command, so it may or may not have taken effect; check with `lazyaria list` (or `lazyaria config list`) before running it again
```

For `add` with several links:

- The message starts with the link that was cut off. Check that one with
  `lazyaria list` before you add it again.
- If the daemon comes back, the links after it are still sent.
- If it does not, `add` stops and names the links it did not send:

  ```text
  lazyaria add: not sent, add them again: https://example.com/b.bin https://example.com/c.bin
  ```

- `add --wait` stops at the first cut-off link, because it cannot keep
  waiting without its connection. It names the links it did not send in
  the same way.

## Do not start the daemon

A script that should only talk to a daemon that is already running can
turn off the automatic start:

```sh
LAZYARIA_DAEMON_AUTO_START=false lazyaria list
```

If no daemon is running, this exits with 3:

```text
lazyaria list: cannot reach the lazyaria daemon; run `lazyaria doctor` (dial unix /run/user/1000/lazyaria/lazyaria.sock: connect: no such file or directory)
```
