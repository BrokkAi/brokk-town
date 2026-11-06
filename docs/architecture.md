# Architecture

Brokk Town is a long-running, shared service on Kubernetes. Everything else —
bots, agents, the browser, Mjolnir — is something it reaches through a defined
boundary. The bots sit behind the bot contract (`internal/botapi`), which
carries no path, command or type the service owns. Each bot is its own binary
in its own pod, and the service reaches it only over the network.

## Process model

```
                     +--------------------------+
   browser  <---->   |  internal/web HTTP server |
                     +------------+-------------+
                                  |
   bt CLI  <---->   +------------v-------------+     +------------------+
                     |  internal/town supervisor |---->|  ACP harness     |
                     |  store, scheduler, state  |     |  (local agent)   |
                     +------------+-------------+     +------------------+
                                  |                            |
                                  |  botclient: HTTP + NDJSON  |  remote runs
                                  v                            v
                     +--------------------------+     +------------------+
                     |  cmd/<bot> x 8 (botserver)|     |  Mjolnir daemon  |
                     |  one pod each, own volume |     +------------------+
                     |                          |
                     +--------------------------+
```

`cmd/bt` opens the state store and sign-in, resolves each bot's URL and token,
starts the managed-review callback listener, the supervisor, the web server,
the harness catalog, the Mjolnir catalog and the attention watcher, then waits
for a signal. On the way out it stops its workers before releasing the state
lock, so a canceled run can still report what it completed. The small CLI
(`bt status`, `bt web`) is the same binary, run in Town's pod, talking to the
running service's HTTP API on its loopback address with the operator's access
key from the state directory; the browser uses the same API.

## Packages

| Package | Responsibility |
| --- | --- |
| `cmd/bt` | Service entry point, process-control CLI, sign-in setup, bot endpoints. |
| `cmd/<bot>` | One binary per bot (`bug-bot`, `review-bot`, …): `botserver.Main` around that bot's `Worker`. |
| `internal/town` | The domain: state model, store, supervisor, scheduling, reconciliation, policies, funnels, recovery, history, storage, outcomes. |
| `internal/botapi` | The bot contract (request, progress and log events, result, storage, network frames and identity) and the bot-side `Worker` that resolves workspaces and agents where the bot runs. |
| `internal/botapi/botserver` | The HTTP side of a bot binary: authentication, bounded requests, the run stream, cancellation, storage, flags and environment. |
| `internal/botapi/botclient` | The service's side: a `botapi.Bot` over HTTP that validates the stream and classifies failures as unavailable or uncertain. |
| `internal/artifact` | Path-safety, measuring and digest checks shared by the service and the bots. |
| `internal/web` | HTTP routes and the embedded browser UI: plain HTML, CSS and ES modules. |
| `internal/auth` | Sign-in: GitHub and Google OAuth, the allowlist and signed session cookies. |
| `internal/harness` | ACP harness registry, catalog cache, archive download and launch. |
| `internal/mjolnir` | Mjolnir API client: catalog, profiles, placements, runtime pins, runs, artifacts, evidence and import. |
| `internal/guide` | One bounded ACP conversation for the Assistant. |
| `internal/durable` | fsync-based atomic file replacement. |
| `internal/filelock` | Advisory locks. |
| `internal/osrun` | Process control, signals and liveness. |
| `bots/*` | One package per bot. Each owns its prompt, configuration, GitHub access, durable state and the adapter (`worker.go`) from the contract to its own config. `bots/inprocess.go` lists them for tests that host them in the test's process; production never does. |

## State and persistence

All state lives in the state directory (`/data` on the `bt` pod's volume, or
`--state-dir`):

| Path | Contents |
| --- | --- |
| `state.json` | The whole snapshot: service config, repositories (`towns`), tasks (`tasks`), workers, events. |
| `daemon.lock` | Single-writer lock. A second service on the same directory refuses to start. |
| `token` | The operator's access key (0600). Delete it to rotate. |
| `session-key` | Signs the sign-in cookies (0600). Delete it to sign everyone out. |
| `connection.json` | Advertises the running service URL, PID and version to clients in the pod. |
| `towns/<key>/extensions/` | Town's own repair, audit and Mjolnir sessions. |
| `towns/<key>/history/` | Archived terminal tasks (History). |
| `mjolnir-options.json` | Cached Mjolnir launch catalog. |

`Store` is the only writer. Every change runs through `Store.Update`, which
clones the state, applies the change, validates it, and replaces `state.json`
atomically with fsync. Snapshots are immutable copies; clients never observe a
half-applied write. `Store.Open` also re-validates persisted state on startup and
repairs or reports the specific problems the model defines.

## Scheduling

The supervisor owns one worker per bot per repository and a shared capacity
limit (`service_config.max_workers`, default 4, maximum 64). Repo Bot observes
without holding an agent slot; it takes one only while repairing the branch.

Each bot has a cadence: a repository-wide `poll_seconds` for intake and repair
work, thirty minutes between the Bug Bot's and the Feature Bot's
discovery scans, five minutes for Review Bot and Release Bot. The scheduler wakes
work when the workload changes, not only on a timer. Release Bot additionally
follows its own cadence and burst rules; see [workflow.md](workflow.md).

