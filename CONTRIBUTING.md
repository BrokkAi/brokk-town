# Contributing to Brokk Town

Thanks for helping. Town is one Go module that runs a local service, an
embedded browser UI and eight in-process bots. This document covers the working
agreement; [AGENTS.md](AGENTS.md) has the same material in a shorter form for
automated contributors.

## Before you start

- For a bug, open an issue with the version (`bt version`), your platform, the
  exact command or browser action, and what you expected instead.
- For a behaviour change, open an issue first so the design is agreed before the
  code. Small fixes and documentation changes can go straight to a pull request.
- Report security problems privately as described in [SECURITY.md](SECURITY.md).

## Set up

```sh
git clone https://github.com/BrokkAi/brokk-town.git
cd brokk-town
make build
export PATH="$PWD/bin:$PATH"
```

You need Go 1.27.1 or newer, Node.js 24 for the browser tests, and Python 3 for
the packaging and license tooling. `gh` is only needed to exercise real
repositories; tests use fakes and the demo service.

## The gate

```sh
make check
```

This runs, in order: `go test -race ./...`, `npm test`, `python3
scripts/licenses.py`, `go vet ./...`, a shell syntax check of `install.sh`, the
npm launcher tests, `npm run check` (browser syntax), and the Python unit tests
under `scripts/`. Run it before you push. `make smoke` additionally builds the
binary and runs the isolated demo integration checks, including the Mjolnir
harness.

## What we look for

- **Focused changes.** One concern per pull request. Do not reformat or rename
  code outside the change.
- **Tests that prove the fix.** A bug fix should include a test that fails before
  the fix. New behaviour needs coverage for the success path and the failure
  paths that matter — refusals, cancellation, uncertain outcomes and restart.
- **No live side effects.** Tests must never use a real GitHub repository, a live
  agent, a published registry, or your personal credentials. Use the fakes in
  `internal/town`, the demo service (`bt --demo`), or the fixture servers the
  existing tests already use.
- **Privacy.** Worker logs, snapshots and API responses must not leak tokens,
  credential-file contents, absolute local paths, or agent command vectors. If
  you add a field, decide whether it belongs in the public projection.
- **Durable state discipline.** All state changes go through `Store.Update`.
  Writes must be atomic and recoverable; an interrupted write becomes an
  explicit, inspectable hold rather than a silent retry.

## Pull requests

1. Branch from `master`.
2. Keep commits coherent and write a message that explains why the change is
   needed, not just what it does.
3. Make sure `make check` passes and CI is green.
4. Describe the user-visible effect, any migration or state-format impact, and
   how you validated it.

Reviewers check for correctness first, then for the shared-state and privacy
rules above, then for documentation. If your change alters configuration,
commands or the work pipeline, update the matching file under `docs/` in the same
pull request.

## Documentation

`docs/` is written for operators. The mapping from behaviour to page is listed in
[AGENTS.md](AGENTS.md). The short version:

- settings and policies → `docs/configuration.md`
- commands, browser, API → `docs/operations.md`
- pipeline and houses → `docs/workflow.md`
- internals → `docs/architecture.md`

## Releases

Maintainers publish by pushing a `vX.Y.Z-town` tag; see [RELEASING.md](RELEASING.md).
Contributors do not need to do anything for a release and should not create tags.

## Code of conduct

Participation is covered by [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).
