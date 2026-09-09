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