## Dispatch

When a bot is due, the supervisor:

1. Re-reads the current state and resolves the bot's agent profile and
   execution placement at dispatch time, not from an older snapshot.
2. Checks holds — quiet hours, budget, capacity, a selected remote runtime, a
   recovery hold — and stops before starting anything if one applies.
3. Asks the bot for its identity (an unreachable bot is not sent the work),
   records the dispatch with the version the bot reported, and sends it one
   `botapi.Request` through `BotWorkers`: the repository key and role instead
   of directories, and the operator's agent selection (a command or a harness
   entry) instead of a resolved launch. Progress and log events stream back
   bounded; the bot reports the transcripts it wrote. A stream that breaks or
   breaks the contract makes the outcome uncertain (see
   [recovery.md](recovery.md#runs-on-a-bot-over-the-network)).
4. Commits the result: task moves, ownership, review audits, branch health,
   outcome records and budget charge.

An interrupted dispatch is recorded as a recovery hold rather than adopted. A
restart never continues a process it did not start.

## Bots

Each bot is a Go package with a `Config`, a `Run` (or a focused duty) and its own
private state directory. Its `worker.go` adapts a contract request into that
config; the shared `botapi.Worker` resolves the workspace under the bot's data
root, installs and launches the selected harness there, and carries progress,
logs and transcripts back. Town converts the typed result into the
`internal/town` model and validates it before anything is persisted. The storage
view asks each bot for its own inventory and sends transcript removals back to
it; the bot holds its state lock while it removes them. Town never reads a
bot's files directly. The repo bot's inventory is the pipeline's
observation step; Town itself additionally performs a few bounded,
idempotent GitHub writes (the merge, closing declined work, filing
follow-ups, submitting a request), each gated on a fresh read of exactly what it
is about to change.

See [bots.md](bots.md) for each bot's duties.

## Funnels

Source funnels normalise external work into one internal shape: a work identity,
provenance, priority, capabilities and lifecycle transitions. GitHub and Slack
are the two providers. A funnel can be read-only or map lifecycle actions onto
provider operations. Funnels never see credentials in state: a credential is a
reference resolved at request time. See [funnels.md](funnels.md).

## Web server

The HTTP server serves the embedded UI plus a JSON API at the public URL, behind
the Ingress that terminates TLS, and at its own loopback address for clients in
its pod. Every `/api/` request must be same-origin and carry a session cookie
or the operator's access key from the state directory. Responses are
`no-store`, and the server sets a strict content security policy. See
[operations.md](operations.md) for the route list.

`internal/auth` signs people in with GitHub or Google (authorization code with
PKCE), checks them against the configured allowlist and issues an HMAC-signed
session cookie; nothing about a session is stored. The web layer accepts that
cookie in place of the access key, refuses a session's writes without the
public origin, and keeps host commands with the access key. Writes that record operator events run through
`Store.As`, which runs them one at a time and stamps their operator events, and
an Assistant question, with the signed-in person. Everyone shares one state;
there is no per-person Town.

The embedded UI is plain HTML, CSS and ES modules with no build step, no
animation, no canvas drawing and no image assets. It renders the committed
snapshot and sends every command through the same API. `internal/web/bots.js`
owns the stage labels, summaries and pipeline order, keyed by the unchanged
role keys; the bot names are `botNames` in `internal/web/town.js`. The Overview
view shows summary metrics, the pipeline of bot cards, the open tasks and the
Activity feed; the Board view's columns come from
`internal/web/town.js`. `internal/web/style.css` defines the colors, type, radii and
shadows as design tokens. No view invents a task or sends a command on its own.

## Harnesses and Mjolnir

`internal/harness` resolves an ACP agent from the official registry, from the
bundled supplements (`anvil`, `muse-acp`, `draupnir`), or from a custom command.
It downloads and caches versioned archives atomically and launches without shell
interpolation. `internal/mjolnir` is the optional remote execution path: the
Town discovers targets, profiles and bundles from a loopback Mjolnir API, pins
an exact runtime, and can place review and issue-repair duties there. Other duties
are held rather than silently run locally. See [harnesses.md](harnesses.md) and
[mjolnir.md](mjolnir.md).

## Assistant

The Assistant (`internal/guide`, `/api/guide`) is a separate bounded ACP
conversation. It sees a context assembled from public state — worker summaries,
recent failures, task counts and recent tasks — and has no workspace tools and
no permission to write. It may propose pausing one bot (a `pause`); the
browser applies it only after the exact proposal digest is confirmed against
current state. See [operations.md](operations.md).

## townsim

`cmd/townsim` is a stochastic model of the scheduler. It reproduces the
supervisor's rules — one worker per bot, shared capacity, poll cadence,
discovery gaps, the Simplifier Bot ahead of the Mayor Bot, one repair round
per pull request, a closed pull request restarting its issue — and reports
whether a set of defaults can grow an unbounded backlog. It is a design tool,
not part of the service.
