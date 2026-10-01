# Architecture

Town is a single long-running process. Everything else — bots, agents, the
browser, Mjolnir — is either a package it calls or a service it talks to.

## Process model

```
                     +--------------------------+
   browser  <---->   |  internal/web HTTP server |
                     +------------+-------------+
                                  |
   bt CLI   <---->   +------------v-------------+     +------------------+
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

`cmd/bt` opens the state store, starts the supervisor, the web server, the
harness catalog, the Mjolnir catalog and the attention watcher, then waits for a
signal or `bt shutdown`. The small CLI (`bt status`, `bt web`, …) is the same
binary talking to the running service over a loopback HTTP API using a token from
the state directory; the browser uses the same API.

## Packages

| Package | Responsibility |
| --- | --- |
| `cmd/bt` | Service entry point, process-control CLI, background start, demo seeding. |
| `internal/town` | The domain: state model, store, supervisor, scheduling, reconciliation, policies, funnels, recovery, history, storage, outcomes. |
| `internal/web` | HTTP routes and the embedded browser UI. |
| `internal/harness` | ACP harness registry, catalog cache, archive download and launch. |
| `internal/mjolnir` | Mjolnir API client: catalog, profiles, placements, runtime pins, runs, artifacts, evidence and import. |
| `internal/guide` | One bounded ACP conversation for Town Guide. |
| `internal/durable` | fsync-based atomic file replacement. |
| `internal/filelock` | Cross-platform advisory locks. |
| `internal/osrun` | Cross-platform process control, signals and liveness. |
| `bots/*` | One package per bot. Each owns its prompt, configuration, GitHub access and durable state. |

## State and persistence

All state lives in the state directory (default `$XDG_STATE_HOME/brokk-town`, or
`%LOCALAPPDATA%\brokk-town` on Windows):

| Path | Contents |
| --- | --- |
| `state.json` | The whole town snapshot: service config, towns, tasks, workers, events. |
| `daemon.lock` | Single-writer lock. A second service on the same directory refuses to start. |
| `token` | The loopback API access key (0600). Delete it to rotate. |
| `connection.json` | Advertises the running service URL, PID and version to clients. |
| `logs/` | Background service stdout and stderr. |
| `towns/<key>/<role>/checkout` | A town's private git checkout for that house. |
| `towns/<key>/<role>/state` | That house's durable bot state. |
| `towns/<key>/history/` | Archived terminal tasks. |
| `mjolnir-options.json` | Cached Mjolnir launch catalog. |

`Store` is the only writer. Every change runs through `Store.Update`, which
clones the state, applies the change, validates it, and replaces `state.json`
atomically with fsync. Snapshots are immutable copies; clients never observe a
half-applied write. `Store.Open` also re-validates persisted state on startup and
repairs or reports the specific problems the model defines.

## Scheduling

The supervisor owns one worker per house per town and a shared capacity limit
(`service_config.max_workers`, default 4, maximum 64). Repo Bot observes without
holding an agent slot; it takes one only while repairing the branch.

Each house has a cadence: a town-wide `poll_seconds` for intake and repair work,
thirty minutes between Bug and Feature discovery scans, five minutes for Review
and Release. The scheduler wakes work when the queue changes, not only on a
timer. Release additionally follows its own cadence and burst rules; see
[workflow.md](workflow.md).

## Dispatch

When a house is due, the supervisor:

1. Re-reads the current state and resolves the house's agent profile and
   execution placement at dispatch time, not from an older snapshot.
2. Checks holds — quiet hours, budget, capacity, a selected remote runtime, a
   recovery hold — and stops before starting anything if one applies.
3. Calls the bot package in process through `BotWorkers`, streaming bounded
   progress and log lines into durable state.
4. Commits the result: task moves, ownership, review audits, branch health,
   outcome records and budget charge.

An interrupted dispatch is recorded as a recovery hold rather than adopted. A
restart never continues a process it did not start.

## Bots

Each bot is a Go package with a `Config`, a `Run` (or a focused duty) and its own
private state directory. Town adapts the town's configuration and work policy
into that package's config, runs it, and converts its typed observation back into
the town model. The repo bot's inventory is the pipeline's observation step;
Town itself additionally performs a few bounded, idempotent GitHub writes (the
merge, closing declined work, filing follow-ups, submitting a request), each
gated on a fresh read of exactly what it is about to change.

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

## Harnesses and Mjolnir

`internal/harness` resolves an ACP agent from the official registry, from the
bundled supplements (`anvil`, `muse-acp`, `draupnir`), or from a custom command.
It downloads and caches versioned archives atomically and launches without shell
interpolation. `internal/mjolnir` is the optional remote execution path: Town
discovers targets, profiles and bundles from a loopback Mjolnir API, pins an
exact runtime, and can place review and issue-repair duties there. Other duties
are held rather than silently run locally. See [harnesses.md](harnesses.md) and
[mjolnir.md](mjolnir.md).

## Town Guide

Town Guide is a separate bounded ACP conversation. It sees a context assembled
from public state — worker summaries, recent failures, task counts and recent
tasks — and has no workspace tools and no permission to write. It may propose a
pause; the browser applies it only after the exact proposal digest is
confirmed against current state. See [operations.md](operations.md).

## Demo and townsim

`bt --demo` runs a closed simulation in a separate state directory: it seeds two
towns and never receives a GitHub client, an Mjolnir connection or a worker
runner. It is safe to run anywhere.

`cmd/townsim` is a stochastic model of the scheduler. It reproduces the
supervisor's rules — one worker per house, shared capacity, poll cadence,
discovery gaps, Simplifier ahead of the Mayor, one repair round per pull request,
a closed pull request restarting its issue — and reports whether a set of
defaults can grow an unbounded backlog. It is a design tool, not part of the
service.
