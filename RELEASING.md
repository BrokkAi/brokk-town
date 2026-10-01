# Releasing Brokk Town

A Town release is a tag push.

```sh
git tag vX.Y.Z-town && git push origin vX.Y.Z-town
```

That is the whole procedure. The tag starts
[`.github/workflows/release-town.yml`](.github/workflows/release-town.yml), which
runs CI and then `scripts/publish_tag.py` from the exact tagged commit. A release
ships the native archives on GitHub; there are no registry packages to publish
and nothing to do by hand after the tag.

## What the workflow does

1. **CI.** The `checks` job calls `.github/workflows/ci.yml`, so the release
   commit must pass the full gate on Linux and macOS. A red run stops the release
   before anything is built.
2. **Build.** `scripts/package_release.py` builds four native archives
   (`linux`/`darwin` × `amd64`/`arm64`) from the tagged commit and verifies every
   archive against its manifest: size, SHA-256, archive contents, executable bit,
   shipped legal files, and the build metadata that records the exact commit.
3. **Publish.** `scripts/publish_tag.py` stages the archives and `checksums.txt`
   on a draft GitHub release, uploads whatever is missing, and only then marks
   the release published. A stable tag becomes the repository's "latest"
   release; a prerelease does not.
4. **Verify.** Each step is gated on its exit status. Re-running a failed
   release is safe: existing draft assets are reused and any conflicting state
   is reported rather than overwritten.

The run happens in the `packages-publish` environment, which grants
`contents: write`. (The environment name predates the removal of registry
publishing; renaming it would change that environment's protection rules, so it
is left as is.)

## Tag format

- Stable: `vX.Y.Z-town` (for example `v0.8.0-town`).
- Prerelease: `vX.Y.Z-rc.1-town`.

The tag is the version. It is embedded in the binary, used in the archive names,
and recorded in each archive's `BUILD.json`. There is no separate version file to
edit, and the bots have no version of their own.

## Prerequisites for a maintainer

- A clean, committed checkout at the commit you want to release.
- Green CI for that commit.
- Current license policy. `scripts/package_release.py` calls
  `scripts/licenses.py`, so a reviewed dependency change is part of the build.
- `gh` authenticated for `BrokkAi/brokk-town` when publishing the release.

## Before tagging

You can build the whole release locally without uploading anything:

```sh
python3 scripts/publish_tag.py --tag vX.Y.Z-town --sha "$(git rev-parse HEAD)" --check-only
```

This runs the same packaging code as the workflow and stops after the build. It
requires a clean checkout and writes nothing outside `dist/`.

## Rules

- Tags and published versions are immutable. If a release fails partway, fix the
  cause and push the next version; never move or delete a tag.
- Only maintainers create release tags.
- The eight bots ship inside this release. A bot change is released by releasing
  Town.

## Installing a release

```sh
sh install.sh                 # latest stable release
sh install.sh vX.Y.Z-town     # a specific release
```

`install.sh` resolves the latest release from the GitHub releases API, downloads
the matching archive for the host platform, verifies its SHA-256 against the
release's `checksums.txt`, and installs it under `INSTALL_DIR/.brokk-town`. See
[README.md](README.md) for the full install and upgrade notes.
