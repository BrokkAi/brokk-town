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

Town packages contain only bt. Bots retain independent versions and npm package
families; Town resolves the latest stable release at worker startup and records
that exact version for the worker lifetime. Bot releases do not require a Town
release or a shared version manifest. Publish a bot's platform packages before
its launcher advances the stable channel.

Every bot release must keep the existing `/v1` API and capabilities working for
older Town clients. Breaking APIs get a new endpoint alongside the old one;
never repurpose `/v1` or raise its minimum version to retire existing clients.
The inherited parent-pipe contract also remains supported for existing Town
releases. CI runs the v1 protocol, lifecycle and local npm integration checks on
every bot release. Add tests for an API before adopting it, retain the older
contract tests, and fail the release if either contract regresses.

For this transition, release the bots with parent-socket support before releasing
Town's npx startup change. Release publication remains an explicit user action.

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
