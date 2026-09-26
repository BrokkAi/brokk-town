# Brokk Town worker protocol v1

Brokk Town supervises the released Brokk bots as independent executables. They
communicate through a local, versioned HTTP/JSON protocol rather than Go library
calls or CLI log parsing. Issue-job summaries and retries also cross the worker
protocol; Town never reads or edits a bot's private state directly.

## Transport and security

The transport is a private Unix-domain socket for one persistent worker per
house and town. Town starts all eight workers, including paused houses:

```text
npx --yes -- @brokkai/BOT@RESOLVED_VERSION worker --socket PATH
```

The socket's parent directory is private to the Town service and the socket is
mode `0600`. Town connects with the Go standard library HTTP client and does not
invoke a shell. Town starts each worker in its own process group (`Setpgid`),
captures bounded stdout/stderr tails, and owns the worker for the service's
lifetime. Town resolves `@latest` once before startup, and records the exact
version it launches. Each worker connects to the private socket named by
`BROKK_TOWN_PARENT_SOCKET`. EOF cancels the worker and its active work, including
when Town exits unexpectedly. This works through npm launchers, which do not
forward arbitrary inherited file descriptors. Workers do not detach or survive
a Town restart.

The message schemas and methods are transport-independent. A self-signed
TLS transport with mutual certificate authentication can therefore be added later
without changing task semantics. Protocol version and capability checks will
remain mandatory during TLS handshake and initialization.

## Initialization and versioning

Before submitting work, Town calls:

```text
GET /v1/initialize
```

The worker returns:

```json
{
  "protocol": 1,
  "minimum_protocol": 1,
  "bot": "review-bot",
  "version": "0.2.0",
  "capabilities": ["policy", "run", "progress", "exact-revision-review", "finding-severity", "parent-socket"]
}
```

A connection is compatible only when:

- `minimum_protocol <= 1 <= protocol`;
- the bot identity matches the role being dispatched;
- the reported service version exactly matches the CLI's `version` output;
- all capabilities required for that role are advertised;
- required result schemas are implied by the role capability.

Town records the package runner path/hash and resolved bot release version. The
runner hash is not a hash of the npm payload; npm verifies package integrity.
Initialization must report the exact resolved bot version. Town rechecks its
runner before and after dispatch and preserves uncertain outcomes on changes.

Bot releases preserve every previously supported API. A future `/v2` is added
alongside `/v1`; existing clients continue using `/v1` without changing Town or
pinning a bot. New capabilities are additive. CI retains v1 contract tests and
runs them for every independent bot release. The protocol range/capability check
catches release regressions before a job starts; it is not an upgrade policy.

## Starting one run

Town submits one strict JSON object:

```text
POST /v1/runs
Content-Type: application/json
Accept: application/x-ndjson
```

The v1 request contains:

- protocol version;
- Git remote and branch;
- isolated checkout and state directories;
- GitHub host and repository;
- the resolved ACP agent launch profile;
- operator verify command;
- PR number for review work;
- expected base and head SHAs for review work.
- issue or PR number and `mode` for Simplifier Bot intake work;
- `mode`, `since_head` and `commits` for Repo Bot inventory work.
- With the optional `incremental-inventory` capability, `inventory_since` and
  `inventory_branch` request a completed delta after a previous full baseline.

Protocol v1 has no field for limiting Release Bot's release-preparation merge
authority. Town therefore does not dispatch Release Bot while the town merge
policy is `manual`.

Unknown fields and trailing JSON are rejected. Request bodies are bounded. The
worker applies its own release defaults and validation before doing work.

## Event stream

A successful run response is newline-delimited JSON with contiguous sequence
numbers beginning at one:

```json
{"type":"progress","seq":1,"progress":{"phase":"investigating","task":"..."}}
{"type":"result","seq":2,"result":{"review":{"complete":true}}}
{"type":"complete","seq":3}
```

Event types are:

- `progress`: owned phase/task snapshot;
- `result`: optional role-owned public result;
- `error`: terminal semantic failure;
- `canceled`: terminal cancellation;
- `complete`: terminal success after all required events.

Town rejects gaps, unknown types, events after a terminal event, missing result
payloads, and streams that end without a terminal event. Worker diagnostics are
kept as bounded tails.

Repo workers return one completed repository observation in `result.inventory`
and their report on the branch it covers in `result.health`. Their `mode` names
the duty: `full` observes and repairs, `inventory` observes only, which is what
Town asks for when it is confirming a merge, so a repair agent never starts
inside another house's work. `since_head` is the branch head Town last observed
and `commits` are the revisions it still needs release ancestry for; Town's task
graph never crosses the protocol. A failed run still carries whatever inventory
the worker completed, because Town's view of the repository must not depend on
the health duty that follows it.

