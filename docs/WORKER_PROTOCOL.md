# Brokk Town worker protocol v1

Brokk Town supervises the released Brokk bots as independent executables. They
communicate through a local, versioned HTTP/JSON protocol rather than Go library
calls or CLI log parsing for primary work. Protocol v1 does not yet carry all
issue-job outcomes, so Town uses the pinned issue-bot release's validated public
state and retry APIs for that scheduling metadata only.

## Transport and security

The initial transport is a Unix-domain socket created for one bot dispatch:

```text
bbb|bfb|bib|brv|brb worker --socket PATH
```

The socket's parent directory is private to the Town service and the socket is
mode `0600`. Town connects with the Go standard library HTTP client and does not
invoke a shell. The worker exits after a Town shutdown request. Town starts the
worker in its own session; the worker must not depend on its parent process, a
controlling terminal, or an open stdout/stderr pipe, and it must keep running
when the Town service exits.

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
  "capabilities": ["run", "progress", "exact-revision-review"]
}
```

A connection is compatible only when:

- `minimum_protocol <= 1 <= protocol`;
- the bot identity matches the role being dispatched;
- the reported service version exactly matches the CLI's `version` output;
- all capabilities required for that role are advertised;
- required result schemas are implied by the role capability.

Town records the executable path, executable hash, and release version before
starting the service and rechecks them after the run. A mid-dispatch replacement
fails the dispatch as uncertain; the next dispatch starts the newly installed
executable and negotiates again.

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

Issue workers return submitted PR ownership in `result.issue`. Review workers
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

## Surviving a Town restart

Town commits the worker's handle (PID, socket path, output file, version, and the
exact task) before `POST /v1/runs`. A Town service that stops for an upgrade
leaves the worker running; the next service reconnects.

Workers that advertise the optional `detach` capability must:

- continue the run when the `/v1/runs` client disconnects, treating the
  disconnect as neither cancellation nor failure;
- buffer every event of the run, including the terminal event, until shutdown;
- serve `GET /v1/attach?after=N` with `Accept: application/x-ndjson`, replaying
  every buffered event whose `seq` is greater than `N` and then streaming live
  events until the terminal event, using the same event schema and contiguous
  sequence numbers;
- answer `GET /v1/attach` with HTTP 404 when no run has been submitted, so Town
  can end an idle worker and reschedule the house without recording a failure.

Town resumes from the last progress sequence it durably observed; a replayed
`result` event is required before a replayed terminal event. Workers without
`detach` receive a shutdown request when a new service adopts them and may finish
their current run first; Town records that attempt as uncertain because it could
not observe the outcome. In both cases GitHub receipts remain the source of truth.

## Shutdown

After Town consumes the terminal event, it requests graceful shutdown:

```text
POST /v1/shutdown
```

The worker returns HTTP 202, stops accepting runs, finishes response streaming,
removes its socket, and exits. Town kills the process group only when graceful
shutdown exceeds its bounded deadline, when an operator stops the house or
deletes the town, or when the dispatch deadline passes. Service shutdown never
kills a worker.
