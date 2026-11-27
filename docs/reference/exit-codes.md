---
title: Exit codes
parent: Reference
nav_order: 2
---

# Exit codes

Every `lazyaria` command exits with one of these codes. Errors go to
standard error, as `lazyaria <command>: <message>`.

| Code | Name | When |
|---|---|---|
| 0 | OK | The command worked. Also: `add` skipped a duplicate because `reliability.duplicates` is `skip`; and `-h`, which prints a command's flags or usage. |
| 1 | Error | Something failed at run time. Examples: a download under `add --wait` failed or was removed; `doctor` found a failing check; `undo` came too late or got an unknown token; the connection to the daemon dropped during a command that changes something, or `add` could not send some links. |
| 2 | Usage | The command line was wrong, so nothing was done. Examples: an unknown command or flag, a missing argument, an ID prefix that matches no job or several, `add --paused --wait`, an `add` link the daemon cannot parse (such as a bad `%`-escape), a `refresh` link that is not an http(s), ftp or sftp link, or a `refresh` of a finished, removed or torrent job, an unknown setting or a bad value for `config set`, a bad value in a `LAZYARIA_*` variable or in the config file. |
| 3 | Daemon unreachable | No daemon answered and none could be started; or the socket belongs to another user; or the daemon and this client do not speak the same protocol version. |
| 4 | Refused | `add` refused: the link is already in the list, the file already exists, or the disk does not have room for it. |

When `add` gets several links and more than one thing goes wrong, it
exits with the highest code that applies.

Running `lazyaria` with no command prints the help and exits with 2.
`lazyaria help` prints the same and exits with 0.

## Examples

```text
$ lazyaria add nope
lazyaria add: "nope" is not a link or a .torrent file
$ echo $?
2
```

```text
$ lazyaria add https://example.com/files/ubuntu.iso
lazyaria add: "ubuntu.iso" is already in the list (done); add it with --force to download it again [existing job #1]
$ echo $?
4
```

See [Use lazyaria from scripts](../guides/use-from-scripts.md) for how to
use these codes.
