# Bots

Each bot is a Go package of this module under `bots/` and runs as its own
binary, `cmd/<bot>` (for example `review-bot`), built from that package and
the shared server in `internal/botapi/botserver`. Town reaches a bot only over
the network through the bot contract in `internal/botapi`, wherever it runs:
started by `bt` on a loopback port, in its own container under docker compose,
or in its own pod on Kubernetes (see [operations.md](operations.md#bots)). Each
bot's prompt, configuration, GitHub access and durable state stay with its
package, and its private files live under its own data root at
`towns/<key>/<role>/{checkout,state}`, one directory per repository. A bot that
`bt` starts on the same machine uses Town's state directory as its data root,
so its workspaces sit where they always have; a bot in a container keeps them
on its own volume.

Town sends the repository's agent profile and work policy in a contract
request; the bot's `worker.go` adapts it into the bot's own config, resolves the
agent harness in its own environment, runs one duty, and returns a typed result
that Town converts into the `internal/town` model. Every bot drives an ACP agent through the shared
[acp-go](https://github.com/BrokkAi/acp-go) runner.

The browser shows each bot by name and stage; configuration, the API and state
use the role key.

| Bot | Stage | Package | Role key |
| --- | --- | --- | --- |
| Repo Bot | Repository | `bots/repo-bot` | `repo` |
| Bug Bot | Bug discovery | `bots/bug-bot` | `bug` |
| Feature Bot | Feature discovery | `bots/feature-bot` | `feature` |
| Simplifier Bot | Simplification | `bots/simplifier-bot` | `simplifier` |
| Mayor Bot | Triage | `bots/mayor-bot` | `hall` |
| Issue Bot | Implementation | `bots/issue-bot` | `issue` |
| Review Bot | Review | `bots/review-bot` | `review` |
| Release Bot | Release | `bots/release-bot` | `release` |

## bug-bot (`role: bug`)

Package `bots/bug-bot`, which runs the Bug discovery stage. Investigates the repository
and files useful GitHub bug issues. The agent decides whether a finding
duplicates an existing open or closed issue by comparing root cause, inputs,
behaviour and discussion — there is no title-similarity heuristic. Findings are
verified before they are filed, and each issue carries a `<!-- bug-bot:... -->`
marker so a rescan does not refile it.

- Policy: `labels`, `focus`, `limit` (issues filed per run), `attempts`, `verify`.
- Discovery scans are spaced thirty minutes apart by the supervisor.

## feature-bot (`role: feature`)

Package `bots/feature-bot`, which runs the Feature discovery stage. Researches
capabilities the project does not have yet, compares them against existing
requests, and files concrete proposals. It shares bug-bot's discovery shape with
a feature-specific prompt and proposal schema, and marks its issues
`<!-- feature-bot:... -->`.

- Policy: `labels`, `focus`, `limit` (proposals filed per run), `attempts`, `verify`.
- Discovery scans are spaced thirty minutes apart.

## simplifier-bot (`role: simplifier`)

Package `bots/simplifier-bot`, which runs the Simplification stage. It assesses every
arrival, weighing its complexity against its value. It has two modes, selected
per repository (`simplifier_mode`):

- **suggest** — attach a bounded recommendation, accept or decline, to a
  pending decision. Mayor Bot stays the decision maker.
- **auto** — let Simplifier Bot pass routine work through, decline low-value
  complex work, and close low-value complex issues without a separate
  decision.

It also reads whole-batch arrivals to propose simplification work. Its issues
carry a `<!-- simplifier-bot:... -->` marker.

- Policy: `labels`, `limit` (proposals per run), `verify`.

## mayor-bot (`role: hall`)

Package `bots/mayor-bot`, which runs the Triage stage.
It has two duties, both one-shot:

- **judge** — decide one arrival: an issue, a pull request, or a bot update.
  Mayor Bot reads Town's description of the arrival, any Simplifier Bot advice,
  the live GitHub discussion and the repository itself, and returns a decision
  with a reason.
- **bulletin** — write the repository's status report, the work-completed feed. A
  status report entry is only written when something merged since the last one, at
  most once per `bulletin_seconds`.

- Policy: `limit` (status report items), `verify`.

## issue-bot (`role: issue`)

Package `bots/issue-bot`, which runs the Implementation stage. Claims eligible issues, gives
a coding agent one focused attempt at each, and opens pull requests it owns. It
keeps durable per-issue job records — status, failure, claim state, retry
eligibility, URL and branch — that Town imports into the task's
`IssueJob`. A pull request Town closed after review is a *superseded* pull
request: the next run starts the issue over and never counts that pull request
again.

Town also dispatches issue-bot for **repairs**: fixing the branch of a pull
request Town owns from review feedback, after which the pull request is
reviewed again.

- Policy: `labels`, `exclude-labels`, `only` (one issue), `attempts`, `verify`.

## review-bot (`role: review`)

Package `bots/review-bot`, which runs the Review stage. Examines a pull request through
its exact base and head, independently verifies each candidate finding, and
returns a certified audit: a base, a head, a discussion digest, the description
digest, a verdict, a summary, the checks it ran, and findings with severities.
It posts a review; it never approves, requests changes, merges, or pushes to a
contributor's branch.

Town uses the audit to decide the next step. A review of any other revision
is stale and never attributed to the dispatched task. A dry-run result never
grants publication or merge authority.

- Policy: `labels`, `exclude-labels`, `only` (one pull request), `focus`, `limit`
  (findings), `attempts`, `verify`.
- Review attempts per revision are bounded; the second incomplete attempt retires
  the pull request for that revision.

## release-bot (`role: release`)

Package `bots/release-bot`, which runs the Release stage. Watches unreleased commits and
runs the release machine: prepare the change, validate publishability without
publishing, publish, and verify the result independently. It ships embedded
skills for triage, preflight, publication and GitHub access.

Its cadence and gates are repository settings; see
[workflow.md](workflow.md) and [configuration.md](configuration.md). A failed
attempt preserves local work and evidence for a retry, and a partially published
release is reconciled rather than republished blindly.

- Policy: `attempts`, `verify`, and the release fields (`daily_seconds`,
  `minimum_gap_seconds`, `quiet_seconds`, `burst`, `burst_window_seconds`,
  `triage`, `preflight`, `verification_timeout_seconds`, `workflows`, `assets`).

## repo-bot (`role: repo`)

Package `bots/repo-bot`, which runs the Repository stage. The repository observer, and the bot
that keeps the branch it covers healthy. One run has two duties:

- **inventory** — one complete observation: the covered branch, its exact head,
  every issue, pull request and release, the commits gained since the last look,
  and proof of which commits a published release already contains.
- **branch repair** — when the covered branch's checks fail, attempt a bounded
  fix and report what it did. `state` is `green`, `pending`, `unreported`, `red`,
  `repaired` or `unrepairable`.

Repo Bot holds an agent slot only while repairing; inventory runs without one.

- Policy: `limit` (repair attempts per revision), `verify`.

## Common behaviour

- Every bot validates its configuration before running and returns a typed error
  rather than a partial result.
- Progress is streamed to Town and coalesced before it reaches durable
  state, so a chatty agent cannot make the service I/O-bound.
- A bot's GitHub credential is resolved at request time and never written into
  Town's state or a snapshot.
- Bot-side `poll` and `timeout` defaults are preserved, but Town schedules
  the runs; the supervisor owns cadence, capacity and retries.
- A bot binary serves one bot. It refuses to start without a bearer token and a
  data root, accepts only its own role, and keeps serving until it is stopped;
  a stopped bot cancels its runs and reports what each one completed first.

## Bot binaries

Every bot binary takes the same flags. A flag outranks its environment
variable.

| Flag | Environment | Meaning |
| --- | --- | --- |
| `--listen ADDR` | `BROKK_BOT_LISTEN` | Address to serve on. Default `:8080`. |
| `--root DIR` | `BROKK_BOT_ROOT` | Data root for workspaces and installed harnesses. Required. |
| `--token-file FILE` | `BROKK_BOT_TOKEN_FILE` | File holding the bearer token Town must present. `BROKK_BOT_TOKEN` holds the token itself instead. Required, at least 16 characters. |
| `--tls-cert FILE`, `--tls-key FILE` | `BROKK_BOT_TLS_CERT`, `BROKK_BOT_TLS_KEY` | Serve HTTPS with this certificate. |
| `--demo` | `BROKK_BOT_DEMO=1` | Answer every run with a canned result for its role; never run git, gh or an agent. Only a demo Town dispatches to a demo bot, and a demo Town dispatches to nothing else. |
| `--log-level` | `BROKK_BOT_LOG_LEVEL` | `debug`, `info`, `warn` or `error`. |
| `--exit-with-parent` | | Exit when standard input closes; `bt` passes it to the bots it starts. |
| `--version` | | Print the version. |

Once listening, a bot prints one JSON line on standard output naming its
address, name, role and version; its own log goes to standard error. Its
`git` and `gh` calls read `GH_TOKEN` from its environment, and the harness it
runs reads its API keys the same way. The container images carry `git`, `gh`,
`node`/`npx` and `uv`/`uvx`, and configure git to take GitHub credentials from
`gh`, so nothing depends on a `gh` login on the host.

### The network contract

| Endpoint | Purpose |
| --- | --- |
| `GET /v1/healthz` | Liveness. The only endpoint that needs no token. |
| `GET /v1/identity` | The bot's name, role, version, contract version and demo flag. |
| `POST /v1/run` | One `botapi.Request` in; an NDJSON stream of frames out. |
| `POST /v1/cancel` | Stops the run named by the `Brokk-Run-Id` header. |
| `POST /v1/storage` | One `botapi.StorageRequest` in; its `StorageResult` out. |

Every other request carries `Authorization: Bearer <token>`, compared in
constant time. Request bodies are bounded at 8 MiB and unknown fields are
refused. A run's stream numbers every frame from zero: a `hello` frame with the
bot's identity, then `event` frames (progress or log) and a `heartbeat` every
ten seconds, then exactly one `done` frame with the result, the bot's error, if
any, and `uncertain` when something other than Town cut the run short (the
bot shutting down) or its result could not be delivered. Town checks the order, the bounds (1 MiB per event, 128 MiB for the
result) and that nothing follows `done`. When Town stops a run it calls
`/v1/cancel` and waits up to 30 seconds for `done`; a closed connection also
cancels the run.
