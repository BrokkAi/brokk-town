# Releasing brokk-town

## Destinations and order

The first stable candidate is **v0.1.0**. All deliverables use the exact same
committed inputs. Never move a published version tag or overwrite conflicting
artifacts. All five npm packages already have `0.1.0-rc.1`, manually bootstrapped
under `next` from `cfa1a68754e565b61894bb5de33558ee05b169f7`; these are historical
published artifacts, not a stable release or permission evidence for a new job.

1. GitHub tag `vVERSION` in `BrokkAi/brokk-town`. It is also the Go module version
   for `github.com/BrokkAi/brokk-town`. There is no separate Go registry upload.
2. A GitHub draft release stages four native archives:
   `brokk-town-vVERSION-{linux,darwin}-{amd64,arm64}.tar.gz`, plus `checksums.txt`
   and `release.json`. Each archive contains `bt`, exact commit/tag/platform
   metadata in `BUILD.json`, the README, license, notices and dependency report.
3. npm platform packages at `VERSION`, in any order:
   `@brokkai/brokk-town-linux-x64`, `@brokkai/brokk-town-linux-arm64`,
   `@brokkai/brokk-town-darwin-x64`, `@brokkai/brokk-town-darwin-arm64`.
4. npm launcher `@brokkai/brokk-town` at `VERSION`, whose optional dependencies
   pin all four platform packages to that exact version.
5. npm's automatic OIDC provenance uses Sigstore Fulcio certificate issuance and
   Rekor transparency logging. These signing/attestation services are additional
   external destinations; metadata reads alone do not prove signing authority.
6. Finalize the GitHub release only after all npm packages are independently
   downloaded and verified. Stable npm versions use `latest`; prereleases use
   `next`. These dist-tags and GitHub's latest-release endpoint feed installers.

The repository's `package.json` is explicitly private and only runs frontend
checks. There are no crates.io, PyPI, Maven, container, separate update-feed,
notarization or documentation deployment targets. The browser is embedded in
`bt`; keep the application local. GitHub's automatically generated source
archives and the Go proxy derive from the immutable source tag.

## Preflight: no publication

Prepare on the job's unique release topic branch, fetch and merge origin/master
when necessary, and deliver changes through a PR. Do not push a tag, dispatch
`publish=true`, upload final release assets or packages, or bypass approvals.
Only branch pushes trigger `CI`; `Publish packages` publishes on `v*` tag pushes
or an explicit `publish=true` dispatch from the exact existing tag. `Release`
is a reusable build workflow and has no publishing permissions or upload steps
for final release assets. Actions candidate artifacts are build evidence only.

Run from a clean committed checkout with Go from `go.mod`, Node.js 24 and Python:

```sh
make check smoke
python3 scripts/licenses.py
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12
export RELEASE_COMMIT="$(git rev-parse HEAD)"
export RELEASE_TAG=v0.1.0
python3 scripts/release_checks.py build --directory dist/candidate
python3 scripts/release_checks.py version --directory dist/candidate
```

Build requires a new output directory and performs native packaging, package
metadata/license/integrity validation and a real offline npm install/launch.
`make check smoke` includes race tests, vet, frontend checks/tests, package tests
and isolated demo HTTP/PTY integration; it never invokes live agents or GitHub.

After the preparation PR is actually merged, fetch origin/master and detach at
its actual merged commit in this checkout. Keep historical topic branches. Then:

```sh
gh workflow run publish-packages.yml --repo github.com/BrokkAi/brokk-town \
  --ref master -f tag=v0.1.0 -F publish=false
```

Check the returned run's exact SHA: master can advance concurrently. A run at
another commit cannot validate this candidate. Select the job's retained topic
branch at the merged commit for dispatch if needed, without force-pushing.
The non-publishing path builds all deliverables before entering the actual
`packages` publishing job in `packages-publish`. It does not require an existing
tag, public release or registry version. It checks all version conflicts and
uses only an asset-free disposable private GitHub draft to test contents-write
permission, deleting the probe immediately. Deleting the probe does not
invalidate successful evidence or remove actual release staging state.

