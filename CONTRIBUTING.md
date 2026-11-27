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
e2e/install-test.sh    # install.sh in containers; needs docker and python3
```

The command, key and settings reference pages are generated from the
code. After you change a command's help, a flag, a key or a setting, run
`go generate .`. A test fails when the pages are out of date.

`docs/install.sh` is a copy of `install.sh`, so the docs site serves it.
After you change `install.sh`, run `go generate .` to copy it.

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

A release always publishes the tarballs and `checksums.txt` to GitHub.
The Homebrew cask and the AUR package are configured in `.goreleaser.yaml`
but stay off until their secret exists. Without the secret the release
skips that upload and still succeeds.

To switch on Homebrew:

1. Create the public repository `Pranjal-SB/homebrew-tap`, with a first
   commit so it has a default branch.
2. Create a fine-grained personal access token limited to that repository,
   with "Contents: read and write".
3. Add it to this repository as the Actions secret
   `HOMEBREW_TAP_GITHUB_TOKEN`.

The next release writes `Casks/lazyaria.rb` there. Users install it with
`brew install Pranjal-SB/tap/lazyaria`, which also installs aria2.

To switch on the AUR:

1. Create an account on aur.archlinux.org and add an SSH public key to it.
   Use a key made only for this, without a passphrase.
2. Add the private key to this repository as the Actions secret
   `AUR_SSH_PRIVATE_KEY`.

The next release pushes `PKGBUILD` and `.SRCINFO` to
`ssh://aur@aur.archlinux.org/lazyaria-bin.git`; the first push creates
the package. Users install it with an AUR helper, for example
`yay -S lazyaria-bin`, which also installs aria2. To
put a maintainer line in the PKGBUILD, add `maintainers` under `aurs` in
`.goreleaser.yaml`.

Both skip pre-release tags such as `v0.2.0-rc.1`. To check the config
and see the generated files in `dist/` without publishing anything:

```sh
go run github.com/goreleaser/goreleaser/v2@latest check
go run github.com/goreleaser/goreleaser/v2@latest release --snapshot --clean
```

## The documentation site

The pages in `docs/` are published with GitHub Pages and the built-in
Jekyll, at https://pranjal-sb.github.io/lazyaria/. There is no build step
and no workflow. To turn it on once, open the repository's Settings, then
Pages, and choose Deploy from a branch, branch `master`, folder `/docs`.

Every page needs front matter (`title`, and `parent` and `nav_order` for
pages inside a section). The generated reference pages get theirs from
`go generate .`. Do not use `{{` or `{%` in a page: Jekyll reads them as
template code. `docs/install.sh` is published as it is.
