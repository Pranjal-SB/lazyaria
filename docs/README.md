# lazyaria documentation

lazyaria is a download manager for the terminal. It keeps a list of
downloads, runs them with aria2, and never writes over a file you already
have.

lazyaria is pre-release. These pages describe what works today. The
terminal interface (TUI) is not released yet, so everything here uses the
command line.

## Getting started

- [Installation](getting-started/installation.md): build lazyaria and check
  your setup.
- [Your first download](getting-started/first-download.md): add, list,
  wait, pause, resume, remove and undo.

## Guides

One task per page.

- [Refresh an expired link](guides/refresh-an-expired-link.md): give a
  download a new link and keep what it has.
- [Use lazyaria from scripts](guides/use-from-scripts.md): exit codes,
  `--wait`, links on standard input, JSON output.

## Concepts

How lazyaria works, and why.

- [The daemon](concepts/the-daemon.md): the background process that starts
  when you need it and stops when you do not.
- [Files and safety](concepts/files-and-safety.md): no overwrites,
  duplicates, the trash and undo.
- [The engine](concepts/the-engine.md): how lazyaria uses aria2.

## Reference

- [Commands](reference/commands.md): every command and flag (generated).
- [Settings](reference/settings.md): every setting and its default
  (generated).
- [Exit codes](reference/exit-codes.md)
- [Files and paths](reference/files-and-paths.md): where lazyaria keeps its
  config, data, log and socket.
- [Troubleshooting](reference/troubleshooting.md): common errors and what
  to do about them.
