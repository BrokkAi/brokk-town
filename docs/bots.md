# Bots

Each bot is a Go package of this module under `bots/`. The Squad calls the
package in process; the bot has no server, no socket and no separate version.
Its prompt, configuration, GitHub access and durable state stay with its
package, and its private files live in the Squad's state directory under
`towns/<key>/<role>/{checkout,state}`, one directory per precinct.

The Squad adapts the precinct's agent profile and work policy into the bot's own
config, runs one duty, and converts the typed result back into the
`internal/town` model. Every bot drives an ACP agent through the shared
[acp-go](https://github.com/BrokkAi/acp-go) runner.

Each bot staffs one unit. The browser shows the unit; configuration, the API and
state use the role key.

| Bot | Package | Role key | Unit |
| --- | --- | --- | --- |
| Repo Bot | `bots/repo-bot` | `repo` | Patrol |
| Bug Bot | `bots/bug-bot` | `bug` | Detectives |
| Feature Bot | `bots/feature-bot` | `feature` | Intel |
| Simplifier Bot | `bots/simplifier-bot` | `simplifier` | Slop Squad |
| Judge Bot | `bots/mayor-bot` | `hall` | Courthouse |
| Issue Bot | `bots/issue-bot` | `issue` | Task Force |
| Review Bot | `bots/review-bot` | `review` | Forensics |
| Release Bot | `bots/release-bot` | `release` | Release |

## bug-bot (`role: bug`)

Package `bots/bug-bot`, staffing the Detectives. Investigates the repository and
files useful GitHub bug issues. The agent decides whether a finding duplicates
an existing open or closed issue by comparing root cause, inputs, behaviour and
discussion — there is no title-similarity heuristic. Findings are verified
before they are filed, and each issue carries a `<!-- bug-bot:... -->` marker so
a rescan does not refile it.

- Policy: `labels`, `focus`, `limit` (issues filed per run), `attempts`, `verify`.
- Discovery scans are spaced thirty minutes apart by the supervisor.

## feature-bot (`role: feature`)

Package `bots/feature-bot`, staffing Intel. Researches capabilities the project
does not have yet, compares them against existing requests, and files concrete
proposals. It shares bug-bot's discovery shape with a feature-specific prompt
and proposal schema, and marks its issues `<!-- feature-bot:... -->`.

- Policy: `labels`, `focus`, `limit` (proposals filed per run), `attempts`, `verify`.
- Discovery scans are spaced thirty minutes apart.

## simplifier-bot (`role: simplifier`)

Package `bots/simplifier-bot`, staffing the Slop Squad. The complexity and value
screener. It has two modes, selected per precinct (`simplifier_mode`):

- **suggest** — attach a bounded admission/dismissal recommendation to a ruling.
  The judge stays the decision maker.
- **auto** — let the Squad admit routine work, dismiss low-value complex work,
  and close low-value complex issues without a separate ruling.

It also reads whole-batch arrivals to propose simplification work. Its issues
carry a `<!-- simplifier-bot:... -->` marker.

- Policy: `labels`, `limit` (proposals per run), `verify`.

## mayor-bot (`role: hall`)

Package `bots/mayor-bot`, shown as **Judge Bot**, staffing the Courthouse. It
has two duties, both one-shot:

- **judge** — rule on one arrival: an issue, a pull request, or a bot update.
  Judge Bot reads the Squad's description of the arrival, any Slop Squad advice,
  the live GitHub discussion and the repository itself, and returns a ruling
  with a reason.
- **bulletin** — write the precinct's blotter, the work-completed feed. A
  blotter entry is only written when something merged since the last one, at
  most once per `bulletin_seconds`.

- Policy: `limit` (blotter items), `verify`.

## issue-bot (`role: issue`)

Package `bots/issue-bot`, staffing the Task Force. Claims eligible issues, gives
a coding agent one focused attempt at each, and opens pull requests it owns. It
keeps durable per-issue job records — status, failure, claim state, retry
eligibility, URL and branch — that the Squad imports into the case's
`IssueJob`. A pull request the Squad closed after review is a *superseded* pull
request: the next run starts the issue over and never counts that pull request
again.

The Squad also dispatches issue-bot for **repairs**: fixing the branch of a pull
request the Squad owns from review feedback, after which the pull request is
reviewed again.

- Policy: `labels`, `exclude-labels`, `only` (one issue), `attempts`, `verify`.

## review-bot (`role: review`)

Package `bots/review-bot`, staffing Forensics. Examines a pull request through
its exact base and head, independently verifies each candidate finding, and
returns a certified audit: a base, a head, a discussion digest, the description
digest, a verdict, a summary, the checks it ran, and findings with severities.
It posts a review; it never approves, requests changes, merges, or pushes to a
contributor's branch.

The Squad uses the audit to decide the next step. A review of any other revision
is stale and never attributed to the dispatched case. A dry-run result never
grants publication or merge authority.

- Policy: `labels`, `exclude-labels`, `only` (one pull request), `focus`, `limit`
  (findings), `attempts`, `verify`.
- Review attempts per revision are bounded; the second incomplete attempt retires
  the pull request for that revision.

## release-bot (`role: release`)

Package `bots/release-bot`, staffing Release. Watches unreleased commits and
runs the release machine: prepare the change, validate publishability without
publishing, publish, and verify the result independently. It ships embedded
skills for triage, preflight, publication and GitHub access.

Its cadence and gates are precinct settings; see
[workflow.md](workflow.md) and [configuration.md](configuration.md). A failed
attempt preserves local work and evidence for a retry, and a partially published
release is reconciled rather than republished blindly.

- Policy: `attempts`, `verify`, and the release fields (`daily_seconds`,
  `minimum_gap_seconds`, `quiet_seconds`, `burst`, `burst_window_seconds`,
  `triage`, `preflight`, `verification_timeout_seconds`, `workflows`, `assets`).

## repo-bot (`role: repo`)

Package `bots/repo-bot`, staffing Patrol. The repository observer, and the unit
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
- Progress is streamed to the Squad and coalesced before it reaches durable
  state, so a chatty agent cannot make the service I/O-bound.
- A bot's GitHub credential is resolved at request time and never written into
  the Squad's state or a snapshot.
- Bot-side `poll` and `timeout` defaults are preserved, but the Squad schedules
  the runs; the supervisor owns cadence, capacity and retries.
