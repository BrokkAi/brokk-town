# Local service and repository towns

`cmd/bt` owns the foreground service, optional explicit background launch, local
connection file and CLI. Browser and CLI consume the same committed state.
Clients never auto-start or replace a service. There is no terminal UI.

Each `bots/<project>` is an independent Go module and executable. The root
`bundle.json` selects their supported versions; installation bundles all binaries.
Town does not import any bot's Go packages or resolve bots through npm at runtime.

Town starts eight persistent workers per configured town on private Unix sockets.
Idle and paused workers remain alive; only dispatched jobs start agent processes.
The supervisor applies capacity, scheduling, pause and authority controls to jobs.
Each worker serializes jobs and may handle many sequential runs during its lifetime.
Issue Bot also serves read-only job summaries while a job is running; its retry
operation owns its state changes.

Worker processes have bounded output, private sockets and a parent-liveness pipe.
Shutdown cancels work, closes parent pipes, terminates workers with bounded
escalation, and reaps children before releasing the state lock. Workers cancel
active work when Town dies unexpectedly. Restart preserves interrupted dispatch
provenance and uncertain write intents; it never adopts an old process or blindly
replays its work.

## Operator projections

Town, Board, and Compact are three views over the same public snapshot and SSE
cursor. Town keeps the animated houses and delivery paths for the selected
repository. Board groups tasks into fixed Open, Queued, Review, Blocked, Ready,
Shipped and Completed columns (empty columns are omitted). Drafts are Open;
queued implementation and repair tasks stay Queued; author/check waits are
Review. Ready review evidence and unreleased commits remain distinguishable.
Only a confirmed `shipped` commit goes into Shipped. Merged PRs, closed work and
implemented issues retain their exact badges in Completed.

Worker cards separately show active runs and failures: a busy house does not
prove which queued task its worker is processing. No exact task-to-session ID
exists in the current worker progress contract. Compact lists every worker and
task, including next-run eligibility, effective profiles and blockers. Unknown
stages stay visibly unknown. Uncertain PR write intents are associated by PR
identity; an issue with the same number cannot inherit a PR's intent.

A selection opens the shared inspector without changing the all-town scope.
The inspector names the repository and offers the existing controls. View and
repository scope persist locally; SSE redraws retain task selection and keyboard
focus by town, surface and identity. Snapshot updates never replay commands.
Animation consumes committed events and may ease their presentation, but it
never starts work, delays a command, or stands in for a GitHub mutation. Active
worker profiles are frozen at dispatch in `worker.agent`; queued work shows the
current public bot profile for its next dispatch. Completed tasks do not claim
that today's profile describes their historical run.

Town deletion marks a durable tombstone, disables scheduling, cancels queued
issue submissions and running contexts, and filters the town and its events out
of public snapshots. Final worker results still commit to its recovery record.
Re-adding waits for workers to stop, then restores the same identity, history, and
private worktrees with automation disabled.

Town config retains a default harness and ACP agent configuration, plus optional
complete `bot_agents` profiles for the bug, feature, issue, review, release,
simplifier, repo, and hall (Mayor Bot) roles.
An absent role inherits the town defaults; an explicit profile owns its harness,
launch definition, model, effort, command, environment, authentication, and mode.
Blank selectors use that profile's harness defaults. Repo Bot carries a profile
like the rest, and starts an agent only to repair a failing branch.
Agent settings preserve private command/authentication fields when changing only
model or effort; switching harnesses resets harness-specific configuration only
within the selected profile. Resetting a role removes its profile, restoring
inheritance. Public snapshots expose effective per-bot harness/model/effort and
inheritance state while omitting private ACP fields and full launch definitions.
Dispatch takes a fresh, role-resolved config snapshot so queued work uses the
latest settings. Running workers and their nested ACP sessions retain their
starting profile; issue repairs use issue-bot's profile and review certification
uses review-bot's profile.
`internal/harness` reads the official ACP registry v1 index, retaining a bundled
offline snapshot and an atomic, validated cache. Authenticated API/CLI refreshes
run independently of town scheduling. The browser displays the cached catalog
immediately and refreshes stale entries in the background. Anvil, Muse ACP, and
Draupnir are explicit supplements resolved from PATH, with setup notes.

