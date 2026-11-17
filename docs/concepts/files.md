# Where files go

This page explains where downloads are written, what happens when a name
is already taken, and what `remove` does with files.

## The download folder

A download goes to the folder given with `lazyaria add --dir`, or else to
the `downloads.dir` setting, or else to your desktop's download folder
(usually `~/Downloads`). `--out` picks the file name. Without it, the name
comes from the link, the server or the torrent. The full order is in
[Files and paths](../reference/files-and-paths.md#downloads).

## Unfinished downloads

While a download runs, it is written under its final name, next to a
small control file with `.aria2` added to the name:

```text
movie.mkv
movie.mkv.aria2
```

The control file records which parts are done, so the download can
resume after a pause or a restart. aria2 deletes it when the download
finishes. A file next to its `.aria2` file is not finished yet, even if it
looks complete.

## When a name is taken

lazyaria does not write over existing files. By default, `add` refuses a
link that is already in the list, or a download whose file name already
exists in the folder, and exits with 4:

```text
lazyaria add: "ubuntu.iso" is already in the list (done); add it with --force to download it again [existing job 01M7MCXK5GYY1BXGRJA7MD9R97]
```

With `--force`, lazyaria downloads it under the next free name, the way
aria2 does: `notes.pdf` becomes `notes.1.pdf`, then `notes.2.pdf`. A name
that another job in the list is still downloading counts as taken too.

```sh
lazyaria add --force https://example.com/files/notes.pdf
```

```text
added 01M7MD22FTPMDGRGZA5W2731BP notes.1.pdf
```

Two links count as the same when they differ only in ways that do not
change the download: the case of the host name, a default port such as
`:443`, a `#fragment`, or the order of the query parameters.

Two settings change this:

| Setting | Values | What it does |
|---|---|---|
| `reliability.duplicates` | `ask` (default), `allow`, `skip` | `ask` refuses a link that is already in the list unless you pass `--force`. `allow` adds it. `skip` leaves it out, prints `skipped`, and exits with 0. |
| `downloads.on_exists` | `rename` (default), `ask`, `overwrite` | What a forced or allowed download does about a file that exists. `rename` picks a free name. `ask` refuses, so you can choose a name with `--out`. `overwrite` moves the old file to the trash, then downloads. |

## Removing a job and its files

`lazyaria remove <id>` takes the job off the list. Its file stays where
it is.

`lazyaria remove --delete-files <id>` also moves the job's files to the
trash: the one in your home folder (`~/.local/share/Trash`) when the file
is on the same disk, otherwise a `.Trash-<your user id>` folder at the top
of the file's disk. It is the same trash your file manager uses. If no
trash can be used on that disk, nothing is moved and the job is kept.

It takes only files the job downloaded:

- A running download is paused first; its unfinished file and `.aria2`
  control file are moved.
- A download saved under a new name, such as `notes.1.pdf`, takes that
  file and leaves `notes.pdf` alone.
- A job that never received a byte takes no file, only its `.aria2`
  control file.
- A torrent takes the files it lists that were picked for download. Its
  folder is not moved as a whole, because it may hold other files.
  Subfolders that end up empty are removed; the top folder stays.

When lazyaria cannot tell which files in a torrent's folder are the
torrent's, for example for a finished magnet link that aria2 no longer
knows, it removes the job, leaves the folder alone and says so:

```text
kept /home/you/Downloads/Music: cannot tell which files are this torrent's
```

## Undo

`remove` prints an undo command:

```text
removed 01M7MCYN6ZSBZPXYHXGG8SH1RN
undo: lazyaria undo VCDLIKHYM7PNFOM5FLMGY6FZ2Q
```

It brings back the jobs and any files the remove moved to the trash. A
job that was running comes back paused. The command works for 10 seconds
(`remove.undo_seconds`; `0` turns undo off) and only while the same
daemon runs. If a file cannot go back because something else now has its
name, undo says so and the token stays valid for another try.

After that, undo exits with 1:

```text
lazyaria undo: [-32098] nothing to undo: the undo token is unknown or has expired (tokens end after remove.undo_seconds or when the daemon restarts). Add the link again with `lazyaria add`; files that were deleted are in the Trash
```

The files are still in the trash.

## Other checks

- **Free space.** When the size of a download is known up front, `add`
  checks that the disk has room for it, plus 1% (exit code 4 if not). If
  the disk fills up anyway, lazyaria pauses every download going to that
  folder.
- **Unplugged drives.** lazyaria remembers which drive each download was
  writing to. If that drive is no longer mounted when the download is
  handed to aria2 again (after a restart, a retry or a resume), the job
  fails with `<folder> is missing or on a different drive; mount it and
  retry`. Mount the drive and run `lazyaria resume`.
- **Origin.** When a download finishes, lazyaria records its link in the
  extended attributes (`user.xdg.origin.url`) of the files it downloaded,
  as browsers do. Turn this off with `privacy.stamp_origin_xattr`.
