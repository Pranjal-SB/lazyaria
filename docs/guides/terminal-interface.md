# The terminal interface

`lazyaria` with no command opens a screen with your downloads on it. It
shows the same list the commands work on, and the commands keep working
next to it, in another terminal or in a script.

## Open it

```sh
lazyaria
```

If you set up the short name `laria` (see
[Installation](../getting-started/installation.md)), `laria` opens it too.

It needs a terminal. Run from a script or a pipe, `lazyaria` with no
command prints its usage and exits with 2, as before. The daemon starts by
itself if it is not running.

To leave, press `q`. Your downloads keep going in the daemon, and
lazyaria prints how many continue, for example:

```text
2 downloads continue in the background; run lazyaria to come back.
```

## The screen

The top line shows the total download and upload speed, a small graph of
the last minute, about how long until everything running is finished, and
whether aria2 is ready.

Below it are tabs, each with a count:

- **Downloading**: what is downloading right now. The interface opens here.
- **Waiting**: queued, paused, or waiting to try again.
- **Finished**: done, and torrents that are still seeding.
- **Problems**: downloads that failed. This tab only appears when
  something failed; the bottom line then says how many, and that `4`
  shows them.

Press `1` to `4` to pick a tab, or `[` and `]` (or `tab` and
`shift+tab`) to go left and right. `j` and `k`, or the arrow keys, move
through the list. Each tab remembers what you had selected.

Each download takes two lines. The first has a status icon, the job
number (`#14`, on wide terminals), the name and the size. The second has
a progress bar, the percentage, the speed and the time left, or a word
that says what is going on, such as `paused`, `retrying` or
`link expired`. Every state has an icon and a word, so nothing depends on
colour alone.

### Details

On a terminal at least 100 columns wide, the details of the selected
download sit to the right of the list: where it is saved, where it comes
from, a map of which pieces are done, its connections or peers, a
torrent's files, and what happened to it so far. On a narrower terminal,
`enter` opens the details under the list and closes them again. Below
80 by 24, rows shrink to one line and details are hidden.

`J` and `K` scroll the details when they do not fit; the mouse wheel
does too.

## Add a download

Paste a link anywhere on the screen. lazyaria picks out every link in
what you pasted (web links, magnet links and paths of `.torrent` files)
and opens the add form with them filled in. You can also press `a` to
open an empty form.

The form has the link, the folder to save to, an optional file name and
**Start paused**. Several links go in the link field separated by spaces;
each becomes its own download, and the file name is then left out.

| Key | In the form |
|---|---|
| `tab`, `shift+tab` | next and previous field |
| `space` | tick **Start paused** |
| `enter` | add |
| `esc` | close the form |

When a link is already in the list, the form says so and offers
**Download again**, **Show it** (select the one you have) or **Cancel**.
The other links are added either way. A link that cannot be added stays
in the field with the reason, so you can fix it and press `enter` again;
the ones that were added are not added twice.

## Pause and resume

`space` pauses the selected download, or resumes it when it is paused or
failed. The keys at the bottom of the screen only offer what the selected
download allows.

## Remove a download

`x` or `delete` asks how to remove the selected download:

- **Remove from list** takes it off the list and leaves its file where it
  is. This button is selected when the question opens, so `enter` never
  deletes a file.
- **Delete files → Trash** also moves its files to the trash, the same
  way as `lazyaria remove --delete-files` (see
  [Where files go](../concepts/files.md)).
- **Cancel** does nothing.

`←` and `→` (or `tab`) choose, `enter` confirms, `esc` cancels.

After a remove, the bottom line says `Removed <name>  u undo` for a few
seconds. `u` brings the download back, and its files too if they went to
the trash. How long the note stays is `ui.toast_seconds`.

## The mouse

Click a download to select it, a tab to open it, or a key at the bottom
to run it. Buttons in the remove question and the add form can be
clicked. The wheel moves through the list, or scrolls the details when
the pointer is over them. Every mouse action also has a key.

While lazyaria uses the mouse, the terminal does not select text on its
own. Hold `Shift` while you drag to select text as usual.

`ui.mouse` sets what the mouse does: `all` (the default, which also
highlights what is under the pointer), `cell`, or `off`.

## Looks

lazyaria uses the 16 colours of your terminal's theme, so it follows a
light or dark theme by itself. With `NO_COLOR` set it uses no colours.

`ui.glyphs` picks the symbols:

- `unicode` (the default) works with every common monospace font.
- `ascii` uses plain characters, for consoles that lack the others.
  lazyaria also uses it when `TERM` is `dumb`.
- `nerd` uses icons from a [Nerd Font](https://www.nerdfonts.com/). Turn
  it on only if your terminal uses one; otherwise the icons show as
  empty boxes.

```sh
lazyaria config set ui.glyphs nerd
```

## Settings

These settings change the interface. It reads them when it starts, so
quit and open it again after a change.

| Setting | What it does |
|---|---|
| `ui.mouse` | `all`, `cell` or `off` |
| `ui.glyphs` | `unicode`, `ascii` or `nerd` |
| `ui.units` | sizes in 1024 steps (`iec`, KiB) or 1000 steps (`si`, kB) |
| `ui.wide_breakpoint` | columns from which details sit next to the list |
| `ui.details_position` | `auto`, `right`, `bottom` or `hidden` |
| `ui.details_width_percent` | width of the details beside the list |
| `ui.paste_detection` | `false` stops a paste from opening the add form |
| `ui.toast_seconds` | how long notes such as `Removed ...` stay |
| `ui.sparkline_seconds` | how much speed history the top graph shows |
| `ui.osc_links` | the folder in the details is a clickable link |

All settings are in [Settings](../reference/settings.md), and every key
is in [Keys](../reference/keys.md). `?` lists the keys on screen.
