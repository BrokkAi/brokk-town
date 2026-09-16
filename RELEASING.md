# Releasing brokk-town

Pushing a `v*` tag is the release request. The `Publish packages` workflow
builds, publishes, and verifies every destination from that tag automatically.
There are no dispatch flags, preflight tags, or manual upload steps.

## How to release

From a clean checkout of `origin/master` with CI green:

```sh
git tag vX.Y.Z
git push origin vX.Y.Z
```

Tag rules:

- The tag must be a version such as `v0.4.0` or `v0.4.0-rc.1`.
- Never move or delete a pushed tag. All five npm packages already treat a
  published version as immutable, and the workflow refuses a tag that points
  anywhere unexpected.
- Stable versions use npm `latest` and become GitHub's latest release.
  Prereleases (a `-` suffix) use npm `next` and never become latest.

## What the tag workflow does

`Publish packages` runs the shared CI checks first, then
`python3 scripts/publish_tag.py`:

1. Builds the four native archives (`brokk-town-vVERSION-{linux,darwin}-`
   `{amd64,arm64}.tar.gz`, plus `checksums.txt` and `release.json`) and the
   five npm packages (`@brokkai/brokk-town` plus four platform packages).
2. Runs the offline installer smoke test.
3. Stages the native archives in a draft GitHub release for the tag.
4. Publishes the platform packages before the launcher with npm provenance.
   Registry visibility can lag behind an accepted upload; submission and
   verification retry together instead of failing or resubmitting.
5. Verifies every downloaded asset, all five package payloads, each package's
   SLSA provenance bundle, the npm dist-tags, and GitHub's latest pointer.
6. Finalizes the GitHub release only after everything verifies.

The workflow's `packages-publish` environment and the npm trusted publishers
are bound to this repository and `publish-packages.yml`. Do not rename either
without updating the trusted-publisher configuration for all five packages.

## Recovery

Re-run the failed jobs from the Actions UI. Identical existing assets and
package versions are reused; anything conflicting fails closed for
investigation instead of overwriting. An `E409 previously staged version`
from npm means the upload was accepted but is not visible yet: the publisher
waits for visibility and skips identical bytes rather than resubmitting.

## Local validation

```sh
make check smoke
python3 scripts/smoke_installers.py
python3 scripts/publish_tag.py --check-only  # build plus local verify, no uploads
```
