# Releasing Jikko

A release is a Git tag. Pushing `vX.Y.Z` runs `.github/workflows/release.yml`,
which runs the test suite and then GoReleaser (`.goreleaser.yaml`): binaries
for Linux and macOS (amd64, arm64) and Windows (amd64), archives,
`checksums.txt`, a GitHub Release, and a Homebrew formula pushed to
`KakkoiDev/homebrew-tap`.

## One-time setup

1. **Homebrew tap.** Create the repository `KakkoiDev/homebrew-tap` (empty is
   fine; GoReleaser creates `Formula/jikko.rb` on the branch named in
   `.goreleaser.yaml`, `main`). Users then run
   `brew install KakkoiDev/tap/jikko`.
2. **Tap token.** Add a repository secret `HOMEBREW_TAP_GITHUB_TOKEN` to
   `KakkoiDev/jikko`: a fine-grained personal access token with
   *Contents: read and write* on `KakkoiDev/homebrew-tap` only. The default
   `GITHUB_TOKEN` cannot push to another repository. Without the secret the
   release still succeeds; only the tap push is skipped.
3. **GitHub Pages.** Repository *Settings → Pages → Build and deployment →
   Source: GitHub Actions*. `.github/workflows/pages.yml` then publishes
   `site/` on every push to `master` that touches it (or on manual dispatch)
   to `https://kakkoidev.github.io/jikko/`.

## Cutting a release

```sh
# 1. On master, with a clean tree, make sure everything passes.
make check

# 2. Dry-run the release pipeline. Artifacts land in dist/ (gitignored).
make release-snapshot
dist/jikko_linux_amd64_v1/jikko version

# 3. Move the [Unreleased] entries in CHANGELOG.md under a dated
#    "## [X.Y.Z] - YYYY-MM-DD" heading and update the compare links.

# 4. Tag and push the tag. The version is the tag without the "v".
git tag -a v0.1.0 -m "jikko v0.1.0"
git push origin v0.1.0
```

Then watch the *Release* workflow. It fails before publishing anything if the
tests fail. GoReleaser marks `v0.x` tags and pre-release suffixes
(`v1.0.0-rc.1`) as pre-releases on GitHub.

After the first release with the tap configured, check the formula:

```sh
brew install KakkoiDev/tap/jikko
brew test jikko
```

## The version string

Binaries are built with `-ldflags "-s -w -X main.version={{.Version}}"`, so
`jikko version` prints the tag. A `go install ...@latest` build has no
ldflags; `main.version` keeps its default there.

## Validating the configuration

`make release-check` runs `goreleaser check`. It currently exits 2 with
"configuration is valid, but uses deprecated properties": the `brews`
section is deprecated upstream in favour of `homebrew_casks`
(https://goreleaser.com/deprecations#brews). Casks are macOS-only and cannot
carry a `test do` block, so Jikko keeps a formula, which also installs on
Linux. `goreleaser release` only warns. Revisit when GoReleaser v3 removes
`brews`.

GoReleaser is run with `go run github.com/goreleaser/goreleaser/v2@latest`
by the Makefile; set `GORELEASER=goreleaser` to use an installed binary.

## Checklist for a human

- [ ] `KakkoiDev/homebrew-tap` exists
- [ ] `HOMEBREW_TAP_GITHUB_TOKEN` secret set on `KakkoiDev/jikko`
- [ ] Pages source set to GitHub Actions
- [ ] `CHANGELOG.md` dated for the version
- [ ] tag pushed
