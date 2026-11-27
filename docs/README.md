---
title: Home
nav_order: 1
permalink: /
---

# lazyaria documentation

lazyaria is a download manager for the terminal. It keeps a list of
downloads and runs them with aria2, in a background process that only runs
while there is work to do.

lazyaria is pre-release. These pages describe what works today: the
terminal interface, which `lazyaria` opens with no command, and the
command line, which works next to it.

## Getting started

- [Installation](getting-started/installation.md): build lazyaria and check
  your setup.
- [Your first download](getting-started/first-download.md): add, list,
  wait, pause, resume, remove and undo.

## Guides

One task per page.

- [The terminal interface](guides/terminal-interface.md): the download
  list, adding by paste, pausing, removing and undo, the mouse and the
  keys.
- [Refresh an expired link](guides/refresh-an-expired-link.md): give a
  download a new link and keep what it has.
- [Use lazyaria from scripts](guides/use-from-scripts.md): exit codes,
  `--wait`, links on standard input, JSON output.

## Concepts

How lazyaria works, and why.

- [The daemon](concepts/the-daemon.md): the background process that starts
  when you need it and stops when you do not.
- [Where files go](concepts/files.md): the download folder, unfinished
  downloads, names that are taken, removing files and undo.
- [The engine](concepts/the-engine.md): how lazyaria uses aria2.
- [How lazyaria compares](comparison.md): other download managers, and
  what lazyaria does not do yet.

## Reference

- [Commands](reference/commands.md): every command and flag (generated).
- [Keys](reference/keys.md): every key of the terminal interface
  (generated).
- [Settings](reference/settings.md): every setting and its default
  (generated).
- [Exit codes](reference/exit-codes.md)
- [Files and paths](reference/files-and-paths.md): where lazyaria keeps its
  config, data, log and socket.
- [Troubleshooting](reference/troubleshooting.md): common errors and what
  to do about them.
