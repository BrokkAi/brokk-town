# Local service and repository towns

`cmd/bt` owns the service lifecycle, local connection file, CLI, and TUI.
`internal/town` owns persistent facts, routing, GitHub reads/writes, and worker
scheduling. `internal/web` embeds the browser application and exposes the same
state/commands to both clients. The service outlives client connections.

A town ID is the lowercase GitHub repository slug. State is keyed by town, then
issue/PR number or commit SHA. Branches and worktrees remain within the town.

Input funnels split discovery from scheduling and provider feedback. An adapter
discovers or refreshes source items and returns a normalized `WorkItem` whose
identity is `(funnel, provider, source item ID)`. The durable record retains
source URL, revision, cursor, external state, eligibility, capabilities, explicit
priority policy, and a typed outcome. Funnel order has no scheduling meaning;
overlap behavior is an explicit independent/deduplicate/reject policy.

Adapters receive only a `SecretRef`, resolve its value locally at call time, and
never copy credential values into normalized records. The model-facing action
surface is limited to `claim`, `report_blocked`, `request_human`, and
`report_complete`; adapters translate configured actions into source vocabulary.
Every consequential write is preceded by a durable `WriteIntent`. Confirmed
receipts close the intent; timeouts and lost responses remain uncertain and must
be reconciled by a source read before any retry. An absent receipt does not prove
failure or authorize a duplicate write.

The GitHub adapter covers queries, selected issue identities, label filters, and
working/blocked label transitions. Existing mature GitHub PR review, merge, and
release reconciliation remains authoritative while issue intake migrates through
the funnel boundary. The Slack adapter proves a different provider vocabulary
with paginated channel reads, reactions, and bounded thread replies over an
injected HTTP client. Provider capabilities explicitly distinguish supported,
read-only, unmapped, and unsupported transitions. Typed incomplete, partial,
authentication, rate-limit, unsupported, revision-conflict, and uncertain states
remain visible; zero items under incomplete coverage is never a clean queue.
The service persists one global `service_config.max_workers` setting (default 4,
validated from 1 through 64). It reserves simultaneous non-reporter workers
across repositories; repo reporters, durable issue publishing, and prompt-free
model discovery run separately. `state.capacity.active` is the live reservation
count and `state.capacity.limit` is the configured limit. A demo derives active
capacity from its simulated `working` and `pausing` workers; a live service
reports scheduler reservations, so stale worker status cannot create a slot.
Capacity edits are serialized with reservations. Lowering the limit cancels no
runs and blocks new bot dispatch until usage is below the limit. Raising it and
releasing a slot wake the scheduler immediately; every town/role remains
exclusive. Reservations include startup and cancellation cleanup, including a
deleted town until its worker exits. Runtime counts reset to zero on restart;
only the configured limit is durable. Legacy state with no setting inherits 4;
present invalid settings fail validation.

Each role has its own managed checkout and bot
state so independent houses do not share a working directory.

The released bot executables remain responsible for their primary operations.
Town does not compile the bot packages into `bt` or read their private state
files. Each primary dispatch starts the corresponding `bbb`, `bfb`, `bib`,
`brv`, or `brb` worker service on a private Unix socket, negotiates protocol
version and capabilities, streams ordered progress, and consumes only explicit
public results. Issue-bot receives implementation-ready PR settings, and Town
adds an explicit repair path for its owned PRs. Review-bot's exact-revision
result is an input to a separate full-change certification. Town never treats a
successful exit or zero new review comments as merge permission. The complete
contract is specified in [WORKER_PROTOCOL.md](WORKER_PROTOCOL.md).

## Facts and concurrency

All state transactions clone, validate, serialize, fsync, rename, and sync the
parent directory before reporting success. Only one daemon can hold the state
lock. Read snapshots are owned copies. Event sequence numbers survive restarts;
the server streams the complete public snapshot and cursor on every update.
Clients animate only newly observed delivery events. Recent event, log, and report
rings are bounded; task identities and external-write intents remain durable.

Worker progress and log callbacks use bounded channels and never wait for disk.
Their consumer persists observations; final results use a separate transaction.
A persistence error stops the supervisor. An initial state/intent write must
succeed before starting work or issuing a push/merge. Shutdown cancels process
groups and waits for all workers before releasing the store lock.

Pause disables scheduling while allowing active work to finish. Stop disables
scheduling and cancels the worker context. The scheduler rechecks enabled state
before entering a worker, preventing a stale scheduling snapshot from restarting
an already-stopped house. Each worker has a deadline and a per-role next-run time.

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
complete `bot_agents` profiles for bug, feature, issue, review, and release roles.
An absent role inherits the town defaults; an explicit profile owns its harness,
launch definition, model, effort, command, environment, authentication, and mode.
Blank selectors use that profile's harness defaults. Repo-bot has no agent.
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

## ACP relationship and future execution

Brokk Town is an operations view and scheduler for released Brokk bot libraries.
It consumes ACP-compatible bot runs through those libraries; it does not depend
on Mjolnir or `mj` as an executor and does not introduce a second ACP scheduler.
Mjolnir remains an independent ACP control plane that can be used to run or
inspect agents outside a town. Shared ACP conventions can improve interoperability,
but a Town dispatch is owned by Town's durable state, worker reservation, and
reconciliation rules.

Adding a future general-purpose executor requires an explicit contract for
ownership, cancellation, restart reconciliation, and the authority that owns
capacity reservations. Until those rules are specified, Town keeps its fixed
bot-role projection and does not expose programmable workflows. Usage quotas,
budgets, and spending limits remain a separate issue-6 concern; the worker
capacity setting is only a local concurrency bound.
