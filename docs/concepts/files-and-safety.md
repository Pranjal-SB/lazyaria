# Files and safety

A download manager writes into folders that hold your own files. lazyaria
follows a few rules so that it never destroys one of them.

## lazyaria never writes over a file

If the file a download would create already exists, lazyaria does not
touch it. What it does instead depends on the case.

**The link is already in the list.** `add` refuses it and exits with 4:

```text
lazyaria add: "ubuntu.iso" is already in the list (done); add it with --force to download it again [existing job 01M7MCXK5GYY1BXGRJA7MD9R97]
```

Two links count as the same when they differ only in ways that do not
change the download: the case of the host name, a default port such as
`:443`, a `#fragment`, or the order of the query parameters. A different
query value makes a different link.

**A file with that name is already in the folder.** `add` refuses it too:

```text
lazyaria add: /home/you/Downloads/notes.pdf already exists; add it with --force to download it again, or choose another name
```

**You pass `--force`.** lazyaria downloads it again under the next free
name, the way aria2 itself would: `notes.pdf` becomes `notes.1.pdf`, then
`notes.2.pdf`.

```sh
lazyaria add --force https://example.com/files/notes.pdf
```

```text
added 01M7MD22FTPMDGRGZA5W2731BP notes.1.pdf
```

A name that another job in the list is still downloading counts as taken
too, even before its file exists.

You can change these rules:

| Setting | Values | What it does |
|---|---|---|
| `reliability.duplicates` | `ask` (default), `allow`, `skip` | `ask` refuses a duplicate unless you pass `--force`. `allow` adds it without asking. `skip` leaves it out, prints `skipped`, and exits with 0. |
| `downloads.on_exists` | `rename` (default), `ask`, `overwrite` | What a forced or allowed download does about a file that exists. `rename` picks a free name. `ask` refuses, so you can choose a name with `--out`. `overwrite` moves the old file to the trash, then downloads. |

Even `overwrite` does not overwrite: the old file goes to the trash
first, where you can restore it. If there is no trash on that disk,
nothing is added.

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

## Removing a job and its files

`lazyaria remove <id>` takes the job off the list. Its file stays where
it is.

`lazyaria remove --delete-files <id>` also removes the file, but it never
deletes anything. It moves the file to the trash:

- the trash in your home folder (`~/.local/share/Trash`), when the file is
  on the same disk;
- otherwise a `.Trash-<your user id>` folder at the top of the disk the
  file is on.

This is the same trash your file manager uses, so you can restore a file
from there at any time.

`--delete-files` only takes files that the job wrote itself. If the job
never received a byte, for example because it failed at once, a file of
the same name that was there before is left alone. The `.aria2` control
file always goes.

If no trash can be used on that disk, lazyaria moves nothing and keeps the
job:

```text
no trash is available for /mnt/usb/movie.mkv; the files are untouched and the job was kept (paused, if it was running). Remove the job without --delete-files, then move or delete the files yourself
```

## Undo

Every `remove` prints an undo command:

```text
removed 01M7MCYN6ZSBZPXYHXGG8SH1RN
undo: lazyaria undo VCDLIKHYM7PNFOM5FLMGY6FZ2Q
```

Run it within 10 seconds to bring back the jobs and any files the remove
moved to the trash. A job that was running comes back paused; resume it
with `lazyaria resume`. Set `remove.undo_seconds` to change the window,
or to `0` to turn undo off.

If a file cannot go back because something else now has its name, undo
says so, leaves that job removed, and keeps the token valid so you can
try again after you move the other file away.

After the window has passed, undo says `nothing to undo: the undo window
has passed`. Trashed files are still in the trash.

## Other safety checks

- **Free space.** When the size of a download is known up front, `add`
  checks that the disk has room for it, plus 1% (exit code 4 if not). If
  the disk fills up anyway, lazyaria pauses every download going to that
  folder, so they do not all fail one by one.
- **Unplugged drives.** lazyaria remembers which drive each download was
  writing to. If that drive is no longer mounted when lazyaria hands the
  download to aria2 again (after a restart, a retry or a resume), it does
  not write into the empty mount point. The job fails with
  `<folder> is missing or on a different drive; mount it and retry`.
  Mount the drive and run `lazyaria resume`.
- **Origin.** When a download finishes, lazyaria records its link in the
  file's extended attributes (`user.xdg.origin.url`), as browsers do. Turn
  this off with `privacy.stamp_origin_xattr`.
