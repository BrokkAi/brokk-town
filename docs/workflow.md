# How work moves

Brokk Town is a pipeline. For each repository, Repo Bot observes the repository;
every other bot does work Town decides it should do. A task always has a
`house` (which bot owns it) and a `stage` (where it is in that bot's work). Town
moves a task only when the repository changed, a bot returned a result, or the
operator acted. The browser speaks of repositories, bots, tasks and decisions;
configuration, the API and state keep their original identifiers. The
[glossary](#glossary) maps one to the other.

## The loop

1. **Observe.** Repo Bot reads the branch, its head, every issue, pull request and
   release, and the commits gained since the last look. Town applies that
   scan to its own task state; it also makes a few bounded GitHub writes of its
   own — a merge, closing declined work, filing follow-ups, submitting a
   request — each gated on a fresh read of the exact object.
2. **Reconcile.** Town turns the scan into tasks, marks closed and locked
   items, records new arrivals as events in the Activity feed, and resets the
   audit when a revision changes. Absence in an incremental scan never means
   deletion.
3. **Route.** Town applies each bot's work policy and hands the task to the bot
   that should act next.
4. **Dispatch.** One bot runs one focused session. Progress and log lines are
   streamed into durable state.
5. **Commit.** Town records the result, ownership, audit, branch health,
   outcome and budget charge, and moves the task.

Nothing is dispatched twice. A revision gets at most two attempts at any step,
and an interrupted dispatch becomes a recovery hold rather than a silent retry.
See [recovery.md](recovery.md).

## Bots

Bots are listed in pipeline order, which follows a task through the repository
and is also the order of keys 1–8.

| Bot | Stage | Role key | Owns |
| --- | --- | --- | --- |
| Repo Bot | Repository | `repo` | Scanning the repository and repairing the covered branch. |
| Bug Bot | Bug discovery | `bug` | Filing new bug issues from repository investigation. |
| Feature Bot | Feature discovery | `feature` | Filing new feature proposals. |
| Simplifier Bot | Simplification | `simplifier` | Judging whether an arrival is worth doing. |
| Mayor Bot | Triage | `hall` | Decisions and the repository's status report. Never edits the repository. |
| Issue Bot | Implementation | `issue` | Implementing accepted issues and repairing the branches of pull requests Town owns. |
| Review Bot | Review | `review` | Reviewing and certifying exact revisions, and merging when policy allows. |
| Release Bot | Release | `release` | Batching unreleased commits and publishing a verified release. |

Repo Bot records what comes in, Bug Bot and Feature Bot file new work, and
external submissions arrive from outside. Every arrival first goes through
simplification, then triage decides it: an accepted issue goes to
implementation and an accepted pull request goes to review; declined work is
closed. Issue Bot implements accepted issues, Review Bot certifies the result
or sends it back, and Release Bot ships what merged. The browser's Board view
shows this flow as columns.

Repo Bot starts enabled. Every other bot starts paused and does nothing until
you start it.

## Issue lifecycle

```
arrival → simplifying → awaiting_mayor → queued → (issue works) → fixes → ready → merged
```

- A new issue from outside is `simplifying`: Simplifier Bot reads it. In
  `suggest` mode it attaches advice and the issue goes to `awaiting_mayor`
  (awaiting a triage decision); in `auto` mode Town can decline low-value work
  outright.
- A decision (`mayoral_decision`) is `pending` until you accept (`admit`) or
  decline (`decline`) it, or Mayor Bot decides. A decline is final; Town closes
  the issue.
- `queued` means Issue Bot can take it. The bot claims the issue, works from the
  repository's base branch, and opens a pull request it owns.
- Review feedback on a pull request Town owns becomes `fixes`: issue-bot gets
  one repair round on the same branch. After the repair is confirmed on GitHub
  the pull request returns to `queued` for a second review.
- If the second review still finds blocking work, Town closes its own pull
  request and starts the issue over from the current base branch. A
  contributor's pull request is never closed by Town; it goes to triage for a
  decision instead.

## Pull request lifecycle

```
arrival → simplifying → awaiting_mayor → queued → ready → merged
```

- The first review sends every certified finding back to Issue Bot for one fix
  round.
- The second review either finds only work below the close threshold — which
  becomes follow-up issues and the pull request merges — or still finds blocking
  work.
- The close threshold is `review_close_severity` (`P1`, `P2` or `P3`, default
  `P2`). An unrated finding counts as blocking.
- Retargeting a pull request off the repository's covered branch retires it
  until it targets the branch again, when a fresh review decides it.
- A closed pull request's issue starts over: the new attempt never counts the old
  pull request again.

### Merge gates

Town merges only when all of these hold at the moment of the merge:

- the pull request targets the branch Town covers;
- the certified audit matches the exact base, head, description and discussion;
- GitHub reports the required checks green, with no pending, failed, cancelled or
  skipped run;
- required approvals are satisfied;
- the pull request is open, unlocked, not a draft, and its head has not moved;
- the operator has not snoozed it or set `merge_policy` to `manual`;
- it is not inside quiet hours.

Before merging, Town submits an expected-head merge and records the intent
first, so an uncertain outcome is reconciled by the next scan instead of
being repeated blindly.

### Merge policy

| Policy | Behaviour |
| --- | --- |
| `bot` (default) | Town merges pull requests it created. |
| `all` | Town also merges external pull requests it certified. |
| `manual` | Town never merges, and Release Bot stays paused. |

## Simplification

Every arrival goes through simplification (`simplifier`), run by Simplifier
Bot. Two modes (`simplifier_mode`), set in repository settings in the browser:

- **suggest** — Simplifier Bot attaches a bounded recommendation to a pending
  decision; triage decides.
- **auto** — Town lets routine work through and declines low-value complex
  work without a separate decision. You can still accept it anyway before the
  closer claims it.

## Triage

Triage (`hall`) is run by Mayor Bot (`bots/mayor-bot`), which has two one-shot
duties:

- **judge** — decide an arrival: an issue, a pull request, or a bot proposal.
  Town gives Mayor Bot the arrival, any advice from Simplifier Bot, the GitHub
  discussion and the repository, and records a decision with a reason.
- **bulletin** — write the status report, the work-completed feed. Report
  entries (`bulletins`) are written at most once per `bulletin_seconds`, and
  only when something merged since the last one.

In the browser a pending decision offers **Accept** (`admit`) and **Decline**
(`decline`); after a review that asked for changes, accepting reads **Review
again**. An accepted issue goes to implementation and an accepted pull request
goes to review. A task declined automatically in `auto` mode offers **Accept
anyway**. Decisions waiting on you also appear under **Awaiting your decision**
in Needs attention.

## Release cadence

Release Bot watches unreleased commits on the covered branch and publishes when
the cadence says so:

- a **daily deadline** (`release_daily_seconds`, default 24h) after which
  unreleased commits are released regardless of the other rules;
- a **burst** (`release_burst` commits inside `release_burst_window_seconds`,
  default 5 in 2h) that releases early, provided the **minimum gap**
  (`release_minimum_gap_seconds`, default 2h) since the last release has passed
  and the branch has been **quiet** (`release_quiet_seconds`, default 15m).

Between those rules the agent decides: Town runs a short read-only
**triage** session asking whether the unreleased range contains anything users
need promptly, such as a security, crash, data-loss or regression fix. A failure
of triage never blocks the daily deadline.

A release goes through preparation, publishability preflight, publication and
independent verification. The prepared commit and tag are binding; the agent may
not weaken checks or rewrite a published tag. If publication is partial, the next
attempt reconciles it rather than republishing from scratch. See
[README.md](../README.md#releasing) for this repository's own release.

## Deferral

The browser's snooze action holds one task until a chosen time. Before then
Town starts no agent work and makes no merge for it; the rest of the bot's
workload keeps moving. At the resume time the task rejoins the workload on its
own, and Resume now clears the snooze. A snooze is operator state: nothing
observed on GitHub changes it.

## Archival

Closed, merged and shipped tasks are archived after thirty days, at most 500 per
scan. Active, blocked, pending-decision, recovery, follow-up and source-cursor
tasks stay hot. Archived tasks keep their identities, decisions and evidence,
are readable in History, and are restored automatically if they reopen. See
[storage.md](storage.md).

## Events

Every handoff (event kind `delivery`) and decision is appended to the
repository's event log (`events`) and streamed to connected clients. Events are
bounded (the most recent 512) and carry a sequence, time, `town` (the
repository id), kind, from/to bots, cargo and title. The browser's Activity
feed reads them from the same committed state: a handoff names both bots, with
"External" for a submission from outside (`outside`) and "You" for your own
action (`operator`).

## Glossary

Configuration, the local API, events and `state.json` keep the identifiers
below, unchanged; search for these when you script against the API or read
state.

| In the browser | Identifier in configuration, the API and state |
| --- | --- |
| Brokk Town, Town | the service (`bt`) |
| repository | `town`; `towns` in config and state, `/api/towns` |
| bot | `house` on a task, `role` on a worker; role keys in [Bots](#bots) |
| task | `task` |
| worker | `worker` |
| handoff | event kind `delivery` |
| Activity | `events`, `GET /api/events` |
| Simplification, Simplifier Bot | `simplifier`; stage `simplifying` |
| Triage, Mayor Bot | `hall`; `bots/mayor-bot` |
| Implementation, Issue Bot | `issue` |
| decision | `mayoral_decision`; a task awaiting one has stage `awaiting_mayor` |
| Accept | action `admit`; `mayoral_decision` `admitted` |
| Decline, declined | action `decline`; stage and `mayoral_decision` `declined` |
| Delayed | status `delayed`; Mayor Bot answers `delay` for a bot update |
| status report | `bulletins`, `bulletin_seconds` |
| External | `outside` |
| You | `operator` |
| Assistant | `/api/guide`, package `internal/guide` |
| History | `POST /api/history`, `towns/<key>/history/` |
| Storage | `POST /api/storage`, `POST /api/storage/cleanup` |
| outcomes | `outcomes`, `GET /api/outcomes` |
| scan reports | `reports` |
| blocked | attention statuses (blocked, failed, inconclusive, uncertain write) |
| Global settings | `service_config`: `max_workers`, `quiet_hours`, `attention_hook` |
| Repository settings | `config` on a `town`, including per-bot fields such as `bot_policies` |
| Start, Start all | control action `start` |
| Pause | control action `pause` |
| Stop now | control action `stop` |
| Demo | demo mode: `bt --demo`, `demo` in state |
