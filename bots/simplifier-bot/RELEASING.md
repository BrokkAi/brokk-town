# Releasing simplifier-bot

This standalone project's source and releases live in `BrokkAi/brokk-town`.
Its workflow is `.github/workflows/release-simplifier-bot.yml` at the umbrella root.
Use tags `vX.Y.Z-simplifier-bot` (or `vX.Y.Z-rc.N-simplifier-bot`); npm versions omit
the project suffix. Existing package and CLI names are unchanged.

Run this project's Go race tests/vet, Python packaging tests, launcher tests and
license checks before releasing. Build without publishing using
`python3 scripts/publish_tag.py --tag TAG --sha SHA --check-only` from this directory.
See the umbrella `RELEASING.md` for trusted publishers and release authorization.
Do not run old repository release workflows or publish without a release request.

Every release must retain previously released worker API versions and their
capabilities. Add breaking APIs alongside older endpoints. Run the umbrella
CI checks, including the v1 contract and offline npm lifecycle smoke, before
publishing. Existing Town clients must keep working with the new bot release.
