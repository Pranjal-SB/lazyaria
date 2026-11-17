# How lazyaria compares

There are several good download managers. This page says how lazyaria
relates to the ones people ask about most, as they are at the time of
writing, and what lazyaria does not do today.

## Other tools

- **Plain `aria2c`.** lazyaria runs aria2 for you, so the downloading
  itself is the same. On its own, aria2c is lighter and gives you every
  option, but you look after its session file, its remote-control
  interface and its file naming yourself. If you only need one download
  now and then, `aria2c <link>` is enough.
- **Motrix.** A graphical desktop app, also built on aria2. Choose it if
  you want windows and buttons. lazyaria is for the terminal and for
  machines without a desktop.
- **Internet Download Manager (IDM).** A paid Windows app with its own
  engine, deep browser integration and video capture. lazyaria runs on
  Linux, has no browser integration and cannot download from video pages.
- **Surge.** A terminal download manager written in Go, with its own HTTP
  engine, a terminal interface you can use today, and a browser extension.
  lazyaria has no terminal interface yet. It adds torrents and magnet
  links through aria2.

## What lazyaria does not do today

- There is no terminal interface (TUI) yet, only the command line.
- There is no browser integration, and no downloading from video sites.
- There is no graphical app, and no Windows support.
- There are no packages or prebuilt binaries; you build it from source.
- It cannot be controlled over the network. The daemon only listens on a
  local socket that only your user can open.

## Speed

lazyaria makes no claim about download speed. It uses aria2's transfer
code, with the same limits, such as at most 16 connections per server.
See [The engine](concepts/the-engine.md).
