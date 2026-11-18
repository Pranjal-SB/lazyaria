# Refresh an expired link

Many download sites hand out links that stop working after a while. If
such a link expires in the middle of a download, the server starts to
refuse it, and the download fails. `lazyaria refresh` gives the download a
new link and keeps the part it already has.

## Spot an expired link

The job shows up in `lazyaria list` with the state `error` and the
server's answer in brackets:

```text
ID        STATE   DONE  SIZE       NAME
01M7MD92  error   2%    190.7 MiB  movie.mkv  (The response status is not successful. status=403)
```

A `403` (forbidden) or `410` (gone) answer to a download that was working
usually means the link expired. `lazyaria list --json` names the reason
in the `error_kind` field, which is `link-expired` in this case.

## Get a new link and refresh

1. Go back to the page you got the link from and copy a fresh link to the
   same file.
2. Give it to the job:

   ```sh
   lazyaria refresh 01M7MD92 'https://example.com/movie.mkv?sig=new'
   ```

   ```text
   refreshed 01M7MD923S8PNYSXSMX0BDWEYG
   ```

The job starts again from where it stopped:

```text
ID        STATE   DONE  SIZE       NAME
01M7MD92  active  2%    190.7 MiB  movie.mkv
```

Put the link in single quotes. Signed links often contain `&` or `?`,
which the shell would otherwise read as its own syntax.

## Things to know

- The new link must point at the same file. lazyaria carries on writing
  the file it has, so a link to a different file gives you a broken
  result.
- You can refresh a job while it runs, while it is paused, or after it
  failed. A paused job stays paused; `lazyaria resume` continues it from
  the new link.
- The new link must be an HTTP, HTTPS, FTP or SFTP link. Anything else is
  refused before it reaches the daemon, with exit code 2. A magnet link,
  for example, gives:

  ```text
  lazyaria refresh: torrents and magnets cannot be refreshed; refresh takes an http(s), ftp or sftp link
  ```

  Torrent and magnet jobs cannot be refreshed either: they have no link
  that expires.
- A finished job has nothing to refresh. A removed job cannot be
  refreshed either: add the link again with `lazyaria add`. These
  refusals also exit with code 2.
- The server must support resuming (HTTP range requests) for the download
  to carry on. Most file hosts do. If it does not, the job fails again
  with a message about the range header.
