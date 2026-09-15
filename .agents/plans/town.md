# Brokk Town implementation plan

## Cross-town inbox for Mayoral decisions and stuck work (2026-09-15)

- Pending Mayoral decisions used to be visible only inside one town's Town
  Hall panel, so an operator had to visit every town to find them. The browser
  header now carries a "Needs you" button with a badge that counts pending
  decisions and stuck work across every town; the badge turns amber while any
  decision waits. The `I` key opens the same inbox.
- The inbox is a pure projection (`inbox` in `internal/web/town.js`) over the
  shared snapshot: pending `mayoral_decision` tasks, tasks whose projected
  status is blocked, failed, inconclusive or uncertain-write, and failed
  workers. Items are grouped by repository and ordered longest wait first, and
  each names the town, house and task to open. The same projection feeds the
  sidebar per-town flags, the overview "to decide" stat and the town meta line,
  so every count agrees with the list.
- Opening an item selects that town and inspects the exact house, which for a
  decision is Town Hall with Admit/Decline visible. Admit and Decline are also
  available on the inbox row; they send the existing `/api/control` admit or
  decline action with the item's own town, never the currently selected one.
  Nothing new is written by the service; the inbox re-renders from every
  snapshot, so a decision made elsewhere disappears without a reload. The TUI
  already reported Mayoral decision counts per town and is unchanged.

## Self-managed service lifecycle (2026-09-15)

