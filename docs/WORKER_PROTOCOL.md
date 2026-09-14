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
invoke a shell. The worker exits after a Town shutdown request or canceled
service context.

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
in `result.review`. Other workers return no typed result. In every role, GitHub
receipts and Town's inventory remain the durable source of truth; process exit
alone is never interpreted as a successful write.

## Shutdown

After Town consumes the terminal event, it requests graceful shutdown:

```text
POST /v1/shutdown
```

The worker returns HTTP 202, stops accepting runs, finishes response streaming,
removes its socket, and exits. Town cancels the process group only if graceful
shutdown exceeds its bounded deadline or the dispatch context is canceled.
