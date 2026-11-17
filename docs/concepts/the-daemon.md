# The daemon

A daemon is a program that runs in the background. lazyaria has one,
started by the same `lazyaria` binary. The commands you type are small
clients: they ask the daemon to do the work and print its answer.

## What the daemon does

- It keeps the list of jobs in a small database (SQLite). The database is
  the record of what you asked for, so nothing is lost when a process
  stops.
- It starts aria2, the program that downloads, and tells it what to do.
  See [The engine](the-engine.md).
- It answers the `lazyaria` commands over a local socket, a file that only
  your user can open. It refuses to talk to other users, and it never
  listens on the network.

There is one daemon per user.

## It starts when you need it

You never start the daemon by hand. When a command such as `add` or
`list` needs it and it is not running, the command starts it and waits
until it is ready. That takes a moment the first time.

`lazyaria doctor` and `lazyaria logs` do not start it. `doctor` reports
`not running; it starts when a client needs it`, which is normal.

## It stops when you do not

The daemon stops by itself after 30 seconds with no work and no client
connected. Work means a job that is queued, downloading, waiting to retry
or seeding. Paused, failed and finished jobs do not keep it running. It
stops aria2 before it exits.

So when nothing downloads, no lazyaria process runs and nothing uses
memory or the network. The next command starts it again, and the jobs are
as you left them: paused jobs are still paused, finished jobs are still in
`list --all`.

The wait is the `daemon.idle_exit` setting.

## If something crashes

If aria2 stops unexpectedly, the daemon starts it again and gives it the
unfinished downloads back. If aria2 keeps crashing, the daemon stops
trying, and `lazyaria doctor` reports the engine as failed. After you fix
the cause, `lazyaria doctor --restart-engine` starts aria2 again.

If the daemon itself stops, the next command starts a new one, which
picks up the unfinished downloads from the database.

## Sleep

While something downloads, the daemon asks the system not to go to sleep,
through systemd-logind or elogind. `lazyaria status` shows
`sleep  held off while downloading` when it does. Set
`reliability.inhibit_sleep` to `false` to turn this off.

## See what it is doing

```sh
lazyaria status
```

```text
daemon     dev (protocol 1), pid 178452, mode on-demand, up 1m16s
engine     ready, aria2 1.37.0
downloads  /home/you/Downloads
sleep      held off while downloading
```

`lazyaria logs` prints the end of the daemon's log, and `lazyaria logs -f`
keeps printing new lines.

## Settings and the daemon

`lazyaria config set` saves a value and tells the daemon at once.
Settings marked *now* in the [settings reference](../reference/settings.md)
take effect immediately. The others take effect the next time the daemon
starts.

`lazyaria config edit` opens the config file in your editor, checks it
when you close the editor, and tells a running daemon to read it again.
If you edit the file another way, the daemon reads it the next time it
starts. To make it read the file now, send it a hang-up signal:

```sh
kill -HUP "$(lazyaria status --json | jq .pid)"
```

## Stop it now

There is no `stop` command. Pause or remove what is running, and the
daemon stops after 30 seconds. To stop it at once, end the process; it
shuts down aria2 cleanly first:

```sh
kill "$(lazyaria status --json | jq .pid)"
```

Running downloads are not lost. The next start picks them up again.

## Keep it running

To keep the daemon running all the time, set the mode to `always`:

```sh
lazyaria config set daemon.mode always
```

This applies the next time the daemon starts.

On a system with systemd, `lazyaria service install` instead writes a
systemd user service that runs `lazyaria daemon --mode=always`, enables
it, and starts it. Its log then goes to the journal:
`journalctl --user -u lazyaria`. `lazyaria service uninstall` removes it.
