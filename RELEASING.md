# Releasing Brokk Town

A Town release is a tag push.

```sh
git tag vX.Y.Z-town && git push origin vX.Y.Z-town
```

That is the whole procedure. The tag starts [`.github/workflows/release-town.yml`](.github/workflows/release-town.yml),
which runs CI and then `scripts/publish_tag.py` from the exact tagged commit.
Nothing else is published manually, and no package can be released without its
own tag.

## What the workflow does

1. **CI.** The `checks` job calls `.github/workflows/ci.yml`, so the release
   commit must pass the full gate on Linux and macOS. CI is a hard prerequisite;
   a red run stops the release before anything is built.
2. **Build.** `scripts/package_release.py` builds four native archives
   (`linux`/`darwin` × `amd64`/`arm64`) from the tagged commit and verifies every
   archive against its manifest. `scripts/package_installers.py` turns those
   archives into five npm packages: the `@brokkai/brokk-town` launcher and one
   platform package per archive.
3. **Publish.** `scripts/publish_tag.py` stages the native archives on a draft
   GitHub release, publishes the npm platform packages, publishes the launcher
   last, and only then marks the GitHub release published. The launcher goes last
   because it pins the platform packages as optional dependencies, and npm treats
   a missing optional dependency as a successful install.
4. **Verify.** The workflow exits successfully only when every upload command
   succeeded. Re-running a failed release is safe: an existing draft release and
   already-published package versions are reused, and anything that conflicts is
   reported instead of overwritten.

The run happens in the `packages-publish` environment, which grants
`contents: write` and `id-token: write` for the provenance-signed npm publish.

## Tag format

- Stable: `vX.Y.Z-town` (for example `v0.8.0-town`).
- Prerelease: `vX.Y.Z-rc.1-town`. Prereleases are published to npm under the
  `next` tag and are not marked "latest" on GitHub.

The version in the tag is the version embedded in the binary, the name on the
native archives, and the npm version. There is no separate version file to edit.

## Prerequisites for a maintainer

- A clean, committed checkout at the commit you want to release.
- Green CI for that commit.
- The npm trusted-publisher connections for the five `@brokkai/brokk-town*`
  packages pointing at `BrokkAi/brokk-town`, the `release-town.yml` workflow, and
  the `packages-publish` environment, with direct publishing allowed. These are
  already configured. They only need changing if the workflow or a package name
  changes.
- Current license policy. `scripts/package_release.py` calls
  `scripts/licenses.py`, so a reviewed dependency change is part of the build.

## Before tagging

You can build the whole release locally without uploading anything:

```sh
python3 scripts/publish_tag.py --tag vX.Y.Z-town --sha "$(git rev-parse HEAD)" --check-only
```

This runs the same packaging code as the workflow and stops after the build. It
requires a clean checkout and writes nothing outside `dist/`.

## Rules

- Tags and published versions are immutable. If a release fails partway, fix the
  cause and push the next version; never move or delete a tag, and never publish
  over an existing version.
- Only maintainers create release tags.
- The eight bots ship inside this release. They have no separate version, tag or
  npm package, so a bot change is released by releasing Town.
- If a release depends on a new npm trusted-publisher connection, set it up and
  read it back before pushing the tag.

## Installing a release

```sh
npm install -g @brokkai/brokk-town@latest
sh install.sh vX.Y.Z-town
```

The shell installer resolves the latest stable npm version when given no
argument, downloads the matching archive, verifies its SHA-256 checksum, and
installs it under `INSTALL_DIR/.brokk-town`. See [README.md](README.md) for the
full install and upgrade notes.