Mayor workers take `mode: "judge"` with the arrival's `issue` or `pr` (neither
for a bot update), `head_sha` for a pull request, and `arrival`, Town's JSON
description of the item; they return `result.judgment` with `decision`
(`admit`, `decline`, or `delay` for a bot update) and `reason`. With
`mode: "bulletin"` they take `since` and `until` and return `result.bulletin`:
`title`, `summary`, classified `items` (`feature`, `fix`, `improvement`,
`other`, each citing `pulls`), and the `pulls` covered. The window echoed back
must match the request. Issue workers return submitted PR ownership in `result.issue`. Simplifier item
workers return the bounded admission/decline advice in
`result.simplification`; repository simplification scans create marked GitHub
issues and return no typed result. Review workers
return exact base/head binding, completion evidence, and public finding details
in `result.review`. Any worker may also return nonnegative `usage.input_tokens`,
`usage.output_tokens`, and `cost_usd` fields when its provider makes those
measurements available. Town preserves absent measurements as unknown rather
than zero. Other workers return no role-specific typed result. In every role,
GitHub receipts and Town's inventory remain the durable source of truth; process
exit alone is never interpreted as a successful write.

Issue Bot run requests include an exact positive `issue` number selected from a
durably admitted Town task. Town requires the worker's `exact-issue` capability;
repository-wide issue scans are not used because they could bypass pending or
declined Mayoral decisions.

## Resetting a release attempt budget

Release Bot stops after three failed attempts at one release and leaves the job
pending. Town lifts that budget through the worker rather than by editing the
bot's private state:

```text
POST /v1/retry
Content-Type: application/json
```

The body is the same strict run request. The worker resets the pending job's
attempt budget in that workspace without starting an agent and answers
`200 {"retry":"scheduled"}`. A workspace with no pending release answers `409`
with the reason; Town treats both as consuming the operator's request and then
submits the ordinary run, which resumes the job. Town sends this only after an
operator asks (`bt retry --role release` or the control API with an empty task)
and only to a worker advertising the `retry` capability. Unknown fields and
protocol mismatches are rejected exactly as for `/v1/runs`.

## Process ownership and job summaries

Town starts every worker at service startup, even for paused houses. A worker
handles sequential runs until Town stops. Concurrent jobs receive HTTP 409.
Each process receives `BROKK_TOWN_PARENT_SOCKET` and connects to that Unix socket
before serving. It advertises `parent-socket` support. EOF means Town died; cancel
the server and all active work. Existing clients may instead supply
`BROKK_TOWN_PARENT_PIPE=1` and a read-only pipe at descriptor 3; that lifecycle
contract remains supported. Request cancellation also cancels work. There is no
detach/attach or adoption protocol.

Before dispatch, Town records the exact target and revision. An interrupted stream
preserves that provenance as an uncertain outcome for reconciliation.

Issue Bot accepts `mode: "jobs"` on `POST /v1/runs` without taking the job slot.
It returns `result.jobs`, keyed by issue number, with status, failure, claim_pending,
tries, retry_at, URL, branch and optional result status/detail. This is a public
summary, not access to private state. `mode: "retry-issue"` requires a positive
issue number, takes the job slot, applies Issue Bot's own retry validation and
returns the updated summaries. Neither mode invokes an agent or writes to GitHub.

## Shutdown

A terminal event ends one run, not the worker process. The same worker accepts
subsequent runs. Pausing a house lets its current job finish and keeps the worker
alive; stopping a running job cancels it and closes that worker. The supervisor
replaces a stopped or failed process independently of whether its house is enabled.

On service shutdown, Town cancels running work and closes every worker's parent
connection, lets the worker cancel and drain for up to seven seconds, then kills
the process group if needed. Failed startup without a parent connection first
sends SIGTERM to the group. It reaps children and removes their socket directories.
Deleting a town also stops its workers. Dispatch cancellation or its deadline
closes the affected worker. An interrupted dispatch's possible writes remain
uncertain in saved state.

The standalone worker servers also support this explicit lifecycle endpoint for
other local protocol clients and their tests; Town does not call it:

```text
POST /v1/shutdown
```

The body must be empty. The worker returns HTTP 202, signals server shutdown,
cancels active work, drains responses within its shutdown bound, removes its
socket, and exits. This endpoint remains part of protocol v1; it is not sent after
each run and does not replace the parent-liveness contract.

### Incremental repository observations

`incremental-inventory` is an additive v1 capability. Town omits both new request
fields for older worker releases, which continue full inventory reads. Updated
workers return `started_at` on every successful scan and mark a delta explicitly
with `incremental: true`; missing/false means a full scan. An interrupted or
incomplete page is an error, never a successful partial scan.

Town requests a full baseline on first sync, branch changes and at least once
per 24 hours. Only a successfully reconciled scan advances `last_sync` to its
start time; `last_full_sync` and `sync_branch` retain its baseline. Delta issue
reads use GitHub's [`since` parameter](https://docs.github.com/en/rest/issues/issues#list-repository-issues)
with five minutes of overlap, while open issues and PRs are refreshed. Changed
closed PRs are fetched individually because the
[PR list API](https://docs.github.com/en/rest/pulls/pulls#list-pull-requests) has no
`since` parameter. Branch identity or cursor uncertainty falls back to a full
scan. Releases and requested exact commit ancestry are still read each time.

Absence in a delta never proves deletion or authorizes a close. Town preserves
closing claims and re-reads an omitted PR before continuing its durable closure
workflow. No task graph, credentials or bot Go packages cross this boundary.
