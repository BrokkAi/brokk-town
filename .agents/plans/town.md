# Brokk Town implementation plan

## Product

A town application that runs and supervises the Brokk bots. Houses represent
workers; their queues, activity and errors are inspectable. Animated wheelbarrows
carry confirmed work between houses. Delivery trucks bring external issues and
pull requests into town; release shipments leave town. Animation follows durable
work events and never causes, delays, or stands in for a GitHub mutation.

The user asked for the full path: bug discovery → filed issue → implementation PR
→ independent review → fixes → another review → merge → accumulated commits →
release. Repo-bot reports incoming changes, queue growth and repository health.

## Repository inspection

- brokk-town contains only go.mod (Go 1.27.1) and an initial commit; no origin.
- All four existing Go bots expose Run and owned WithProgress snapshots.
- Their --json CLI output is a log stream, not a stable task-handoff protocol.
- issue-bot considers opening a PR terminal, and skips issues with any linked PR.
  It needs an explicit mode for revising its existing PR from review feedback.
- issue-bot defaults to draft PRs; review-bot intentionally skips draft PRs.
  Town must deliberately mark implementation-ready PRs ready for review.
- review-bot posts COMMENT reviews. Zero newly published findings is not a clean
  verdict: findings may be suppressed as duplicates or remain uncertain.
- review-bot binds publication to exact base/head SHAs. Town must retain that
  constraint and invalidate merge readiness on any relevant revision change.
- release-bot already observes the release branch and batches unreleased commits.
  Town must observe its durable release receipts rather than invent a second
  publisher or infer a release from a successful worker exit.

## Chosen decisions

- Browser and TUI share a local Go service. The browser renders the animated town;
  the TUI provides a compact operator view. Neither owns worker lifetimes.
- One repository is one town, as agreed in the user's design follow-up. Several
  towns share the local machine and four non-reporter worker slots. Both clients
  have a cross-town overview and a repository switcher.
- Default merging covers Town-created PRs only, after current clean evidence and
  GitHub checks/approvals. Manual and all-eligible policies are configurable.
- Original generated sprite atlases supply houses, workers, wheelbarrows, trucks.
  No external sprite pack is bundled. Local serving is the intended deployment.

## Initial architecture

Use Go and the existing ACP runners. Separate the town model, worker supervision,
GitHub reconciliation, persistent state, and presentation. The service exposes
one command API, snapshots, and a sequenced event stream for browser, TUI and CLI
clients. Reconnecting clients receive a consistent snapshot and cursor, then
resume events without replaying old arrivals. A local browser UI is served by the
service; public hosting is not required. Bind locally by default, validate origins
and protect control requests. Remote access is a separate explicit configuration.
No network, filesystem scanning or agent work belongs on a render/input loop. An agent can
be busy, waiting, paused, blocked, stopped or failed; these have distinct visuals.

Work identity contains repository, issue/PR number and revision. Delivery events
also have stable identities so polling, restarts and repeated worker snapshots
do not generate repeated wheelbarrows or rerun a task. A delivery carries a
reference to real work and its destination, not a copy of mutable GitHub state.

GitHub remains the authority for issues, PRs, checks, reviews, merges and releases.
The town owns scheduling and local evidence. Workers retain responsibility for
idempotent domain mutations, private worktrees and their existing durable intent
handling. Commands and facts are separate: a scheduled review is not a completed
review; an attempted merge is not a merged commit; an attempted publish is not a
release shipment. Ambiguous writes require reconciliation before retry.

## Review and repair contract

Review outcomes are clean, changes_needed or inconclusive, bound to base/head and
the discussion snapshot. Each unresolved finding remains tracked until verified
as resolved or explicitly dismissed with evidence. Comment deduplication never
resolves a finding. Inconclusive, stale and failed reviews cannot unlock merging.
A clean verdict must include complete required review coverage.

Issue-bot repairs the current bot-owned PR branch without force-pushing, duplicating
the PR or stealing a contributor branch. It consumes explicit finding identities,
records what changed and which checks ran, then requests review of the new head.
Conflicts, repeated ineffective fixes and exhausted attempts become visible
blocked work. Review/repair cycles have configurable attempt and time budgets.
External PRs enter review; modifying external contributor branches requires an
explicit supported ownership policy. External issues enter the issue queue.

Merging is a separate town action governed by the selected policy, a current clean
review, satisfied GitHub checks/approvals, non-draft/open/mergeable status and an
exact expected head SHA. Never bypass branch protections. Only a confirmed merged
commit moves to the release house.

## Repo-bot and town hall

