# AGENTS.md

Instructions for automated and human contributors working in this repository.

## What this repository is

Brokk Town is one Go module. `cmd/bt` is the service: an embedded browser UI
and a small process-control CLI. The eight bots live under `bots/` as packages
of this module, and each runs as its own binary (`cmd/<bot>`) and container
image. The service reaches them only over the network through the bot contract
in `internal/botapi`: a JSON request naming the workspace (repository key and
role, never a path), a stream of progress and log events, and a typed result.
The service must not depend on sharing a process, filesystem, `PATH` or `gh`
login with a bot.

Town runs only on Kubernetes (`deploy/kubernetes`), on Linux, and only as a
shared Town: people sign in with GitHub or Google, and the operator's access
key is for clients in Town's pod. `bt` is given each bot's URL and token; it
never starts a bot itself. There is no demo mode, no docker compose
deployment and no desktop or single-user mode; do not add them back.

## Layout

| Path | Contents |
| --- | --- |
| `cmd/bt` | Service entry point, process-control CLI (`status`, `web`, `version`), sign-in setup, bot endpoints. |
| `cmd/<bot>` | One binary per bot, serving it over the network contract. |
| `cmd/townsim` | A stochastic scheduler model used to sanity-check defaults. |
| `internal/town` | State model, store, supervisor, scheduling, funnels, recovery, storage. |
| `internal/botapi` | The bot contract: request, events, result and storage operations, plus the bot-side worker every bot runs. `botserver` serves it over HTTP, `botclient` reaches it. |
| `internal/artifact` | Path-safety, measuring and digest checks shared by the service and the bots. |
| `internal/web` | HTTP server, embedded assets and the browser UI. |
| `internal/auth` | Sign-in: GitHub and Google OAuth, allowlist, session cookies. |
| `internal/harness` | ACP harness catalog, download and launch. |
| `internal/mjolnir` | Mjolnir catalog, profiles, runtime pins, remote runs and evidence. |
| `internal/guide` | Assistant agent session. |
| `internal/durable`, `internal/filelock`, `internal/osrun` | Durable writes, locking and process control. |
| `bots/*` | One package per bot; each owns its prompt, config, durable state and its `worker.go` adapter onto the contract. `bots/inprocess.go` hosts them in a test's process. |
| `Dockerfile`, `deploy/kubernetes/` | Images (one target per bot and `bt`) and the Kubernetes manifests, the only deployment. |
| `scripts/` | Generates the license file attached to a release and carried by the images. |
| `docs/` | Operator and developer documentation. |
| `licenses/` | Generated third-party notices and supplementary license texts. |

## Build and test

```sh
make build     # bin/bt and the eight bot binaries
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

CI (`.github/workflows/ci.yml`) runs the race-enabled Go checks, vet, the
build and the browser checks on Linux, plus workflow lint. Its `deploy` job
validates the Kubernetes manifests, builds every image, deploys them to a kind
cluster with placeholder sign-in, and checks that Town starts and its pod
reaches all eight bots with their tokens.

## Working rules

- Keep the change focused. Do not reformat, rename or "clean up" code outside
  the area you were asked to change.
- Write the test first when fixing a bug. Tests must fail on the old behaviour.
- Use fakes and the in-process bots. Never point a test at a live GitHub
  repository, a real agent session, or a published registry.
- Do not commit generated artifacts (`bin/`, `dist/`, `var/`, `__pycache__/`).
- Treat another checkout or worktree as read-only. Do not reset, clean, stash,
  force-push or delete work you did not create.
- Never print, commit or log credentials. Town scrubs known token shapes
  from worker logs; do not rely on that and avoid putting secrets in test
  fixtures.

## Go conventions

- Go 1.27.1, standard library first. `gofmt` is enforced by CI.
- Wrap errors with context: `fmt.Errorf("...: %w", err)`. Return errors; do not
  panic outside `main`/startup.
- Reach a bot only through `internal/botapi`. Outside tests, only
  `bots/inprocess.go` and each `cmd/<bot>/main.go` (for its own bot) may import
  a bot package (a test enforces it); Town's integration tests host the real
  bots through `inProcessBots`, and its network tests through `botserver` and
  `botclient`. The service never reads a bot's files, sends it a host path, or
  resolves a command for it to run: it sends a harness selection and the bot
  resolves it where it runs.
- A change to the contract must work over the network: values are plain JSON,
  bounded, and an older peer must not misread them (raise `botapi.Version` if it
  would). Never log a bot token or a callback token.
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
- Keep CSS in `internal/web/style.css`. Colors, type, radii and shadows are
  design tokens on `:root`; use them rather than literal values. Bot stage
  labels, summaries and pipeline order, keyed by the unchanged role keys, live
  in `internal/web/bots.js`; the bot names are `botNames` in
  `internal/web/town.js`.
- The Overview view shows summary metrics, the pipeline of eight bot cards and
  the Activity feed; the Board and Compact views show the same snapshot as
  columns and as a dense list. There is no animation, no canvas drawing, no
  themes and no image assets. The UI is plain HTML, CSS and ES modules.
- User-facing text is plain and professional: repository, bot, task, decision,
  accepted, declined, status report, Assistant, Activity. API fields, config
  keys, role keys and state values keep their original names (`town`, `house`,
  `task`, `hall`, `mayoral_decision`, `operator`, …). The product is "Brokk
  Town" or, for short, "Town". See the glossary in `docs/workflow.md`.

## Documentation

`docs/` is the source of truth for operators. When behaviour changes, update the
affected page in the same change:

- `docs/configuration.md` for settings, policies and config-file fields.
- `docs/operations.md` for the Kubernetes deployment, sign-in, process
  commands, the browser and the API.
- `docs/workflow.md` for how work moves between bots, and the glossary.
- `docs/architecture.md` for the internal structure.
- `docs/recovery.md`, `docs/storage.md` for interruption and retention.

## Releasing

Releases come from the nightly workflow (`.github/workflows/nightly.yml`). Each
night, if `master` has commits the latest release does not, it runs CI. When
that is green it tags the next minor version and runs the release workflow,
which pushes the nine container images for `linux/amd64` and `linux/arm64` and
then publishes the GitHub release with the third-party license file. When it is red it opens a `Nightly failed at
<commit>` issue with the failed jobs and their errors. A manual tag push
(`git tag vX.Y.Z && git push origin vX.Y.Z`) still publishes a release. Do not create tags or push to a release branch unless the user
explicitly asked for a release.
