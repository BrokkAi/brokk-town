# AGENTS.md

Instructions for automated and human contributors working in this repository.

## What this repository is

SlopCop Squad is one Go module. `cmd/sct` is the whole product: a local service
with an embedded browser UI and a small process-control CLI. The eight bots live
under `bots/` as packages of this module and are called in process, not over a
socket or a separate binary. There is no worker protocol, no per-bot module,
and no per-bot release. If you find code or docs that describe one, it is stale.

## Layout

| Path | Contents |
| --- | --- |
| `cmd/sct` | Service entry point, process-control CLI, background start. |
| `cmd/townsim` | A stochastic scheduler model used to sanity-check defaults. |
| `internal/town` | State model, store, supervisor, scheduling, funnels, recovery, storage. |
| `internal/web` | HTTP server, embedded assets and the browser UI. |
| `internal/harness` | ACP harness catalog, download and launch. |
| `internal/mjolnir` | Mjolnir catalog, profiles, runtime pins, remote runs and evidence. |
| `internal/guide` | Desk Sergeant agent session. |
| `internal/durable`, `internal/filelock`, `internal/osrun` | Durable writes, locking and process control. |
| `bots/*` | One package per bot; each owns its prompt, config and durable state. |
| `scripts/` | Generates the license file attached to a release. |
| `docs/` | Operator and developer documentation. |
| `licenses/` | Generated third-party notices and supplementary license texts. |

## Build and test

```sh
make build     # go build -o bin/sct ./cmd/sct
make test      # go test -race ./... and the browser JavaScript tests
make js        # node --check every browser source, then node --test
make check     # full gate: race tests, browser tests and vet
```

`make check` is the gate. Run it before claiming a change is done. When you are
only touching one area, the fast loop is:

```sh
go test -race ./internal/town/...      # or the package you changed
go vet ./...
make js                                # browser JavaScript
```

CI (`.github/workflows/ci.yml`) runs the race-enabled Go checks on Linux for
pull requests and on Linux and macOS for `master`, a build, vet and test pass
on Windows for `master`, plus the browser and workflow-lint checks.

## Working rules

- Keep the change focused. Do not reformat, rename or "clean up" code outside
  the area you were asked to change.
- Write the test first when fixing a bug. Tests must fail on the old behaviour.
- Use fakes and the demo mode. Never point a test at a live GitHub repository, a
  real agent session, or a published registry.
- Do not commit generated artifacts (`bin/`, `dist/`, `var/`, `__pycache__/`).
- Treat another checkout or worktree as read-only. Do not reset, clean, stash,
  force-push or delete work you did not create.
- Never print, commit or log credentials. The Squad scrubs known token shapes
  from worker logs; do not rely on that and avoid putting secrets in test
  fixtures.

## Go conventions

- Go 1.27.1, standard library first. `gofmt` is enforced by CI.
- Wrap errors with context: `fmt.Errorf("...: %w", err)`. Return errors; do not
  panic outside `main`/startup.
- Keep state changes inside `Store.Update`. Durable writes go through
  `internal/durable`; never write `state.json` directly.
- Validate anything that enters persisted state, and keep private data (tokens,
  command vectors, absolute paths) out of public projections.
- Tests use the race detector. Prefer table-driven tests and explicit fakes over
  timing or sleeps.

## Browser conventions

- `internal/web/*.js` is plain ES modules with no build step. `make js` runs
  `node --check` on every file and then the Node test runner. The root
  `package.json` exists only to mark those files as ES modules; there is no npm
  package to install or publish.
- Render from committed state. The UI never invents state and never triggers a
  write that the service did not record.
- Keep CSS in `internal/web/style.css`. The precinct vocabulary and unit
  metadata — unit names, callsigns, roster order and case-flow lanes, keyed by
  the unchanged role keys — live in `internal/web/precinct.js`.
- The cartoon layer — robot cops, squad cars, stamps and the slop in the Slop
  Tank — lives in `internal/web/animation.js` as inline SVG and CSS. It only
  decorates committed state, never sends a command, and stops when Motion is
  off or the OS asks for reduced motion.
- There are no themes, no canvas drawing and no image assets. The UI is plain
  HTML, CSS and ES modules.
- User-facing text speaks the precinct vocabulary (the Squad, precinct, unit,
  case, Courthouse, ruling, blotter, CPT); API fields, config keys, role keys
  and state values keep their original names (`town`, `house`, `task`,
  `operator`, …). The product is always "SlopCop Squad" or "the Squad", never
  just "SlopCop". See the glossary in `docs/workflow.md`.

## Documentation

`docs/` is the source of truth for operators. When behaviour changes, update the
affected page in the same change:

- `docs/configuration.md` for settings, policies and config-file fields.
- `docs/operations.md` for process commands, the browser and the local API.
- `docs/workflow.md` for how work moves between units, and the glossary.
- `docs/architecture.md` for the internal structure.
- `docs/recovery.md`, `docs/storage.md` for interruption and retention.

## Releasing

A release is a tag push: `git tag vX.Y.Z && git push origin vX.Y.Z`. The workflow
runs GoReleaser, which builds the archives, checksums them and publishes the
GitHub release. Do not create tags or push to a release branch unless the user
explicitly asked for a release.
