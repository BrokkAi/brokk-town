# Releasing Town and standalone bots

Each project has a separate `.github/workflows/release-PROJECT.yml` workflow.
Push `vX.Y.Z-town` for Town or `vX.Y.Z-BOT-NAME` for one bot, for example
`v0.5.5-issue-bot`. Prereleases put the prerelease before the project suffix:
`v0.5.5-rc.1-issue-bot`. Package versions omit the project suffix.

Release only when explicitly requested, from a clean committed checkout with
passing CI. Tags and published versions are immutable. The workflow runs CI,
builds native assets and npm packages, then publishes from the exact tagged
commit. Only stable Town releases can update GitHub's repository-wide latest
pointer. Installers resolve each project's npm stable version explicitly.

Town packages include all eight bots built independently from that same checkout.
Update a changed bot's version in `bundle.json` before a Town release; unchanged
bots retain their versions. A bot release must match that bot's manifest version.
Standalone bot releases publish only their own package family. The original bot
repositories are no longer release sources.

For local builds use `make build`. For a non-publishing release build after
committing preparation changes:

```sh
python3 scripts/publish_tag.py --tag vX.Y.Z-town --sha "$(git rev-parse HEAD)" --check-only
# From a bot directory, use its own scripts/publish_tag.py and suffix tag.
```

## Publishing connections

All 45 npm publisher connections were updated and read back on 2026-09-22:
Town and all eight bot families, each with a launcher and four platform packages.
They trust `BrokkAi/brokk-town`, their `release-PROJECT.yml` workflow, and the
`packages-publish` environment. Town uses `release-town.yml`; bots use their
project names, such as `release-issue-bot.yml`. Direct publishing is enabled for
the GitHub Actions workflows; existing staging permissions were preserved.

When adding a package or renaming a workflow, update its connection before
releasing. npm authentication on a maintainer machine is for managing this trust;
release package publication runs in GitHub Actions.

Inspect the installed CLI's `npm trust --help` and list existing connections with
`npm trust list PACKAGE --json`. npm's CLI supports create, list and revoke, not
edit: preserve connection details, revoke the old connection and recreate it with
the new repository/workflow. Allow direct publishing for these workflows:

```sh
npm trust github PACKAGE --file release-PROJECT.yml --repo BrokkAi/brokk-town --env packages-publish --allow-publish -y --json
```

Then list again to verify. npm may require interactive browser authentication.
Do not print or store credentials or authentication callback URLs.

Release Bot's standalone Python distribution builder is retained; its new bot
workflow publishes npm. Python publication remains an explicit separate action.
