# Releasing repo-bot

This standalone project's source and releases live in `BrokkAi/brokk-town`.
Its workflow is `.github/workflows/release-repo-bot.yml` at the umbrella root.
Use tags `vX.Y.Z-repo-bot` (or `vX.Y.Z-rc.N-repo-bot`); npm versions omit
the project suffix. Existing package and CLI names are unchanged.

Run this project's Go race tests/vet, Python packaging tests, launcher tests and
license checks before releasing. Build without publishing using
`python3 scripts/publish_tag.py --tag TAG --sha SHA --check-only` from this directory.
See the umbrella `RELEASING.md` for trusted publishers and release authorization.
Do not run old repository release workflows or publish without a release request.