Repo-bot reconciles paginated GitHub state and reports deltas: new issues/PRs,
merges, release-branch commit titles, releases, growing queues and stalled work.
The initial report is an inventory, not a burst of newly arrived trucks. Later
observed arrivals generate deliveries exactly once. Reports persist in town hall;
quiet summaries run on a configurable cadence and notable changes trigger an
immediate report. No Slack/email destination is implied by this request.

## Implementation checkpoints

1. Domain model and behavior tests for revision-aware routing, feedback loops,
   merge gates and exactly-once delivery observations.
2. Selected town renderer and clearly labeled demo covering every handoff; compact
   operator layout, keyboard inspection, resize and responsive cancellation.
   If both surfaces are selected, build the animated browser town and a compact
   TUI against the same service; do not duplicate business rules between clients.
3. Read-only GitHub reconciliation and repo-bot reports driven by real state.
4. Supervised real workers, durable routing and operator controls.
5. issue-bot PR repair and review-bot complete review outcomes, independently
   tested with fake GitHub/ACP fixtures before connecting the loop.
6. Selected merge policy, release observation, full restart/recovery validation,
   repository documentation, licensing, and distribution structure.

Validate transition invariants, event replay, cancellation and terminal width in
behavior tests. Use simulated agents and GitHub fixtures for writes; do not run
live autonomous work against the user's repositories as a development test.

## Implementation delivered

- Persistent Go town model, process-group cancellation, four shared worker slots,
  per-role released bot adapters, revision-aware routing, exact write intents,
  and explicit saved-commit retry for uncertain repair pushes.
- Full-change review certification with retained finding/discussion IDs, PR
  description binding, clean-worktree/HEAD checks, repair ownership and ancestry
  checks, automatic fix/review loop, merge gates, release ancestry confirmation.
- Authenticated loopback service and sequenced snapshot stream; browser world,
  multi-town cards, inspectors and controls; responsive TUI with an all-town
  view, queues, logs, controls, paste filtering, and terminal restoration.
- Isolated two-town demo covering all handoffs. No live automation was run during
  development. Optional WebMCP read/navigation tools have unit contract coverage;
  no supported browser WebMCP context was available for in-browser verification.
- Apache license, attribution, reviewed dependency inventory and complete legal
  notices, artwork provenance, setup/configuration/architecture docs, CI checks.
- Validation: go test -race ./..., go vet ./..., frontend syntax and behavioral
  tests, license checker and its tests; real demo HTTP/PTY smoke covering assets,
  auth, SSE, multi-town controls, bracketed paste, resize, detach, restart.
  Builds also pass for macOS arm64/amd64 and Linux arm64/amd64.
- Local Git/fake-agent tests cover repair publication to an existing branch,
  lost-response confirmation, certification immutability and stale feedback.
  Regression tests cover slow inventory racing a confirmed repair and idempotent
  repair counting across the reporter and worker.

## Boundaries of the first implementation

No standalone bot repository was changed: their released library APIs are reused,
with Town-specific certification and PR repair adapters. No hosted site or live bot work was run. The subsequent public-repository
setup adds the bot-style native/npm packaging and release workflows. No release
tag or npm version is published by the source-publication task. The browser assets were inspected, and frontend contracts were
tested; browser interaction/visual QA was not performed in this environment.
Large repositories may need incremental inventories and task archival. Merge
queues and non-squash merge strategies currently require manual merging. Real
agent judgment and repository-specific publishing policy need an operator-chosen
live town before they can be exercised end to end.

## Public repository and distribution setup

The user authorized creating a public repository and pushing this implementation,
using the existing bots as the reference for licensing and deployment. Created
https://github.com/BrokkAi/brokk-town and pushed master with origin tracking.
Adapted review-bot's pinned Linux/macOS CI, native release archives, checksum
installer, npm launcher/platform packages, integrity checks, and tag-only OIDC
workflow. GitHub recognizes Apache-2.0; effective collaborator access matches
review-bot. The packages-publish environment accepts v* tags only. Default
workflow tokens are read-only, with write permissions scoped to publishing jobs.

Local application, API, terminal, package/installer tests, license checks, and
workflow lint passed. All four native archives and five npm packages were built
and verified, then an offline npm install successfully launched bt. The hosted
macOS smoke exposed a PTY output-drain issue in the test harness; keep reading
while waiting for the TUI's screen-restoration output and process exit. CI checks
both platforms independently so a failing platform does not cancel the other.

No release tag or npm package was published. All five new npm package names
currently return 404. npm-side trust requires bootstrap publication during the
first release; the prepared OIDC workflow alone is not actual npm trust. The
release guide records bootstrap, access matching, trust registration, validation,
and release/recovery steps.
