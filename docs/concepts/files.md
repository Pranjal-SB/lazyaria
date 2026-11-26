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

A download runs in a hidden folder inside the download folder,
`.laria/<job id>/`. When it finishes, lazyaria moves it into the download
folder under its name. While downloads run, the download folder looks like
this:

```text
.laria/
movie.mkv.laria
```

`movie.mkv.laria` is a marker: a symbolic link to the unfinished file in
`.laria`. It shows that the download is running, and you can open it to
look at what has arrived so far, for example to start watching a video.
Deleting the marker does not affect the download, and lazyaria puts it
back while the download runs. It removes the marker when the download
finishes or is removed.

There is a marker only for a download of a single file, not for torrents
or magnet links, and not on drives that cannot hold symbolic links (FAT,
exFAT). Set `downloads.marker` to `false` to turn markers off.

Next to the unfinished file, in `.laria/<job id>/`, aria2 keeps a small
control file with `.aria2` added to the name. It records which parts are
done, so the download can resume after a pause or a restart. Without it,
or when the server cannot continue a download from the middle, the
download starts over in the same folder. The finished file still gets the
job's name.

### Moving the finished download

When a download finishes, lazyaria first writes down in its database what
it is going to move and under which names. Then it renames each file or
folder into the download folder, which copies no data. If the daemon or
the computer stops halfway, the next start finishes the move from that
record. The `.laria/<job id>/` folder is then removed, and `.laria` too
once it is empty.

