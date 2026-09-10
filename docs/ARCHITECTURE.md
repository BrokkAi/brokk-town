# Local service and repository towns

`cmd/bt` owns the service lifecycle, local connection file, CLI, and TUI.
`internal/town` owns persistent facts, routing, GitHub reads/writes, and worker
scheduling. `internal/web` embeds the browser application and exposes the same
state/commands to both clients. The service outlives client connections.

A town ID is the lowercase GitHub repository slug. State is keyed by town, then
issue/PR number or commit SHA. Branches and worktrees remain within the town.
Four shared slots bound simultaneous non-reporter workers across repositories;
repo reporters run separately. Each role has its own managed checkout and bot
state so independent houses do not share a working directory.

The released bot libraries remain responsible for their primary operations.
Town invokes one-shot `Run` calls with progress observers, gives issue-bot
implementation-ready PR output, and adds an explicit repair path for its owned
PRs. Review-bot's persisted investigation/verification results are inputs to a
separate full-change certification. Town never treats a successful exit or zero
new review comments as merge permission. Standalone bot repositories are unchanged.

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

Town deletion marks a durable tombstone, disables scheduling, cancels queued
issue submissions and running contexts, and filters the town and its events out
of public snapshots. Final worker results still commit to its recovery record.
Re-adding waits for workers to stop, then restores the same identity, history, and
private worktrees with automation disabled.

Agent settings preserve private command/authentication fields when changing only
model or effort; switching harnesses resets harness-specific configuration.
Dispatch takes a fresh config snapshot so queued work uses the latest settings.
`internal/harness` reads the official ACP registry v1 index, retaining a bundled
offline snapshot and an atomic, validated cache. Authenticated API/CLI refreshes
run independently of town scheduling. The browser displays the cached catalog
immediately and refreshes stale entries in the background. Anvil, Muse ACP, and
Draupnir are explicit supplements resolved from PATH, with setup notes.

Selecting a harness persists its full launch definition in private town config;
public snapshots expose only the ID and version. Catalog refreshes never mutate
saved definitions. A user can explicitly select the new version. Legacy towns
pin their definition at settings save or first dispatch. Package runners receive
the registry's exact package, arguments, and environment without a shell. Native
archives install in a private cache keyed by definition and platform, using a
cross-process lock, bounded downloads/extraction, optional registry checksums,
path/link validation, and atomic publication. Preparation runs in worker or
choice-request contexts, not in render or scheduling loops. Installed supplemental
commands retain the user's version. Registry definitions pin launch recipes;
upstream mutable package tags or release assets remain upstream-controlled.

Choice discovery first prepares the harness with a three-minute bound, then uses
a temporary ACP session with a 45-second bound, no prompt, and no client tools.
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
frontend routing, and optional page-tool contracts. Demo integration must not
receive live workers or GitHub handles. Tests do not prove an actual ACP model's
judgment or a repository's live branch-protection/release configuration.

The interface is local and authenticated, not a multi-user security boundary.
Agents execute repository commands under the service account. Prompts constrain
the intended workflow, but they are not an OS sandbox. Isolate untrusted work at
the operating-system level. The first implementation polls complete GitHub
inventories; large repositories may need incremental synchronization and archival
policies in a later iteration.

## Automatic feature discovery

Feature-bot is a separate Go/ACP bot and `bfb` CLI, modeled on bug-bot. Town calls
its shared library with the selected harness and an isolated feature workspace.
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
