# Contributing

Bug reports and ideas are welcome as issues. Please open an issue before a
large change, so we can agree on the approach first.

## Build and test

You need Go 1.27 or newer, and aria2 for the end-to-end tests.

```sh
go build -o lazyaria .
go vet ./...
go test -race ./...
go test -race -timeout 20m -tags e2e -v ./e2e/    # needs aria2c
```

The command, key and settings reference pages are generated from the
code. After you change a command's help, a flag, a key or a setting, run
`go generate .`. A test fails when the pages are out of date.

## Pull requests

- Use [Conventional Commits](https://www.conventionalcommits.org/) for
  commit messages, for example `fix(cli): say when a setting takes effect`.
  The release notes are built from them.
- Keep files small and focused. Split a file before it grows large.
- Add or update tests for the change, and update the docs when behavior
  changes.

## Releasing

Pushing a tag that starts with `v` runs the release workflow. To start a
release by hand for an existing tag:

```sh
gh workflow run release.yml -f tag=v0.1.0
```