Selecting a harness persists its full launch definition in the selected private
profile; public snapshots expose only the ID and version. Catalog refreshes never
mutate saved definitions. A user can explicitly select the new version. Legacy
towns retain their defaults and pin the definition at settings save or first
dispatch. Package runners receive the registry's exact package, arguments, and
environment without a shell. Native
archives install in a private cache keyed by definition and platform, using a
cross-process lock, bounded downloads/extraction, optional registry checksums,
path/link validation, and atomic publication. Preparation runs in worker or
choice-request contexts, not in render or scheduling loops. Installed supplemental
commands retain the user's version. Registry definitions pin launch recipes;
upstream mutable package tags or release assets remain upstream-controlled.

Choice discovery resolves the selected role's profile and first prepares its
harness with a three-minute bound, then uses a temporary ACP session with a
45-second bound, no prompt, and no client tools.
Model selection precedes reading model-specific effort options. Demo discovery
uses fixtures and never starts a process; demo registry refreshes never access
the network. Nested bot sessions reuse the prepared launch from their run.

Feature and bug submissions are durable commands separate from bot scheduling.
The HTTP handler validates and queues a client-generated idempotency ID. A
supervised background publisher persists `uncertain` before the GitHub POST.
A confirmed receipt creates a workshop task without enabling issue-bot. A lost
response or restart only permits paginated reads of open/closed issues for that
ID's marker; absence never permits another POST. Deletion preserves in-flight
outcomes while canceling submissions that have not begun. Demo requests only
create local tasks.

Field scenery is generated once per selected town and cached as a canvas.
Active worker poses and effects read committed worker status. Delivery easing
changes presentation only; pointer hit testing uses the same eased positions.
Reduced-motion preferences freeze worker poses and clear moving deliveries.

## Review, repair, merge, release

Certifications retain findings and discussion IDs across revisions. Strict
structured receipts require a full coverage claim, meaningful check descriptions,
explicit resolution evidence, and a verdict. New defects receive stable `new:`
IDs. Incomplete or uncertain evidence cannot certify clean. Independent sessions
must leave reviewed tracked files and HEAD unchanged, including after operator
verification. GitHub metadata/discussion is checked again before accepting results.

Repairs run on private branches based on the exact PR head. Town proves local
ownership, requires verified current feedback, forbids rewriting history, checks
a clean working tree and a nonempty resulting change, and runs operator checks.
It saves the exact old/new commit, remote branch, and worktree before non-force
publication. A changed discussion reroutes to review. Uncertain publication holds
the saved work; an explicit retry checks and pushes that commit without rerunning
an agent. Exact remote confirmation hands the revised PR back to review.

A merge requires the selected policy, an exact clean audit, current GitHub gate,
and unchanged discussion. Town persists the merge intent, then requests squash
merge with the expected head SHA. The GitHub API supplies no expected-base
parameter: adjacent base checks reduce that race, while GitHub's current branch
protection remains authoritative. Merge queues require operator merging today.

Reconciliation observes confirmed merges and branch advances. It creates commit
cargo for actual changes and summarizes commit titles in repo-bot reports. Release
observations include stable published releases; comparison ancestry proves which
commits shipped. Timestamps and worker exit status never prove shipment. The
existing release-bot owns batching, release preparation, and publishing.

## Validation boundaries

Automated tests exercise state/restart, routing and duplicate suppression, review
coverage, merge gates, lost-response intents, fake-agent local Git repair pushes,
audit immutability, pause/stop, API authentication/SSE, terminal sizing/cancellation,
frontend routing, optional page-tool contracts, and fake versioned Unix-socket
workers that exercise initialization, capability negotiation, request identity,
progress, typed results, sequence validation, and shutdown. Demo integration must
not receive live workers or GitHub handles. Tests do not prove an actual ACP
model's judgment or a repository's live branch-protection/release configuration.

The interface is local and authenticated, not a multi-user security boundary.
Agents execute repository commands under the service account. Prompts constrain
the intended workflow, but they are not an OS sandbox. Isolate untrusted work at
the operating-system level. The first implementation polls complete GitHub
inventories; large repositories may need incremental synchronization and archival
policies in a later iteration.

## Automatic feature discovery

