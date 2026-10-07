# Architecture

SlopCop Squad is a single long-running process. Everything else — bots, agents,
the browser, Mjolnir — is either a package it calls or a service it talks to.

## Process model

```
                     +--------------------------+
   browser  <---->   |  internal/web HTTP server |
                     +------------+-------------+
                                  |
   scs CLI  <---->   +------------v-------------+     +------------------+
                     |  internal/town supervisor |---->|  ACP harness     |
                     |  store, scheduler, state  |     |  (local agent)   |
                     +------------+-------------+     +------------------+
                                  |                            |
                                  |  in-process calls          |  remote runs
                                  v                            v
                     +--------------------------+     +------------------+
                     |  bots/* Go packages      |     |  Mjolnir daemon  |
                     +--------------------------+     +------------------+
```

`cmd/scs` opens the state store, starts the supervisor, the web server, the
harness catalog, the Mjolnir catalog and the attention watcher, then waits for a
signal or `scs shutdown`. The small CLI (`scs status`, `scs web`, …) is the same
binary talking to the running service over a loopback HTTP API using a token from
the state directory; the browser uses the same API.

## Packages

| Package | Responsibility |
| --- | --- |
| `cmd/scs` | Service entry point, process-control CLI, background start, demo seeding. |
| `internal/town` | The domain: state model, store, supervisor, scheduling, reconciliation, policies, funnels, recovery, history, storage, outcomes. |
| `internal/web` | HTTP routes and the embedded browser UI: plain HTML, CSS and ES modules. |
| `internal/harness` | ACP harness registry, catalog cache, archive download and launch. |
| `internal/mjolnir` | Mjolnir API client: catalog, profiles, placements, runtime pins, runs, artifacts, evidence and import. |
| `internal/guide` | One bounded ACP conversation for the Desk Sergeant. |
| `internal/durable` | fsync-based atomic file replacement. |
| `internal/filelock` | Cross-platform advisory locks. |
| `internal/osrun` | Cross-platform process control, signals and liveness. |
| `bots/*` | One package per bot. Each owns its prompt, configuration, GitHub access and durable state. |

## State and persistence

All state lives in the state directory (default `$XDG_STATE_HOME/slopcop-squad`, or
`%LOCALAPPDATA%\slopcop-squad` on Windows):

| Path | Contents |
| --- | --- |
| `state.json` | The whole snapshot: service config, precincts (`towns`), cases (`tasks`), workers, events. |
| `daemon.lock` | Single-writer lock. A second service on the same directory refuses to start. |
| `token` | The loopback API access key (0600). Delete it to rotate. |
| `connection.json` | Advertises the running service URL, PID and version to clients. |
| `logs/` | Background service stdout and stderr. |
| `towns/<key>/<role>/checkout` | A precinct's private git checkout for that unit. |
| `towns/<key>/<role>/state` | That unit's durable bot state. |
| `towns/<key>/history/` | Archived terminal cases (the Case archive). |
| `mjolnir-options.json` | Cached Mjolnir launch catalog. |

`Store` is the only writer. Every change runs through `Store.Update`, which
clones the state, applies the change, validates it, and replaces `state.json`
atomically with fsync. Snapshots are immutable copies; clients never observe a
half-applied write. `Store.Open` also re-validates persisted state on startup and
repairs or reports the specific problems the model defines.

## Scheduling

The supervisor owns one worker per unit per precinct and a shared capacity
limit (`service_config.max_workers`, default 4, maximum 64). Repo Bot observes
without holding an agent slot; it takes one only while repairing the branch.

Each unit has a cadence: a precinct-wide `poll_seconds` for intake and repair
work, thirty minutes between the Bug Detective's and the Feature Detective's
discovery scans, five minutes for Forensics and Release. The scheduler wakes
work when the caseload changes, not only on a timer. Release additionally
follows its own cadence and burst rules; see [workflow.md](workflow.md).

## Dispatch

When a unit is due, the supervisor:

1. Re-reads the current state and resolves the unit's agent profile and
   execution placement at dispatch time, not from an older snapshot.
2. Checks holds — quiet hours, budget, capacity, a selected remote runtime, a
   recovery hold — and stops before starting anything if one applies.
3. Calls the bot package in process through `BotWorkers`, streaming bounded
   progress and log lines into durable state.
4. Commits the result: case moves, ownership, review audits, branch health,
   outcome records and budget charge.

An interrupted dispatch is recorded as a recovery hold rather than adopted. A
restart never continues a process it did not start.

## Bots

Each bot is a Go package with a `Config`, a `Run` (or a focused duty) and its own
private state directory. The Squad adapts the precinct's configuration and work
policy into that package's config, runs it, and converts its typed observation
back into the `internal/town` model. The repo bot's inventory is the pipeline's
observation step; the Squad itself additionally performs a few bounded,
idempotent GitHub writes (the merge, closing work sent to the Slop Tank, filing
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

The HTTP server binds a loopback address and serves the embedded UI plus a JSON
API. Every `/api/` request must be same-origin and carry
`Authorization: Bearer <token>`; the token is the local access key in the state
directory. Responses are `no-store`, and the server sets a strict content
security policy. See [operations.md](operations.md) for the route list.

The embedded UI is plain HTML, CSS and ES modules with no build step, no canvas
drawing and no image assets. It renders the committed snapshot and sends every
command through the same API. `internal/web/precinct.js` owns the precinct
vocabulary — unit names, callsigns and roster order — keyed by the unchanged
role keys. The Precinct view is a cutaway drawing of the precinct house with
the courthouse attached: `internal/web/scene.js` lays out the rooms, stands each
unit's officer at its post and each open case in the room of the unit holding
it, compares consecutive committed snapshots to see which cases moved, arrived
or left, and choreographs the walks, vehicles and stamps.
`internal/web/animation.js` draws the cast as inline SVG — the robot officers,
the slop (loose and cuffed), the reformed citizens and the vehicles.
Neither invents a case or sends a command, and both stop animating when motion
is off. The Board view's case-flow columns come from `internal/web/town.js`.

## Harnesses and Mjolnir

`internal/harness` resolves an ACP agent from the official registry, from the
bundled supplements (`anvil`, `muse-acp`, `draupnir`), or from a custom command.
It downloads and caches versioned archives atomically and launches without shell
interpolation. `internal/mjolnir` is the optional remote execution path: the
Squad discovers targets, profiles and bundles from a loopback Mjolnir API, pins
an exact runtime, and can place review and issue-repair duties there. Other duties
are held rather than silently run locally. See [harnesses.md](harnesses.md) and
[mjolnir.md](mjolnir.md).

## Desk Sergeant

The Desk Sergeant (`internal/guide`, `/api/guide`) is a separate bounded ACP
conversation. It sees a context assembled from public state — worker summaries,
recent failures, case counts and recent cases — and has no workspace tools and
no permission to write. It may propose standing down one unit (a `pause`); the
browser applies it only after the exact proposal digest is confirmed against
current state. See [operations.md](operations.md).

## Demo and townsim

`scs --demo` runs a closed simulation in a separate state directory: it seeds two
precincts and never receives a GitHub client, an Mjolnir connection or a worker
runner. It is safe to run anywhere.

`cmd/townsim` is a stochastic model of the scheduler. It reproduces the
supervisor's rules — one worker per unit, shared capacity, poll cadence,
discovery gaps, the Magistrate ahead of the Probation Judge, one repair round
per pull request, a closed pull request restarting its issue — and reports
whether a set of defaults can grow an unbounded backlog. It is a design tool,
not part of the service.
