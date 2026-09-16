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
3. Stages the native archives in a draft GitHub release for the tag,
   uploading only what is not already there.
4. Publishes the platform packages before the launcher with npm provenance,
   skipping versions already published with identical bytes.
5. Finalizes the GitHub release.

There is no read-back verification: upload exit codes gate each step, and a
re-run fills in whatever is still missing. Registry visibility can lag behind
an accepted upload; re-run until the run reports success.

The workflow's `packages-publish` environment and the npm trusted publishers
are bound to this repository and `publish-packages.yml`. Do not rename either
without updating the trusted-publisher configuration for all five packages.

## Recovery

Re-run the failed jobs from the Actions UI. Identical existing assets and
package versions are reused; anything conflicting fails closed for
investigation instead of overwriting. An `E409 previously staged version`
from npm means the upload was accepted but is not visible yet: re-run, and
the publisher skips it once the version record appears.

## Local validation

```sh
make check smoke
python3 scripts/smoke_installers.py
python3 scripts/publish_tag.py --check-only  # build, no uploads
```