Feature-bot is a separate Go/ACP bot and `bfb` CLI, modeled on bug-bot. Town
starts its protocol worker with the feature role's effective harness and an
isolated feature workspace.
Both discovery workers wait 30 minutes between successful attempts and share the
same global worker cap with implementation, review and release workers.
Feature proposals require a user problem, current workflow, proposed behavior,
user value, bounded scope, testable acceptance criteria and repository evidence.
The bot independently reviews the proposal and compares all existing issue
history before publishing through a durable request intent.

Town never turns a successful discovery exit into an issue delivery. Only the
GitHub inventory's observed `feature-bot` receipt marker routes a new issue from
the feature study to the issue workshop. The initial inventory stays a baseline;
repeated inventories do not replay arrivals. Existing saved towns gain an absent
feature worker paused, preserving every other worker's settings. The closed demo
simulates feature research and its confirmed delivery without an agent or GitHub.

## Town Hall and Mayor Bot

Mayor-bot is a separate Go/ACP worker modeled on simplifier-bot; Town Hall is
its house and it is paused by default. Each run is one duty. A `judge` run names
the next arrival awaiting a Mayoral decision (lowest task ID first, skipping
arrivals backing off a failed attempt) and carries `arrival`, Town's JSON
description of it: kind, title, Simplifier advice, the Town review, or the bot
update on offer. The bot reads the live GitHub source itself, judges in a
detached worktree at the exact revision, and returns `result.judgment`. Town
applies it through `decideTask`, the path the Mayor's own clicks use, with the
reason appended to the task; a failed run increments the arrival's attempts,
backs it off fifteen minutes, and after three attempts leaves it for a person.
One arrival's failure never stops the house.

A `bulletin` run is dispatched when nothing waits to be judged, at least
`bulletin_seconds` passed since the last bulletin, and a confirmed merge
outcome landed after it. It carries `since` and `until`; the bot summarizes the
pull requests merged in that window for users and returns `result.bulletin`
with classified items and the pull requests covered. Town appends it to
`Town.Bulletins`, refusing a window that does not start where the last one
ended, and shows the feed in Town Hall.

## Simplifier intake and discovery

Simplifier-bot is a separate Go/ACP worker modeled on bug-bot. Town starts it
with the simplifier role's effective harness and a private workspace. Every new
GitHub issue and PR is first routed to a durable `simplifying` task, including
bug/feature findings and Town-owned implementation PRs. An item dispatch sends
the exact issue/PR number plus the town's `suggest` or `auto` mode.

In suggest mode, the worker returns a bounded admit/decline assessment that
Town attaches to the subsequent Mayoral decision. In auto mode, Town applies
that assessment itself: admitted work moves to Issue Bot or Review Bot, declined
contributor PRs are ignored, and declined issues are closed by Repo-bot through
the normal idempotent closure path. A declined Town-owned PR is claimed as
`closing` and closed through the same path as one that failed review, which
deletes its branch and starts its issue over. A repository scan can file simplifier proposals using
the bot's durable request marker; those marked issues bypass recursive intake,
then follow the mode's normal decision route.

Assessment worktrees are detached at an exact revision and checked for tracked
edits afterward. The worker never closes GitHub issues itself; Town preserves
one reconciliation and write-intent path for closures.

## ACP relationship and future execution

Brokk Town is an operations view and scheduler for independent Brokk bot executables.
It dispatches their work through the worker protocol; each bot runs its ACP agent.
Town does not depend
on Mjolnir or `mj` as an executor and does not introduce a second ACP scheduler.
Mjolnir remains an independent ACP control plane that can be used to run or
inspect agents outside a town. Shared ACP conventions can improve interoperability,
but a Town dispatch is owned by Town's durable state, worker reservation, and
reconciliation rules.

The planned Mjolnir integration uses a town target default with per-bot overrides.
Mjolnir owns execution capacity for Mjolnir-backed work; Town displays that
capacity read-only. Local work retains Town's concurrency bound. These are design
decisions, not implemented settings. See [Mjolnir execution](mjolnir-execution.md)
for inheritance, capacity ownership, and the dependent implementation work.
Town keeps its fixed bot-role projection and does not expose programmable
workflows. Usage quotas, budgets, and spending limits remain separate concerns.
