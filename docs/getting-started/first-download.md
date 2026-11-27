---
title: Your first download
parent: Getting started
nav_order: 2
---

# Your first download

This page walks through the everyday commands. The links are examples:
use any HTTP, HTTPS or FTP link you like. If you would rather see your
downloads on one screen, run `lazyaria` with no command; see
[The terminal interface](../guides/terminal-interface.md).

## Add a download

```sh
lazyaria add https://example.com/files/ubuntu.iso
```

```text
added #1 ubuntu.iso
```

`add` returns at once. The download runs in the background, in the
lazyaria daemon, so you can close the terminal. Each download in the list
is called a job, and `#1` is its number. Other commands take the number,
with or without the `#`. Numbers are never reused.

The file goes to your download directory: `~/Downloads`, or the folder
your desktop set as its download folder. While it downloads, it is kept in
a hidden `.laria` folder there, and `ubuntu.iso.laria` shows that it is
on its way; the file itself appears when it is finished. Use `--dir` to
pick another folder, and `--out` to pick the file name:

```sh
lazyaria add --dir ~/isos --out ubuntu-24.04.iso https://example.com/files/ubuntu.iso
```

`add` also takes several links at once, a `.torrent` file, or a magnet
link.

## See what is running

```sh
lazyaria list
```

```text
#   STATE   DONE  SIZE       NAME
#3  active  6%    190.7 MiB  big.img
```

`list` shows the jobs that are not finished. Add `--all` to include the
finished ones:

```sh
lazyaria list --all
```

```text
#   STATE  DONE  SIZE      NAME
#2  done   100%  48.8 KiB  notes.pdf
#1  done   100%  2.9 MiB   ubuntu.iso
```

Each job also has a long ID, shown by `list --json`. Commands accept its
first characters too, as long as they match only one job.

## Wait for a download

`--wait` keeps `add` running until the download is finished:

```sh
lazyaria add --wait https://example.com/files/notes.pdf
```

```text
added #2 notes.pdf
done #2 notes.pdf
```

It exits with 0 when every download finished and 1 when one failed. This
is useful in scripts; see [Use lazyaria from scripts](../guides/use-from-scripts.md).

## Pause and resume

```sh
lazyaria pause 3
```

```text
paused #3
```

```sh
lazyaria resume 3
```

```text
resumed #3
```

A resumed download carries on from where it stopped, as long as the
server supports it. Paused jobs stay paused when the daemon stops and
starts again.

## When a download fails

A failed job shows the state `error` and the reason in brackets:

```text
#   STATE   DONE  SIZE  NAME
#4  error   -     -     later.bin  (Reached max-file-not-found count=3)
```

`lazyaria resume` tries a failed job again, from where it stopped. If the
link itself expired, give the job a new one with `lazyaria refresh`; see
[Refresh an expired link](../guides/refresh-an-expired-link.md). Other
errors are explained in [Troubleshooting](../reference/troubleshooting.md).

## Remove a download

```sh
lazyaria remove 3
```

```text
removed #3
undo: lazyaria undo KJY3SB2H4I2VYVUFLL4DC3QIXK
```

This takes the job off the list and keeps its file. A finished file stays
where it is. An unfinished one is moved into the download folder with
`(unfinished)` before its extension, such as `movie (unfinished).mkv`.
aria2's `.aria2` control file for it goes to the trash, where undo finds
it.

To remove the file too, add `--delete-files`. lazyaria does not delete
it. It moves it to the trash; an unfinished download goes there as a
folder with the same kind of name, such as `movie (unfinished).mkv`,
holding the file and its `.aria2` control file:

```sh
lazyaria remove --delete-files 4
```

It only takes files the job downloaded itself. See
[Where files go](../concepts/files.md).

## Undo a remove

`remove` prints an undo command. Run it within 10 seconds:

```sh
lazyaria undo VCDLIKHYM7PNFOM5FLMGY6FZ2Q
```

```text
undone; jobs that were running are paused, resume them with `lazyaria resume`
```

The job comes back, and so do its files if they went to the trash. A
download that was running comes back paused. The undo window is the
`remove.undo_seconds` setting.

After the window has passed, the files are still in your trash, and you
can restore them from there.

## Adding the same link twice

lazyaria refuses a link that is already in the list, and a file name that
already exists in the folder:

```text
lazyaria add: "ubuntu.iso" is already in the list (done); add it with --force to download it again [existing job #1]
```

`--force` downloads it anyway, and saves it under a new name such as
`ubuntu.1.iso`.
See [Where files go](../concepts/files.md).

## Next

- [Refresh an expired link](../guides/refresh-an-expired-link.md)
- [All commands](../reference/commands.md)
- [All settings](../reference/settings.md)