If the name is taken by then, the download gets the next free name, as
described [below](#when-a-name-is-taken).

When the download folder and `.laria` are on different file systems,
which can happen in some container setups, a rename is not possible and
lazyaria copies the download instead. This needs room for a second copy:
lazyaria checks for twice the download's size first, and if there is not
enough, the job fails with kind `no_space` and keeps its files in
`.laria`.

A `.laria/<job id>/` folder that belongs to no job, for example after a
crash right after `add`, is moved to the trash once it is an hour old.

### Hidden folders and other programs

`.laria` starts with a dot, so most file managers and `ls` do not show it.
Jellyfin and Plex skip folders that start with a dot, so unfinished
downloads do not appear in their libraries.

Syncthing does sync hidden folders. If the download folder is synced, add
this line to the `.stignore` file at the top of the synced folder, so
Syncthing does not copy unfinished downloads:

```text
.laria
```

### Torrents

Torrents are downloaded into `.laria/<job id>/` too. Seeding is off by
default (`bt.seed_minutes` is `0`), so a finished torrent is moved into
the download folder like any other download.

When seeding is on (`bt.seed_minutes` above `0`), the files appear in the download folder as soon as
the download finishes, and aria2 goes on seeding. The files in the
download folder and the ones aria2 seeds from in `.laria` are the same
files on disk (hard links), so they take up space only once. This means:

- Editing a file in place while it seeds changes what aria2 uploads.
  Make a copy first if you want to change it.
- Deleting the files from the download folder stops the seeding and
  frees the space:
  the file of a single-file torrent, or the whole folder of a larger one.
  Moving or renaming them counts as deleting them. Deleting only some
  files inside a torrent's folder does not stop it.
- When seeding ends, the copy in `.laria` is removed. The files in the
  download folder stay.

On drives without hard links (FAT, exFAT), the files are moved into the
download folder and aria2 seeds them from there. A torrent saved under a
new name because its name was taken, or a magnet link, is not seeded on
such a drive.

## When a name is taken

By default, lazyaria leaves files that are already in the folder as they
are, and `add` refuses a link that is already in the list, or a download
whose file name already exists in the folder, and exits with 4:

```text
lazyaria add: "ubuntu.iso" is already in the list (done); add it with --force to download it again [existing job #1]
```

With `--force`, lazyaria downloads it, and when it finishes, saves it
under the next free name, the way aria2 does: `notes.pdf` becomes
`notes.1.pdf`, then `notes.2.pdf`. The same happens when a file with the
download's name appears in the folder while it runs.

```sh
lazyaria add --force https://example.com/files/notes.pdf
```

```text
added #6 notes.pdf
```

`lazyaria list --all` shows the name it was saved under once it is done.

Two links count as the same when they differ only in ways that do not
change the download: the case of the host name, a default port such as
`:443`, a `#fragment`, or the order of the query parameters.

Two settings change this:

| Setting | Values | What it does |
|---|---|---|
| `reliability.duplicates` | `ask` (default), `allow`, `skip` | `ask` refuses a link that is already in the list unless you pass `--force`. `allow` adds it. `skip` leaves it out, prints `skipped`, and exits with 0. |
| `downloads.on_exists` | `rename` (default), `ask`, `overwrite` | What a forced or allowed download does about a file that exists. `rename` picks a free name. `ask` refuses, so you can choose a name with `--out`. `overwrite` moves the old file to the trash, then downloads. |

## Removing a job and its files

`lazyaria remove <job>` takes the job off the list and keeps its files. A
finished download stays where it is, in the download folder. An
unfinished one is stopped and moved out of `.laria/<job id>/` into the
download folder, under the name it would have finished with, together
with its `.aria2` control file:

```text
movie.mkv
movie.mkv.aria2
```

If that name is taken, it gets the next free name, as described
[below](#when-a-name-is-taken), for example `movie.1.mkv` and
`movie.1.mkv.aria2`. The `.laria/<job id>/` folder is then removed. The
control file marks the file as unfinished; delete both when you no
longer need them. Undo moves them back, so the download resumes.

`lazyaria remove --delete-files <job>` also moves the job's files to the
trash: the one in your home folder (`~/.local/share/Trash`) when the file
is on the same disk, otherwise a `.Trash-<your user id>` folder at the top
of the file's disk. It is the same trash your file manager uses. If no
trash can be used on that disk, nothing is moved and the job is kept.

It takes only files the job downloaded:

- An unfinished download is stopped first, and its `.laria/<job id>/`
  folder goes to the trash, with the unfinished file and its control
  file. The trash lists it as `<name> (unfinished)`, so you can tell it
  apart in a file manager.
- A finished download takes the file or folder it put in the download
  folder, as long as it is still there under that name. A download saved
  under a new name, such as `notes.1.pdf`, takes that file and leaves
  `notes.pdf` alone.
- A finished torrent's folder is not moved as a whole, because it may
  hold other files by now. It takes the files the torrent lists that were
  picked for download. Subfolders that end up empty are removed; the top
  folder stays.
- A torrent that is seeding stops, and its copy in `.laria` is removed.

When lazyaria cannot tell which files in a torrent's folder are the
torrent's, for example for a finished magnet link that aria2 no longer
knows, it removes the job, leaves the folder alone and says so:

```text
kept /home/you/Downloads/Music: cannot tell which files are this torrent's
```

## Undo

`remove` prints an undo command:

```text
removed #3
undo: lazyaria undo VCDLIKHYM7PNFOM5FLMGY6FZ2Q
```

It brings back the jobs and any files the remove moved, to the trash or
out of `.laria`. A job that was running comes back paused. The command
works for 10 seconds (`remove.undo_seconds`; `0` turns undo off) and only
while the same daemon runs. If a file cannot go back because something
else now has its name, undo says so and the token stays valid for another
try.

After that, undo exits with 1:

```text
lazyaria undo: the undo has expired: undo works for 10 seconds after a remove (remove.undo_seconds). Add the link again with `lazyaria add`; files that were deleted are in the Trash
```

A token the daemon does not know, because it was mistyped or the daemon
has restarted since, also exits with 1:

```text
lazyaria undo: no undo with that token: check that you copied all of it; tokens also end when the daemon restarts. Add the link again with `lazyaria add`; files that were deleted are in the Trash
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
  as browsers do. The link is stored without user name, password, query
  string or fragment, since these can hold credentials or signed tokens; a
  magnet link keeps only its info-hash. Turn this off with
  `privacy.stamp_origin_xattr`.