The npm check obtains the publishing job's GitHub OIDC identity, validates its
commit/workflow/environment/expiry, exchanges it separately for all five
package-scoped npm tokens and reads direct-publish trust (`createPackage`).
Staging-only permission, failed trust reads and unknown evidence fail closed.
Tokens and identity documents are never printed or saved as build artifacts.
See [npm's registry API](https://api-docs.npmjs.com/) and
[trusted publishing](https://docs.npmjs.com/trusted-publishers/).

## Current authorization blockers

**This preparation is not ready for publication.** On 2026-09-10, GitHub reported
that `packages-publish` permits only `v*` tags, and no repository tags existed.
An administrator must explicitly authorize a reviewed preflight branch in this
same environment, retaining any required reviewers, before the non-publishing
job can exercise its actual credentials. Do not create a tag to test access or
silently broaden the deployment policy. Workflow/token changes use the PR path.

The documented prior npm trust setup is recovery evidence only. A local
`npm trust list @brokkai/brokk-town --json` returned 401. An npm package maintainer
must confirm all five trusted publishers permit direct publication from
`BrokkAi/brokk-town`, `publish-packages.yml`, `packages-publish`. If npm refuses
the trust read using exchanged OIDC tokens, provide a supported way for the
same Actions context to inspect that permission; do not upload to test it.

Sigstore authorization still needs a supported non-publishing check for both
Fulcio and Rekor, plus independent provenance verification. The authorization
entry point explicitly reports this unimplemented gate and prevents every final
upload. Merely receiving an OIDC identity, reading public service metadata or
running `npm publish --dry-run` is insufficient. Preserve provenance rather than
turning it off to manufacture readiness. No signing probe or transparency-log
entry was submitted during this preparation.

## Evidence commands

For a successful exact-commit dispatch, substitute its numeric run ID below.
The daemon provides `RELEASE_COMMIT`, `RELEASE_TAG`, and `RELEASE_TARGET`.

```sh
python3 scripts/release_checks.py remote --gate build --run-id RUN_ID
python3 scripts/release_checks.py remote --gate authorization --run-id RUN_ID
python3 scripts/release_checks.py remote --gate version --run-id RUN_ID
```

Each command rejects failed, missing, skipped, incomplete, wrong-event and
wrong-commit jobs and missing/expired candidate artifacts. Authorization evidence
expires after 15 minutes and needs a fresh non-publishing publisher job. Version
checks contact the remote destinations again. Build evidence contains exact
commit/tag/platform/package metadata and tools; it contains no credentials.
Required workflows are `ci.yml`, `release.yml` (called by the release pipeline),
and `publish-packages.yml`. A successful ordinary CI run does not establish
publishing authorization. Pending environment approval is a blocker, not success.

## Publication and recovery (only after a separate publication request)

Resolve every authorization blocker first. The publishing job repeats all
version and credential checks before its first final upload. Publish the new tag
at the prepared commit and follow `Publish packages`. If a tag is created with
`GITHUB_TOKEN`, GitHub does not necessarily start another workflow: dispatch
`publish-packages.yml` explicitly from that exact tag with its tag input and
`publish=true`. Never assume an absent workflow run is successful.

`scripts/publish_release.py` recreates missing real staging independently of
probe state, retains matching uploaded assets, refuses conflicts, uploads missing
native assets, checks downloaded bytes against staged bytes, publishes platform
packages before the launcher, waits for registry visibility, then finalizes
GitHub. A completed release goes straight to read-only verification. Do not
finalize a draft manually to hide an incomplete package publication.

Resume a partial run from the exact existing tag; do not move it. Full existing
native archives and npm tarballs are validated against their own manifests and
registry checksums, then compared with expected unpacked files and permissions.
Compressor byte differences are acceptable; different binaries, metadata,
licenses or executable modes are not. A partial draft without a complete
manifest requires identical staged bytes for each existing asset; conflicts
require investigation. Successful npm uploads must not be blindly resubmitted.

After publication, download the exact workflow candidate, then run:

```sh
python3 scripts/release_checks.py published --directory dist/candidate
```

This verifies the exact tag commit, public finalized GitHub release, every native
asset and all five npm packages. Independent Sigstore attestation verification
must be added before this can certify the full destination list. A missing,
partial or differing destination is an error even if the GitHub release exists.