- The tool owns its lifecycle; the operator installs nothing. Every client
  command (`bt`, `tui`, `web`, `status`, controls) probes `connection.json`
  (PID liveness plus an authenticated state request) and starts the service
  when it is down. Real towns register with the login session through
  `internal/daemon`: a launchd agent (`KeepAlive`, `RunAtLoad`, bounded
  throttle) on macOS or a systemd user unit (`Restart=always`, no start limit,
  best-effort lingering) on Linux. The unit captures the installing shell's
  `PATH`, `HOME`, and `BROKK_TOWN_MANAGED=1`, and is rewritten whenever its
  rendered content changes. Registration failure, `bt service off`, and demo
  mode fall back to a detached spawn with owner-only log files under the state
  directory. Temporary (`go run`/`go test`) binaries are refused for both
  registration and detached spawn because they disappear; they use `bt serve`
  in the foreground. `launch.json` also
  remembers the last explicit `--listen` per town (real and demo) so a later
  command's default cannot re-register the job onto a different port; `bt
  service stop` unloads a supervised job even when no connection file exists,
  so a crash-looping job can always be halted.
- The service advertises version, executable, start time, and managed mode in
  `connection.json` and `version` in public state. A newer release client, or
  the same binary rebuilt since the service started, restarts the service:
  `/api/restart` re-executes the binary at its own path (same PID, so
  supervisors and the npm launcher see one job); a different path re-registers
  or respawns. An older client never downgrades. The access key persists in
  `token` so browser bookmarks, tabs, and a polling TUI survive restarts; the
  page reloads itself when the served version changes.
- In-app upgrades install through the original channel (npm for the package
  layout, otherwise the checksum-verified release archive swapped over the
  executable) and then restart in place. `bt serve` remains the foreground
  mode and names the process holding the state lock.
- Tests: fake supervisor runners assert launchctl/systemctl sequences and
  template escaping; fake managers and services cover start, fallback, drift
  rolling, and `bt service` verbs; the web tests cover restart responses,
  version exposure, and asset revalidation; smoke covers real on-demand start,
  crash recovery, and token stability for the demo town.

## Bots survive Town restarts (2026-09-15)

The user found that upgrading `bt` killed every in-flight bot and asked for a
state where the town keeps functioning while the service is replaced. Repo-bot
stays in-process by decision: it is a stateless reconcile that runs every poll
and gates town initialization, so an external package would add fragility
without preserving anything.

- Bot processes start detached (own session, output to a file) and a durable
  `WorkerRun` handle is committed on the worker before the run request. The
  handle carries executable identity, PID, socket, output, last observed event
  sequence, deadline, and the exact issue/PR revision.
- Cancellation carries a cause. Operator stop, retry, and deletion kill the bot
  after identity is proven over its socket; the dispatch deadline kills. Plain
  cancellation is service shutdown and detaches, keeping the handle.
- The supervisor adopts every handle before its first scheduling pass. Worker
  Protocol v1 gains the optional `detach` capability and `GET /v1/attach?after=N`
  with 404 for an idle worker. Detach-capable bots replay and complete normally;
  older bots are asked to shut down when they can and the attempt is recorded as
  an uncertain outcome. Deleted towns' orphans are ended.
- Restarts use the self-managed lifecycle above: `/api/restart` re-executes in
  place, and the new image adopts the detached bots. The systemd unit uses
  `KillMode=process` so a unit restart never kills them with the cgroup.
- Bot repositories still need the `detach` capability and attach endpoint; until
  they ship it, restarts preserve processes but not results. In-process agent
  sessions (issue repair and review certification) are still bound to the
  service lifetime.
- Validation: Go race/vet, browser tests, and Python fake workers covering
  detach, adopt, replay offset, idle-worker 404, stop kill, uncertain outcome,
  store reload, and supervisor adoption.

## Recoverable review outcomes (2026-09-14)

- Reviewer execution/evidence failures are distinct from completed negative
  reviews. Town retries reviewer failures up to five times, retains exact
  expected/returned revision diagnostics internally, and exposes the actionable
  failure after the budget is exhausted. An operator retry resets that budget.
- A completed changes-needed review routes Town-owned PRs to Issue Bot for repair.
  External PRs route to Town Hall for an explicit retry or decline decision, so
  no review outcome can leave work in an operator-inaccessible terminal state.

## Editable external contribution policy (2026-09-14)

- Town Settings exposes the persisted merge policy after onboarding, with clear
  external-PR ownership and eligibility language. Agent-profile and policy edits
  commit atomically through the authenticated settings API.
- New external issues and PRs persist at Town Hall until the Mayor explicitly
  admits or declines them. Feature Bot proposals use the same gate by default,
  controlled by a Town Settings checkbox. Decisions are durable, create committed
  events, and never modify GitHub themselves.
- Admitted issues dispatch individually through Issue Bot's `exact-issue` worker
  capability in pinned `@brokkai/issue-bot@0.5.2`, so pending or declined issues
  cannot be selected accidentally.

## In-app Town update notice (2026-09-14)

- The service checks npm asynchronously at startup and every six hours, never in
  an HTTP, input, or render loop. Stable semantic versions only are considered.
- Browser and TUI clients offer an explicit upgrade action when a newer Town
  release exists. The authenticated service installs that exact version with a
  bounded, cancellable npm subprocess and asks for a restart. Offline or malformed
  registry responses are non-fatal.

## Configurable source funnels (issue #34, 2026-09-14)

- Add source-neutral discovery, refresh, capability, lifecycle, and receipt
  contracts. Normalize stable `(funnel, provider, source item ID)` identity,
  provenance/revision/cursor, eligibility, source state, explicit priority, and
  typed incomplete/auth/rate-limit/partial/unsupported/uncertain outcomes.
- Persist normalized source tasks, per-funnel sync health/cursors, and lifecycle
  write intents separately from legacy GitHub PR/merge intents. Save before a
  provider mutation; reconcile lost responses without treating absence as retry
  authorization. Public projections omit credential references and values.
- Put GitHub issue selection behind an adapter with query, selected issue, and
  include/exclude label filters (#3 slice), plus configurable working/blocked
  label mappings and revision rereads. Retain existing GitHub PR/review/release
  authority paths during incremental migration.
- Prove provider differences with an injected-HTTP Slack channel adapter for
  paginated reads, reactions, bounded thread replies, read-only operation, and
  private call-time credential resolution. Demo/tests never contact live sources.
- Show normalized provenance, eligibility, external state, priority, revision,
  sync time, capabilities, and typed source health through the shared snapshot,
  browser inspector, TUI, and CLI status. Document trust and priority boundaries.
- Treat provider-native completion as a terminal inbound observation: Slack check
  reactions and closed GitHub issues remain auditable but ineligible, and appear
  in the Completed board column. Never infer done from an absent item in an
  incomplete source read.
- Validate focused fake adapters and restart/overlap/intent behavior, then run Go
  race/vet, frontend, and integration gates before ready PR delivery.

## Versioned bot worker protocol (2026-09-14)

The user requested live-upgradable external bots, accepted Unix sockets for the
initial transport, and authorized corresponding bot-repository changes. A later
self-signed HTTPS/mutual-TLS transport can be added without changing semantics.

- Town now removes bug-bot, feature-bot, issue-bot, release-bot, and review-bot
  Go dependencies. Primary roles launch their installed executables and speak
  Worker Protocol v1 over a private mode-0600 Unix-domain HTTP socket.
- Initialization exchanges protocol range, exact bot/service version, identity,
  and capabilities before work. Runs use strict JSON requests and contiguous
  newline-delimited progress/result/terminal events. Issue and review results are
  explicit public protocol payloads; Town never parses bot-private state.
- Town records and rechecks the resolved executable path, hash, and version.
  PATH is resolved anew for each dispatch, allowing replacement in an existing
  service-PATH directory without a Town restart; mid-dispatch replacement fails
  uncertainly. Worker subprocess output remains bounded and shutdown is graceful
  with a process-group cancellation fallback.
- Coordinated commits in all five bot repositories add an `internal/worker`
  standard-library HTTP service and `<command> worker --socket PATH`. They retain
  their existing CLIs and adapt their released Run/state APIs behind the public
  worker boundary. The user subsequently requested releases. Final exact-tag
  releases and all 25 npm launcher/platform packages were verified:
  bug-bot `v0.3.1`, feature-bot `v0.1.1`, issue-bot `v0.5.1`, release-bot
  `v0.5.1`, and review-bot `v0.2.1`. A public npm installation smoke verified
  all five launchers and `worker --help`.
- Validation passed: Town `go test -race ./...`, `go vet ./...`, `make check`,
  and isolated `make smoke`; full race/vet suites for bug-bot, feature-bot,
  issue-bot, and review-bot; race/vet for release-bot's changed worker/cmd
  packages; all bot license, Python, and npm launcher tests; and a real
  cross-process initialize/shutdown smoke against all five locally built worker
  binaries. Release-bot's full root-package race suite initially exposed two
  pre-existing macOS `/var` versus `/private/var` failures; canonical checkout and
  repository paths fixed them, and its complete suite now passes. GitHub twice
  returned an `untagged-*` alias for release-bot drafts while the exact tag was
  propagating; both published records were repaired to their exact tags, package
  jobs completed, and release-bot `4a45936` now detects and repairs that alias
  before staging assets.

## Town v0.1.0 authorization and provenance (2026-09-14)

The user explicitly requested a new Town release using the external-worker
approach. Town v0.1.0 was published without weakening destination checks:

- `Publish packages` now publishes only by explicit `publish=true` dispatch from
  an exact existing tag. A uniquely named non-final `v*` preflight tag supplies
  the tag-only `packages-publish` environment while the workflow input remains
  the proposed stable `v0.1.0`; no deployment-policy broadening is needed.
- Authorization now validates GitHub contents access and all five npm package
  OIDC exchanges, then exercises the same job's
  Sigstore authority with npm's bundled client. One clearly identified non-package
  DSSE statement obtains a Fulcio certificate and Rekor entry; independent TUF
  verification checks the chain, SCT, signature, inclusion proof, workflow
  identity, repository, ref and exact commit.
- The second preflight proved all builds and version checks but exposed an invalid
  assumption in the authorization code: npm OIDC exchange tokens cannot read the
  maintainer-only trust-governance endpoint and correctly receive HTTP 401. The
  check now records package-scoped exchange evidence without claiming it proves
  direct-versus-staged permission; npm enforces that distinction on publication.
- Final npm publication explicitly requests provenance. Read-only publication
  verification requires one SLSA v1 Fulcio/Rekor bundle for every one of the five
  packages and independently checks its package PURL, tarball SHA-512, workflow,
  tag ref and source commit. Missing or untrusted provenance fails; registry
  signature/integrity checks remain in place.
- Added regression coverage for Actions-only Sigstore execution, safe handling of
  helper failures/evidence, publish order/flags, provenance metadata, request
  construction and final fail-closed verification. The independent verifier was
  also exercised against a real published Sigstore attestation and TUF root.
- PR #35 merged the corrected npm authorization preflight at merge commit
  `8006469e2b861513f469c4b481b551cc22ce1594`. Exact-commit preflight run
  `34837196002` passed before the immutable `v0.1.0` tag was created. Publication
  run `34837658974` then completed; the finalized GitHub release, four native
  archives, launcher, four platform packages, registry bytes, and all five
  independent SLSA/Fulcio/Rekor provenance bundles were verified publicly.

## Self-contained worker startup patch (2026-09-14)

- The v0.1.0 package installed Town itself but real workers expected separately
  installed `bbb`, `bfb`, `bib`, `brv`, and `brb` executables. The user rejected
  that surprising prerequisite and selected pinned npx execution.
- Default dispatch now always invokes the exact compatible bot package through
  `npx --yes`; it never selects an ambient or floating bot version. Explicit
  command overrides remain internal test seams for fake workers.
- Regression coverage asserts the exact package/version mapping for all five
  roles. Release validation must include a fresh public Town-only installation
  that starts each pinned worker service, not merely `bt --help`.

## Epic #30: simplified operations and scheduling capacity (2026-09-14)

- Long Board queues use viewport-relative bounded scroll areas with persistent
  column headings, keyboard access, and scroll positions retained on live redraw.
- Board scrollbars use recessed dark-green tracks and sage thumbs with hover
  and active states, retaining native scrolling and system high-contrast colors.
- User-requested name: Board and Compact display **"No Fun Dave" mode**, with
  matching selector tooltips; the standard view names and shortcuts remain.
- Isolated branch `codex/epic-30-operations-capacity`, based on fetched
  `origin/master` at `660a27d`; canonical checkout remains untouched.
- One persisted global service setting, default 4 and validated 1–64, controls
  reserved non-reporter bot runs across towns. Reductions preserve active work;
  increases and freed slots wake the existing scheduler. Reporter, durable issue
  publishing and prompt-free model discovery remain outside this pool.
- Town, Board and Compact consume the existing snapshot/SSE and command API.
  Task stages and typed write/review evidence remain authoritative; projection
  cannot certify GitHub outcomes. Active worker profiles are frozen at dispatch.
- Luna ownership: browser assets/tests; CLI/TUI, demo and docs/smoke. Primary owns
  scheduler, state/API contracts, integration and final verification.
- Usage, spending limits and quota routing remain #6; Mjolnir stays an independent
  ACP control plane, with no executor dependency or second scheduler introduced.
- Implementation integrated and `make check smoke` passed: Go race/vet,
  20 frontend behavior tests (including actual app handlers), syntax, licenses,
  launcher and 33 packaging regressions, plus isolated CLI/API/TUI smoke.
- Browser QA passed view switching, task/worker inspection, capacity saving,
  persisted selection, keyboard tabs and 320/390px layouts. Fixed hidden
  inspectors, task lookup, mobile header overlap and schedule text placement.
- Validation uses bundled Python 3.12. Xcode Python 3.9 fails the unchanged mock
  HTTPError.close test, reproduced from exact base 660a27d. Bifrost correctness
  policy execution panicked after 17.166s with `one semantic temporary has one
  transparent assignment source`; no policy cleanliness established.
- Clean commit 07fe207 passed native packaging for all four supported targets,
  all five npm packages, and offline npm launch. Ready PR #31 links this work to
  epic #30; current-head Linux/macOS/workflow CI delivery is tracked there.
  No live agents, test GitHub writes, merge or release publication were used.

## Per-bot agent profiles (2026-09-10)

User requested independent harness/model/reasoning choices for every bot, such as
Claude for review, Codex for issue implementation, and an OpenRouter-backed agent
for releases.

- Preserved town-wide agent settings as defaults; added complete private profiles
  for bug, feature, issue, review, and release bots, with explicit inheritance
  reset. Repo-bot performs repository reporting without an agent.
- Persisted per-profile harness versions, commands, authentication, environment,
  model and effort. The selected bot resolves at dispatch; active runs retain
  their settings, and nested repair/review sessions reuse the prepared launch.
  Public state exposes only harness/version/model/effort and inheritance.
- Added browser profile selection and bot-inspector access, authenticated API and
  CLI role selection/reset, documentation and a multi-profile config example.
- Validation passed: `make check` (Go race tests/vet, 15 frontend behavior tests,
  syntax, launcher, packaging and license checks) and isolated `make smoke` with
  CLI/API profile isolation, defaults/reset and restart persistence. Fake agents
  cover private config preservation, dispatch/nested sessions, and model choices.
- In-app browser demo QA verified Claude review, Codex issue and OpenCode release
  profiles, saved versions, offline choices, reset isolation and reload/inspector
  display, with no browser errors. Unsaved drafts survive switching profiles;
  pending inheritance resets must be saved before further customization so old
  private commands/authentication cannot survive a reset followed by an edit.
- Changes stay local on the current branch. No live service restart, real bot
  automation, GitHub writes or release publication were used for development.

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
setup adds the bot-style native/npm packaging and release workflows. No GitHub release
tag was created. The user subsequently authorized manual npm bootstrap publication. The browser assets were inspected, and frontend contracts were
tested; browser interaction/visual QA was not performed in this environment.
Large repositories may need incremental inventories and task archival. Merge
queues and non-squash merge strategies currently require manual merging. Real
agent judgment and repository-specific publishing policy need an operator-chosen
live town before they can be exercised end to end.

## Durable issue retry follow-up

- Explicit retries of `issue:N` now reset that repository's pinned issue-bot
  state through its released, issue-scoped Retry API when the selected durable
  job is blocked or pending. Funnel-only failures and completed or absent jobs
  require only the Town reset. PR review, repair, merge-intent, and saved-commit
  recovery retain their separate behavior.
- Town validates durable state before interrupting an active issue worker. A
  verified reset drains that worker while scheduling is held until issue-bot
  releases its state and checkout locks. Saved work and uncertain claim/publication
  fields are preserved, and validation or reset failures leave Town's blocked task
  counters intact and return an error to the caller.
- Regression coverage seeds two exhausted durable jobs, retries one through the
  real supervisor control path during an active fake worker, verifies only the
  selected job becomes eligible and resumes, and checks failure atomicity.

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

The user then explicitly requested manual npm publication. Published all five
0.1.0-rc.1 packages under next from cfa1a68754e565b61894bb5de33558ee05b169f7,
following browser authentication. Configured actual npm trusted publishers for
all five names and read every setting back: BrokkAi/brokk-town,
publish-packages.yml, packages-publish, with the same publishing permissions as
review-bot. All five are public, effective collaborators match mjolnir, and the
brokkai:developers team has read-write access. Published package integrity matches
the validated local artifacts. Audit details: /tmp/brokk-town-npm-audit.json.
No GitHub tag or stable release was created.

The remaining macOS smoke difference was precisely the transient PENDIN flag,
which XNU sets when ICANON is restored. The smoke comparison accounts for that
kernel-maintained state on macOS while checking all configured flags, control
characters, and speeds. The application itself correctly restored its settings.

## v0.1.0 publishability preflight (2026-09-10)

- Preserved the npm-only 0.1.0-rc.1 bootstrap and all local history. The initial
  release target 692830d already has successful exact-commit Linux/macOS CI;
  no tags, GitHub releases or prior preparation PR existed at inspection.
- Prepare on brb/release-ilamovjxd3ttri6z3cdpvpheqk and deliver through its PR to
  master. No tag pushes, public releases, final assets or npm uploads are
  authorized in this phase. Branch pushes run CI only.
- Enumerated native GitHub assets, source/Go tag, four npm platform packages,
  the npm launcher, npm/GitHub latest pointers, and npm's Sigstore provenance.
  No other registry, container or hosted documentation targets were found.
- Added a non-publishing exact-version build/verification path, actual Actions
  publisher checks, strict remote run evidence checks and regression coverage.
  Release orchestration validates all versions before uploads, retains partial
  state and finalizes GitHub after npm verification. Independent archive checks
  compare unpacked bytes and permissions after validating published checksums.
- Local validation passed: make check smoke (race, vet, frontend and demo
  integration), Python regression tests and actionlint. Committed candidate packaging
  and exact-SHA Actions validation follow the preparation commit/PR.
- Blocked: packages-publish permits only v* tags and there are no tags; preserve
  that policy until an administrator explicitly authorizes a reviewed preflight
  branch. The local npm trust read returned 401. All five actual OIDC trust
  configurations and createPackage permissions need same-job validation.
- Remaining signing gate: establish non-publishing Fulcio/Rekor authorization
  and independent provenance verification. The release authorization command
  fails closed on this missing evidence; no signing request or log entry was
  submitted. RELEASING.md records these limitations and the recovery commands.
- Exact-version local packaging exposed npm 12's package-name-keyed JSON output;
  accept both npm 11's single-record array and npm 12's keyed object while
  rejecting wrong names, versions, extra records and unsafe filenames.
- Packaging and offline install now isolate npm configuration and cache from
  the developer account; a user-level allow-scripts setting otherwise causes
  npm 12 to reject a project-scoped install even with --ignore-scripts.

## Town management and scenery follow-up

User requested town deletion, harness/model/effort selection, feature and bug
requests that create issues, better working animation, and a landscaped field.

- Implemented deletion through the shared command API, browser confirmation, CLI,
  and TUI confirmation. Workers are canceled; local recovery tombstones retain
  ownership, worktrees, and uncertain writes. Re-add waits for active cleanup and
  restores the town with automation paused.
- Added per-town Codex/Claude/Gemini/custom ACP settings and private-command
  preservation. Available model and model-specific effort choices come from a
  prompt-free ACP discovery session; demo discovery uses fixtures.
- Added durable feature/bug submissions, authenticated API and CLI commands, a
  browser form and receipt history, and a supervised GitHub issue publisher.
  Uncertain POSTs only reconcile by a saved marker; they never blindly repost.
  Demo requests are local, and submitting work does not enable paused workers.
- Preserved original sprite atlases. Added cached procedural grass, dirt roads,
  trees, rocks, flowers, and fences; larger actors, worker patrols, house effects,
  eased delivery movement, shadows/dust, and reduced-motion handling.
- Validation complete: `make check`, Go race tests/vet, frontend syntax and eight
  behavior tests pass. Expanded `make smoke` covers CLI settings, literal issue
  bodies, same-ID retry, demo choices, TUI deletion/cancel, and restart recovery.
  Fake-agent tests cover model-specific effort discovery without prompts and
  settings at dispatch; fake GitHub tests cover lost responses and reconciliation.
  Chrome desktop QA verified the landscaped scene, saved harness/model/effort,
  a demo bug receipt in the workshop, and deletion of the neighboring town.
  No live GitHub writes, real bot work, or release publication were used.

## Official ACP registry and additional harnesses

User requested the official ACP registry plus BrokkAi/anvil, BrokkAi/muse-acp,
and foundev/draupnir instead of a fixed three-agent selector.

- Added the complete official registry v1 catalog (40 bundled entries), validated
  refreshes, an atomic persistent cache, and an offline/demo fallback. Browser
  settings group registry agents and the three requested supplements, display
  setup/project/version details, and refresh stale catalogs asynchronously.
- Registry selections persist the exact launch definition privately with each
  town. Catalog refreshes leave saved versions and active runs alone; explicit
  version selection upgrades a town. Public snapshots expose ID/version without
  commands or environment. Codex/Claude aliases and custom commands still work.
- Launch preparation handles registry npm/uvx recipes and native zip, tar.gz,
  tar.bz2, and raw executables. Private native installations use cross-process
  locks, bounded downloads/extraction, optional checksums, safe paths/links, and
  atomic publication. Anvil/Muse ACP/Draupnir resolve their installed commands.
  Model discovery retains prompt-free ACP selectors and cancellation, with a
  separate preparation timeout. Nested bot sessions reuse their run's launch.
- Added `bt harnesses [--refresh]` and `--harness-version`, with README setup
  instructions, architecture notes, and Apache-2.0 registry snapshot attribution.
- Validation: `make check` and `make smoke` pass, including Go race tests/vet,
  frontend syntax/behavior, existing packaging checks, and isolated CLI/TUI/API
  integration. Fake downloads cover concurrency/cache reuse, zip/gzip/bzip/raw
  formats, checksums, cancellation, archive traversal/links, and partial-install
  rejection. Tests cover catalog refresh/failure/restart, version preservation,
  explicit upgrades, private metadata, and all three supplemental settings.
- Chrome demo QA verified all 40 registry entries and three supplements, setup
  links, native distribution details, model-choice completion, saved Anvil model
  and effort, persistence after restart, and the compact dialog layout. Demo
  refresh/discovery do not run agents or access the network. No live bot work,
  GitHub writes, or publication was used. Actual provider authentication remains
  the user's harness setup; the tests use fake agents and downloaded fixtures.

## Feature-bot discovery and distribution (2026-09-10)

User requested a separate feature-bot closely modeled on the latest bug-bot,
including native/npm packaging, matching GitHub Actions, and a studious graphic.
Pulled master in both Town and bug-bot before beginning. Standalone source is committed and published from the separate sibling
/Users/ryansvihla/code/feature-bot checkout.

- Adapt bug-bot's discovery, independent review, full issue-history comparison,
  exact revision checks and durable publication intents for useful new features.
  Require user problem/value, scope, acceptance criteria and repository evidence;
  reject defects, existing/rejected requests, unsupported or uncertain proposals.
- Preserve the standalone Go/ACP library, bfb CLI/TUI, installer, four native
  targets, five @brokkai/feature-bot npm packages and three pinned Actions workflows.
- Integrate the feature role into Town's shared controls, supervision, 30-minute
  cadence, receipt-driven issue deliveries and isolated demo. Migrate existing
  state by adding only an absent feature worker, paused by default.
- Add original studious study/reader artwork and a seven-building browser layout.
- Validate with fake GitHub/ACP, race/vet, frontend checks and demo integration;
  validate complete standalone packaging/offline install before external setup.
- User explicitly authorized the public repository and initial prerelease, then
  clarified the established bootstrap flow: npm publish, user follows its approval
  link and enables the five-minute window, then CLI scripts configure trusted
  publishers and matching owners. Use that flow for subsequent npm bootstraps;
  do not drive account setup through browser automation.
- Created public BrokkAi/feature-bot with bug-bot's effective GitHub collaborators,
  workflow permissions and packages-publish environment. Published v0.1.0-rc.1
  at df984949176eb3865414897b9a2ff7de954a8ce2, with six native GitHub assets and
  five npm packages under next. Every published artifact matches the validated
  local bytes. All five trusted publishers were configured by npm CLI and read
  back, with createPackage/createStagedPackage permissions matching bug-bot;
  effective npm owners are foundev and bigslopdave on every package.
- Town pins the published v0.1.0-rc.1 library without local replacements. Updated
  reviewed dependency notices. Full make check smoke passes (Go race/vet,
  frontend and launcher tests, package/license regressions, isolated CLI/API/TUI
  integration). Independent review found and fixed new-CLI/old-service TUI
  compatibility; missing feature workers display restart guidance.
- Browser QA passed at 1440, 390 and 320 pixels with controls, keyboard 7,
  reduced motion, PNG serving and no errors/overflow. Both original studious
  RGBA sprites preserve their generated alpha; prompts and provenance are saved.
- Final public npm install launched bfb v0.1.0-rc.1. Exact-source Linux/macOS CI
  passed on both master and the release tag. The existing-release package
  validation workflow 34498883724 passed, rebuilt identical npm packages from
  the published native assets, verified registry integrity, and saved its
  validated package artifact. The redundant tag-triggered publisher was canceled
  after its checks passed because the bootstrap release was already published.
- Final standalone source is df98494. Published artifacts and the five-package
  owner/trust audit are retained in feature-bot/dist (ignored). Town integration
  is committed on its current master branch; no Town release was published and
  the user's existing live service was not restarted or enabled by this work.

## v0.1.2 release preparation (2026-09-14)

- Reconcile the job target and its recovered release-verification changes with
  the already-published v0.1.1 baseline without moving existing tags.
- Preserve upstream npm/Sigstore provenance validation while hardening native
  archive recovery, npm dist-tag verification, and exact-run build evidence.
- Run the complete local checks, deliver the preparation through the job's
  unique PR, and validate the merged commit with a `publish=false` workflow run.
- Do not create the final v0.1.2 tag, upload assets, publish npm packages, or
  dispatch the publishing path during this preflight phase.

## Review attempt recovery (2026-09-14)

- Treat incomplete or revision-mismatched reviewer output as a failed attempt,
  never as a clean or negative review, and retain the exact returned evidence in
  the terminal error for diagnosis.
- Retry reviewer failures up to five times without exposing noisy intermediate
  evidence or marking the whole worker failed; retain explicit operator recovery.
- Route completed negative reviews of external PRs back to the Mayor for an
  explicit admit/decline decision while owned PR feedback continues to Issue Bot.
- Validate with fake workers and GitHub only; no live repository automation.

## Explicit bot version upgrades (2026-09-14)

- Publish the effective exact bot pins in each town's safe public configuration.
- Show the selected bot's current pin in Settings and check npm's stable tag only
  when the Mayor asks; never float or silently mutate a running configuration.
- Save an explicitly selected semantic version per town and bot, then retain all
  worker protocol, capability, reported-version, and exact-run checks.

## Release recovery through the worker API (2026-09-15)

- The v0.1.2 publish run failed only because `release_checks.published` ended
  with a placeholder `raise` after every destination and provenance check had
  passed; certify instead of raising, with a unit test.
- Add `POST /v1/retry` to the worker protocol so an operator lifts Release Bot's
  exhausted attempt budget with `bt retry --role release` or the control API;
  Town sends it only to a worker advertising `retry` and never edits bot state.
- Release Bot itself routes a repeated identical verification failure to its
  preparation agent, so script or workflow bugs get repaired without an operator.
- Bump the Release Bot pin once a release advertising `retry` is published.

## Offered bot upgrades (2026-09-15)

- Check npm's stable bot tags from the supervisor at start and every six hours;
  a registry failure is quiet and never changes a pin or stops a town.
- Represent a newer stable release as an `upgrade:<role>` task in Town Hall so
  it shares the Mayoral decision surfaces, counts and CLI with outside work.
- Approve pins the exact version for the bot's next run; delay asks again after
  a day; decline keeps the pin until a newer release appears. Nothing floats.
- Add a per-town `auto_update_bots` setting, off by default, that pins new
  stable releases as they appear and applies any offer already waiting.
- Withdraw an offer when an explicit pin or the registry catches up with it.

## Master conflict recovery (2026-09-15)

- Resolve the interrupted pull/rebase onto `1886354`: local commits `4aa63ee`
  (Mayoral intake) and `54190d3` (review recovery) already landed through PR #43
  as `4c23680` and `5188b98`, with upstream compatibility fixes. Skip both
  duplicate replays, preserving the upstream implementation and original local
  history in `backup/master-before-conflict-recovery-20260915`.
- Audit remaining work: bot control gating, source/version UX, cross-town inbox,
  bot upgrade decisions, and repair-push confirmation are on pushed branches
  outside master. Service/bot handoff and shared agent defaults/onboarding also
  have uncommitted changes in separate worktrees; leave that work intact.
- `make check smoke` passed: Go race/vet, frontend syntax and tests,
  packaging/license checks, and isolated demo CLI/API/TUI integration.
  Commit this recovery record and push master; no release or live bot
  automation is part of this recovery.
