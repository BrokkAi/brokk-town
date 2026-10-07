# How work moves

SlopCop Squad is a pipeline. In each precinct, Patrol (Repo Bot) observes the
repository; every other unit does work the Squad decides it should do. A case
always has a `house` (which unit, and so which bot, owns it) and a `stage`
(where it is in that unit's work). The Squad moves a case only when the
repository changed, a bot returned a result, or the operator acted. The browser
speaks of precincts, units, cases and rulings; configuration, the API and state
keep their original identifiers. The [glossary](#glossary) maps one to the
other.

## The loop

1. **Observe.** Repo Bot reads the branch, its head, every issue, pull request and
   release, and the commits gained since the last look. The Squad applies that
   inventory to its own case state; it also makes a few bounded GitHub writes of
   its own — a merge, closing work sent to the Slop Tank, filing follow-ups,
   submitting a request — each gated on a fresh read of the exact object.
2. **Reconcile.** The Squad turns the inventory into cases, marks closed and
   locked items, records new arrivals as events on the Radio, and resets the
   audit when a revision changes. Absence in an incremental scan never means
   deletion.
3. **Route.** The Squad applies each unit's work policy and transfers the case
   to the unit that should act next.
4. **Dispatch.** One unit runs one focused session. Progress and log lines are
   streamed into durable state.
5. **Commit.** The Squad records the result, ownership, audit, branch health,
   outcome and budget charge, and moves the case.

Nothing is dispatched twice. A revision gets at most two attempts at any step,
and an interrupted dispatch becomes a recovery hold rather than a silent retry.
See [recovery.md](recovery.md).

## Units

Units are listed in roster order, which follows a case through the precinct and
is also the order of keys 1–8.

| Unit | Callsign | Role key | Bot | Owns |
| --- | --- | --- | --- | --- |
| Patrol | PTL | `repo` | Repo Bot | Inventorying the repository and repairing the covered branch. |
| Bug Detective | BUG | `bug` | Bug Bot | Finding new slop: filing new bug issues from repository investigation. |
| Feature Detective | FTR | `feature` | Feature Bot | Finding new slop: filing new feature proposals. |
| Magistrate | MAG | `simplifier` | Simplifier Bot | Arraigning every arrival: is it worth doing? |
| Probation Judge | JDG | `hall` | Judge Bot | Rulings and the precinct's blotter. Never edits the repository. |
| Caseworker | CWK | `issue` | Issue Bot | Rehabilitating slop on probation: implementing issues and repairing the branches of PRs the Squad owns. |
| Forensics | LAB | `review` | Review Bot | Reviewing and certifying exact revisions, and merging when policy allows. |
| Release | REL | `release` | Release Bot | Batching unreleased commits and publishing a verified release. |

Patrol books what comes in, the Bug Detective and the Feature Detective find
new slop, and civilian reports (CIV) arrive from outside. Every arrival is
first arraigned before the Magistrate, then the Probation Judge rules on it:
probation sends an issue to the Caseworker and a pull request to Forensics, and
anything thrown out is locked in the Slop Tank. The Caseworker rehabilitates
slop on probation, Forensics examines the evidence and clears it or sends it
back, and Release ships the reformed. The browser's Board view shows this case
flow as columns.

Patrol starts deployed (enabled). Every other unit starts stood down (paused) and
does nothing until you deploy it.

## Issue lifecycle

```
arrival → simplifying → awaiting_mayor → queued → (issue works) → fixes → ready → merged
```

- A new issue from outside is `simplifying`: it is at arraignment before the
  Magistrate. In `suggest` mode the Magistrate attaches advice and the issue
  goes to `awaiting_mayor` (awaiting the Probation Judge's ruling); in `auto`
  mode the Magistrate can send low-value work to the Slop Tank outright.
- A ruling (`mayoral_decision`) is `pending` until you grant probation
  (`admit`) or send it to the tank (`decline`), or Judge Bot rules. The tank is
  final; the Squad closes the issue.
- `queued` means the Caseworker can take it. The bot claims the issue, works
  from the precinct's base branch, and opens a pull request it owns.
- Review feedback on a pull request the Squad owns becomes `fixes`: issue-bot
  gets one repair round on the same branch. After the repair is confirmed on
  GitHub the pull request returns to `queued` for a second review.
- If the second review still finds blocking work, the Squad closes its own pull
  request and starts the issue over from the current base branch. A
  contributor's pull request is never closed by the Squad; it goes to the
  Probation Judge for a ruling instead.

## Pull request lifecycle

```
arrival → simplifying → awaiting_mayor → queued → ready → merged
```

- The first review sends every certified finding back to the Caseworker for one
  fix round.
- The second review either finds only work below the close threshold — which
  becomes follow-up issues and the pull request merges — or still finds blocking
  work.
- The close threshold is `review_close_severity` (`P1`, `P2` or `P3`, default
  `P2`). An unrated finding counts as blocking.
- Retargeting a pull request off the precinct's branch retires it from this
  precinct until it targets the branch again, when a fresh review decides it.
- A closed pull request's issue starts over: the new attempt never counts the old
  pull request again.

### Merge gates

The Squad merges only when all of these hold at the moment of the merge:

- the pull request targets the branch this precinct covers;
- the certified audit matches the exact base, head, description and discussion;
- GitHub reports the required checks green, with no pending, failed, cancelled or
  skipped run;
- required approvals are satisfied;
- the pull request is open, unlocked, not a draft, and its head has not moved;
- the operator has not snoozed it or set `merge_policy` to `manual`;
- it is not inside quiet hours.

Before merging, the Squad submits an expected-head merge and records the intent
first, so an uncertain outcome is reconciled by the next inventory instead of
being repeated blindly.

### Merge policy

| Policy | Behaviour |
| --- | --- |
| `bot` (default) | The Squad merges pull requests it created. |
| `all` | The Squad also merges external pull requests it certified. |
| `manual` | The Squad never merges, and Release stays stood down (paused). |

## Magistrate

Every arrival is arraigned before the Magistrate (`simplifier`), staffed by
Simplifier Bot. Two modes (`simplifier_mode`), set in Precinct settings in the
browser:

- **suggest** — Simplifier Bot attaches a bounded recommendation to a ruling;
  the Probation Judge rules.
- **auto** — The Magistrate sets routine work free and sends low-value complex
  work to the Slop Tank without a separate ruling. You can still grant
  probation anyway before the closer claims it.

## Probation Judge

The Probation Judge (`hall`), staffed by Judge Bot (`bots/mayor-bot`), sits in
Probation Court and has two one-shot duties:

- **judge** — rule on an arrival: an issue, a pull request, or a bot proposal.
  The Squad gives Judge Bot the arrival, any advice from the Magistrate, the
  GitHub discussion and the repository, and records a ruling with a reason.
- **bulletin** — write the blotter, the work-completed feed. Blotter entries
  (`bulletins`) are written at most once per `bulletin_seconds`, and only when
  something merged since the last one.

In the browser a pending ruling offers **Grant probation** (`admit`) and
**Send to the tank** (`decline`); after a review that asked for changes,
granting probation reads **Review again**. Probation sends an issue to the
Caseworker and a pull request to Forensics. A case the Magistrate sent to the
tank in `auto` mode offers **Grant probation anyway**. Rulings waiting on you
also appear under **Awaiting your ruling** in Needs you.

## Release cadence

Release Bot watches unreleased commits on the covered branch and publishes when
the cadence says so:

- a **daily deadline** (`release_daily_seconds`, default 24h) after which
  unreleased commits are released regardless of the other rules;
- a **burst** (`release_burst` commits inside `release_burst_window_seconds`,
  default 5 in 2h) that releases early, provided the **minimum gap**
  (`release_minimum_gap_seconds`, default 2h) since the last release has passed
  and the branch has been **quiet** (`release_quiet_seconds`, default 15m).

Between those rules the agent decides: the Squad runs a short read-only
**triage** session asking whether the unreleased range contains anything users
need promptly, such as a security, crash, data-loss or regression fix. A failure
of triage never blocks the daily deadline.

A release goes through preparation, publishability preflight, publication and
independent verification. The prepared commit and tag are binding; the agent may
not weaken checks or rewrite a published tag. If publication is partial, the next
attempt reconciles it rather than republishing from scratch. See
[README.md](../README.md#releasing) for this repository's own release.

## Deferral

The browser's snooze action holds one case until a chosen time. Before then the
Squad starts no agent work and makes no merge for it; the rest of the unit's
caseload keeps moving. At the resume time the case rejoins its caseload on its
own, and Resume now clears the snooze. A snooze is operator state: nothing
observed on GitHub changes it.

## Archival

Closed, merged and shipped cases are archived after thirty days, at most 500 per
inventory. Active, blocked, pending-ruling, recovery, follow-up and source-cursor
cases stay hot. Archived cases keep their identities, decisions and evidence, are
readable in the Case archive, and are restored automatically if they reopen. See
[storage.md](storage.md).

## Events

Every transfer (event kind `delivery`) and ruling is appended to the precinct's
event log (`events`) and streamed to connected clients. Events are bounded (the
most recent 512) and carry a sequence, time, `town` (the precinct id), kind,
from/to units, cargo and title. The browser's Radio reads them from the same
committed state: a transfer names both ends by callsign, with CIV for a civilian
report from outside (`outside`) and CPT, the captain, for your own action
(`operator`).

## Glossary

The browser uses precinct terms. Configuration, the local API, events and
`state.json` keep the identifiers below, unchanged; search for these when you
script against the API or read state.

| In the browser | Identifier in configuration, the API and state |
| --- | --- |
| the Squad, SlopCop Squad | the service (`scs`) |
| precinct | `town` (one repository); `towns` in config and state, `/api/towns` |
| unit | `house` on a case, `role` on a worker; role keys in [Units](#units) |
| case | `task` |
| officer | `worker` |
| transfer | event kind `delivery` |
| Radio | `events`, `GET /api/events` |
| Magistrate | `simplifier` |
| at arraignment, Arraignment | stage `simplifying` |
| Probation Judge | `hall` |
| Judge Bot | `bots/mayor-bot` |
| Caseworker | `issue` |
| ruling | `mayoral_decision`; a case awaiting one has stage `awaiting_mayor` |
| grant probation | action `admit`; `mayoral_decision` `admitted` |
| send to the tank, Slop Tank, dismissed | action `decline`; stage and `mayoral_decision` `declined` |
| continued | status `delayed`; Judge Bot answers `delay` for a bot update |
| the blotter | `bulletins`, `bulletin_seconds` |
| civilian report (CIV) | `outside` |
| CPT (the captain: you) | `operator` |
| Desk Sergeant | `/api/guide`, package `internal/guide` |
| Case archive | history: `POST /api/history`, `towns/<key>/history/` |
| Evidence locker | storage: `POST /api/storage`, `POST /api/storage/cleanup` |
| clearance stats | `outcomes`, `GET /api/outcomes` |
| patrol reports | `reports` |
| stuck | attention statuses (blocked, failed, inconclusive, uncertain write) |
| Squad settings | `service_config`: `max_workers`, `quiet_hours`, `attention_hook` |
| Precinct settings | `config` on a `town`, including per-unit fields such as `bot_policies` |
| Deploy, Start patrol, Deploy the rest | control action `start` |
| Stand down | control action `pause` |
| Stop now | control action `stop` |
| training exercise | demo mode: `scs --demo`, `demo` in state |
