# Bots

Each bot is a Go package of this module under `bots/`. Town calls the package in
process; the bot has no server, no socket and no separate version. Its prompt,
configuration, GitHub access and durable state stay with its package, and its
private files live in the town's state directory under
`towns/<key>/<role>/{checkout,state}`.

Town adapts the town's agent profile and work policy into the bot's own config,
runs one duty, and converts the typed result back into the town model. Every bot
drives an ACP agent through the shared
[acp-go](https://github.com/BrokkAi/acp-go) runner.

## bug-bot (`role: bug`)

Package `bots/bug-bot`. Investigates the repository and files useful GitHub bug
issues. The agent decides whether a finding duplicates an existing open or closed
issue by comparing root cause, inputs, behaviour and discussion — there is no
title-similarity heuristic. Findings are verified before they are filed, and each
issue carries a `<!-- bug-bot:... -->` marker so a rescan does not refile it.

- Policy: `labels`, `focus`, `limit` (issues filed per run), `attempts`, `verify`.
- Discovery scans are spaced thirty minutes apart by the supervisor.

## feature-bot (`role: feature`)

Package `bots/feature-bot`. Researches capabilities the project does not have yet,
compares them against existing requests, and files concrete proposals. It shares
bug-bot's discovery shape with a feature-specific prompt and proposal schema, and
marks its issues `<!-- feature-bot:... -->`.

- Policy: `labels`, `focus`, `limit` (proposals filed per run), `attempts`, `verify`.
- Discovery scans are spaced thirty minutes apart.

## simplifier-bot (`role: simplifier`)

Package `bots/simplifier-bot`. The complexity and value reviewer. It has two Town
selected modes:

- **suggest** — attach a bounded admission/decline recommendation to a Mayoral
  decision. The Mayor stays the decision maker.
- **auto** — let Town admit routine work, decline low-value complex work, and close
  low-value complex issues without a separate Mayoral decision.

It also reads whole-batch arrivals to propose simplification work. Its issues
carry a `<!-- simplifier-bot:... -->` marker.

- Policy: `labels`, `limit` (proposals per run), `verify`.

## mayor-bot (`role: hall`)

Package `bots/mayor-bot`. Town Hall has two duties, both one-shot:

- **judge** — decide one arrival: an issue, a pull request, or a bot update. The
  Mayor reads Town's description of the arrival, any Simplifier advice, the live
  GitHub discussion and the repository itself, and returns a decision with a
  reason.
- **bulletin** — write the town's work-completed feed. A bulletin is only written
  when something merged since the last one, at most once per `bulletin_seconds`.

- Policy: `limit` (bulletin items), `verify`.

## issue-bot (`role: issue`)

Package `bots/issue-bot`. Claims eligible issues, gives a coding agent one focused
attempt at each, and opens pull requests it owns. It keeps durable per-issue job
records — status, failure, claim state, retry eligibility, URL and branch — that
Town imports into the task's `IssueJob`. A pull request Town closed after review
is a *superseded* pull request: the next run starts the issue over and never counts
that pull request again.

Town also dispatches issue-bot for **repairs**: fixing a Town-owned pull request's
branch from review feedback, after which the pull request is reviewed again.

- Policy: `labels`, `exclude-labels`, `only` (one issue), `attempts`, `verify`.

## review-bot (`role: review`)

Package `bots/review-bot`. Investigates a pull request through its exact base and
head, independently verifies each candidate finding, and returns a certified audit:
a base, a head, a discussion digest, the description digest, a verdict, a summary,
the checks it ran, and findings with severities. It posts a review; it never
approves, requests changes, merges, or pushes to a contributor's branch.

Town uses the audit to decide the next step. A review of any other revision is
stale and never attributed to the dispatched task. A dry-run result never grants
publication or merge authority.

- Policy: `labels`, `exclude-labels`, `only` (one pull request), `focus`, `limit`
  (findings), `attempts`, `verify`.
- Review attempts per revision are bounded; the second incomplete attempt retires
  the pull request for that revision.

## release-bot (`role: release`)

Package `bots/release-bot`. Watches unreleased commits and runs the release
machine: prepare the change, validate publishability without publishing, publish,
and verify the result independently. It ships embedded skills for triage,
preflight, publication and GitHub access.

Its cadence and gates are town settings; see
[workflow.md](workflow.md) and [configuration.md](configuration.md). A failed
attempt preserves local work and evidence for a retry, and a partially published
release is reconciled rather than republished blindly.

- Policy: `attempts`, `verify`, and the release fields (`daily_seconds`,
  `minimum_gap_seconds`, `quiet_seconds`, `burst`, `burst_window_seconds`,
  `triage`, `preflight`, `verification_timeout_seconds`, `workflows`, `assets`).

## repo-bot (`role: repo`)

Package `bots/repo-bot`. The repository observer, and the house that keeps the
branch it covers healthy. One run has two duties:

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
- Progress is streamed to Town and coalesced before it reaches durable state, so a
  chatty agent cannot make the service I/O-bound.
- A bot's GitHub credential is resolved at request time and never written into
  town state or a snapshot.
- Bot-side `poll` and `timeout` defaults are preserved, but Town schedules the
  runs; the supervisor owns cadence, capacity and retries.
