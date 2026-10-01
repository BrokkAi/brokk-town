# How work moves

Town is a pipeline. Repo Bot observes the repository; every other house does work
Town decides it should do. A task always has a **house** (which bot owns it) and a
**stage** (where it is in that house's work). Town moves a task only when the
repository changed, a bot returned a result, or an operator acted.

## The loop

1. **Observe.** Repo Bot reads the branch, its head, every issue, pull request and
   release, and the commits gained since the last look. Town applies that
   inventory to its own task state; it also makes a few bounded GitHub writes of
   its own — a merge, closing declined work, filing follow-ups, submitting a
   request — each gated on a fresh read of the exact object.
2. **Reconcile.** Town turns the inventory into tasks, marks closed and locked
   items, records new arrivals as events, and resets the audit when a revision
   changes. Absence in an incremental scan never means deletion.
3. **Route.** Town applies each house's work policy and gives the task to the
   house that should act next.
4. **Dispatch.** One house runs one focused session. Progress and log lines are
   streamed into durable state.
5. **Commit.** Town records the result, ownership, audit, branch health, outcome
   and budget charge, and moves the task.

Nothing is dispatched twice. A revision gets at most two attempts at any step,
and an interrupted dispatch becomes a recovery hold rather than a silent retry.
See [recovery.md](recovery.md).

## Houses

| House | Role | Owns |
| --- | --- | --- |
| Bug greenhouse | `bug` | Filing new bug issues from repository investigation. |
| Feature study | `feature` | Filing new feature proposals. |
| Simplifier clarifier | `simplifier` | Judging whether an arrival is worth doing. |
| Town Hall | `hall` | Mayoral decisions and the town bulletin. |
| Issue workshop | `issue` | Implementing issues and repairing Town-owned PR branches. |
| Review observatory | `review` | Reviewing and certifying exact revisions, and merging when policy allows. |
| Release depot | `release` | Batching unreleased commits and publishing a verified release. |
| Repo watchtower | `repo` | Inventorying the repository and repairing the covered branch. |

Repo Bot starts enabled. Every other house starts paused and does nothing until
you start it.

## Issue lifecycle

```
arrival → simplifying → awaiting_mayor → queued → (issue works) → fixes → ready → merged
```

- A new issue from outside is `simplifying`: Simplifier reads it. In `suggest`
  mode it attaches advice and the issue goes to `awaiting_mayor`; in `auto` mode
  Town can decline low-value work outright.
- A Mayoral decision is `pending` until you `admit` or `decline` it, or the Mayor
  decides. A decline is final; Town closes the issue.
- `queued` means the issue workshop can take it. The bot claims the issue, works
  from the town's base branch, and opens a pull request it owns.
- Review feedback on a Town-owned pull request becomes `fixes`: issue-bot gets one
  repair round on the same branch. After the repair is confirmed on GitHub the
  pull request returns to `queued` for a second review.
- If the second review still finds blocking work, Town closes its own pull request
  and starts the issue over from the current base branch. A contributor's pull
  request is never closed by Town; it goes to the Mayor instead.

## Pull request lifecycle

```
arrival → simplifying → awaiting_mayor → queued → ready → merged
```

- The first review sends every certified finding back to the issue workshop for one
  fix round.
- The second review either finds only work below the close threshold — which
  becomes follow-up issues and the pull request merges — or still finds blocking
  work.
- The close threshold is `review_close_severity` (`P1`, `P2` or `P3`, default
  `P2`). An unrated finding counts as blocking.
- Retargeting a pull request off the town's branch retires it from this town until
  it targets the branch again, when a fresh review decides it.
- A closed pull request's issue starts over: the new attempt never counts the old
  pull request again.

### Merge gates

Town merges only when all of these hold at the moment of the merge:

- the pull request targets the branch this town covers;
- the certified audit matches the exact base, head, description and discussion;
- GitHub reports the required checks green, with no pending, failed, cancelled or
  skipped run;
- required approvals are satisfied;
- the pull request is open, unlocked, not a draft, and its head has not moved;
- the operator has not snoozed it or set `merge_policy` to `manual`;
- it is not inside quiet hours.

Before merging, Town submits an expected-head merge and records the intent first,
so an uncertain outcome is reconciled by the next inventory instead of being
repeated blindly.

### Merge policy

| Policy | Behaviour |
| --- | --- |
| `bot` (default) | Town merges pull requests it created. |
| `all` | Town also merges external pull requests it certified. |
| `manual` | Town never merges, and Release Bot is paused. |

## Simplifier

Two modes, set in the town's settings in the browser:

- **suggest** — Simplifier attaches a bounded recommendation to a Mayoral
  decision; the Mayor decides.
- **auto** — Town admits routine work and declines low-value complex work without
  a separate Mayoral decision. An operator can still admit an auto-decline before
  the closer claims it.

## Mayor

Town Hall has two one-shot duties:

- **judge** — decide an arrival: an issue, a pull request, or a bot proposal.
  Town gives the Mayor the arrival, any Simplifier advice, the GitHub discussion
  and the repository, and records a decision with a reason.
- **bulletin** — write the work-completed feed. Bulletins are written at most once
  per `bulletin_seconds`, and only when something merged since the last one.

## Release cadence

Release Bot watches unreleased commits on the covered branch and publishes when
the cadence says so:

- a **daily deadline** (`release_daily_seconds`, default 24h) after which
  unreleased commits are released regardless of the other rules;
- a **burst** (`release_burst` commits inside `release_burst_window_seconds`,
  default 5 in 2h) that releases early, provided the **minimum gap**
  (`release_minimum_gap_seconds`, default 2h) since the last release has passed
  and the branch has been **quiet** (`release_quiet_seconds`, default 15m).

Between those rules the agent decides: Town runs a short read-only **triage**
session asking whether the unreleased range contains anything users need
promptly, such as a security, crash, data-loss or regression fix. A failure of
triage never blocks the daily deadline.

A release goes through preparation, publishability preflight, publication and
independent verification. The prepared commit and tag are binding; the agent may
not weaken checks or rewrite a published tag. If publication is partial, the next
attempt reconciles it rather than republishing from scratch. See
[README.md](../README.md#releasing) for this repository's own release.

## Deferral

The browser's snooze action pauses one task until a chosen time. Before then Town
starts no agent work and makes no merge for it; the rest of the house's queue
keeps moving. At the resume time the task rejoins its queue on its own, and
Resume now clears the snooze. A snooze is operator state: nothing observed on
GitHub changes it.

## Archival

Closed, merged and shipped tasks are archived after thirty days, at most 500 per
inventory. Active, blocked, pending-decision, recovery, follow-up and source-cursor
tasks stay hot. Archived tasks keep their identities, decisions and evidence, are
readable in History, and are restored automatically if they reopen. See
[storage.md](storage.md).

## Events

Every delivery and decision is appended to the town's event log and streamed to
connected clients. Events are bounded (the most recent 512) and carry a sequence,
time, town, kind, from/to houses, cargo and title. The browser board reads them
from the same committed state.
