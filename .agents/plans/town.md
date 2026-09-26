# Brokk Town implementation plan

## Finish Mjolnir issues, review/merge PRs, then release changed components

- User requested monitoring https://github.com/BrokkAi/mjolnir/issues/1166 and
  automatic resumption once fixed. Upstream PR #1167 merged as c107232 after
  green CI (head 07e76c4; 29 passed, one intentional skip), closing #1166.
  Resume implementation and fake-service checks under the existing authorization.
  Verified the fixed worker in a fresh container using digest-checked artifacts
  from v2.23.1 release workflow 36254212898. Do not duplicate upstream release work.
- User withdrew the suggestion to cut an early release. Finish all remaining
  issues and reviewed, green PRs first, then release each changed component.
  During upstream monitoring the first CI runs were explicitly cancelled by the
  foundev account. Two later macOS fixture failures were fixed upstream
  (canonical cache paths and portable `false` lookup); the final CI passed.
- PRs #175, #176 and #177 are merged after review fixes and green CI; latest
  merged base is d2f3f07. No release has been published. #149/#153–155 remain
  open pending integrated acceptance. Current branch is
  `codex/mjolnir-worker-dispatch` with PR #178 containing the ACP executor,
  Review Bot remote-agent-v1/dry-run protocol and Town review/repair routing.
  The integration now passes root and Review Bot race tests/vet, 99 frontend
  tests/syntax, both modules' Python release and npm launcher tests, build,
  demo/fake-Mjolnir smoke, and all eight offline npm worker lifecycle checks.
  New fixtures cover ACP intent ordering, uncertain creation/settings/evidence,
  cancellation, strict callback identities, old worker capabilities, frozen
  configuration, independent remote finding verification and dry-run writes.
  Next: finish the separate real-container dry-run against that PR, fix findings,
  merge green, close acceptance issues, then release Review Bot and Town.
- Integration PR #178 is open at 92ff9de. The exact-head COMMENT review found
  successful worker dry-runs misleadingly returned `stale`; return `dry_run`
  while keeping Complete false and isolating live requests and changed revisions.
  Regression and full Review Bot race/vet checks pass. Added an opt-in
  `mjolnir_acceptance` harness (compiled/vetted, default skips) for an existing
  real PR; it only dispatches dry-run review and checks guarded evidence, CLI
  session/index visibility and confirmed cleanup. Do not count its compiled/
  skipped run as acceptance.
- Upstream blocker is verified fixed on a fresh container: downloaded CLI and
  worker artifacts from release run 36254212898 at 07e76c4, verified their GitHub
  SHA-256 digests, and restarted only the isolated acceptance daemon as 2.23.1.
  Prompt-free session 0afdc5c5493216994a5b93ec4f9eb62e on acceptance-known now
  has target_installation identity mj-runtime-v1:4622601d622949c3ce7b37515d9520ba05ff10340fc0064ce3727627d5462538,
  bridge 1.13.3 and provider 0.156.1. Next is the real PR #178 dry-run through
  the separately gated worker/ACP acceptance harness. The dry-run status fix
  is committed as 9e3d147; no Town or bot release has been published.
- First real dry-run at 6aa138a reached the exact checkout and pinned runtime,
  but Mjolnir refused the prompt before starting any agent turn: its API limits
  prompts to 65,536 characters. Session 55d44032c0a5046ad7ca5b587cdb6eb5 and run
  run-93a059eca1f43656ce6548df7c1192be remain retained under proof-pr178. Adapter
  log confirms HTTP 400; transcript contains only the harness-started event.
  Fix Town/Review Bot to refuse oversized prompts before creating a session and
  include complete metadata with an exact-checkout diff command instead of a
  duplicate inline diff. No truncation, GitHub writes or prompt replay occurred.
- Prompt fix d97425a passes full Town/Review Bot race suites and vet. Fresh
  dry-run run-347b2b93115e842af13a9bd7dccfc9d2/session
  2172a82c527cf0fc1db89833a27a40c0 is now running against that exact PR head.
  Applied the same complete-diff command to Town's independent certification
  prompt; its regression and full Town package race/vet checks pass. Keep the
  PR head fixed until the current read-only acceptance finishes.
- The d97425a dry-run completed its agent turn but refused immediate evidence;
  retain its run/session rather than replay it. Subsequent reads through Town's
  actual decoders pass identity/runtime/tree/transcript checks, and the turn wait
  receipt matches the final transcript answer exactly. The original callback
  discarded its detailed cause, so snapshot lag is a supported hypothesis, not a
  proven attribution. Its review independently reproduced two Town bugs: the
  certification prompt limit (fixed in 9c6f475) and stale GET configuration after
  successful PATCH. Add bounded read-only configuration/idle confirmation,
  preserve confirmed ACP answers before collection, and return the parent's
  actionable failure to Town and the acceptance harness. No mutation is retried.
- Upstream v2.23.1 is now published; its checksum-verified official Linux archive
  contains byte-identical CLI and worker binaries to those used for acceptance.
- Isolated acceptance data and two idle, prompt-free discovery sessions are
  retained under ignored `var/mjolnir-acceptance`. The released v2.23.0 binary
  was checksum-verified. Both cached and explicitly installed Docker runtimes
  report unknown identity because the worker inspects the `sh` launcher instead
  of the Codex package. Never bypass that refusal. An isolated upstream clone
  exists at `var/mjolnir-runtime-fix` on `codex/container-runtime-identity`; no
  upstream source fix, PR, merge or release has been made.

- User explicitly authorized one PR per remaining issue, review and fixes,
  normal merge after passing checks, and releases for each changed bot and Town
  after the whole sequence. Use available test infrastructure without asking
  the user to choose target/profile details. Docker is available; installed
  Mjolnir 2.23.0 now contains the new contracts; downloaded and checksum-verified
  its released Linux binary for an isolated Docker acceptance. Keep it apart from live
  repository automation and preserve all existing worker APIs/capabilities.
- PR #175 covers #153: guarded launch receipts plus explicit runtime selection
  through shared browser/CLI API. Save target-owned versions by target/profile,
  preserve old pins on failed discovery, freeze dispatched and recovery copies,
  and never start a local harness for managed discovery. No silent upgrades.
- Reviewed #175 at bdb34e1, fixed runtime-save timeout/late-response handling,
  passed all root/frontend/local checks and the nine-module CI plus worker smoke,
  then merged normally as 2440dfe. Runtime dispatch acceptance remains pending
  the later PRs, so #153 stays open until the integrated path is validated.
- #154 now has a durable Mjolnir run lifecycle. Resolve an unambiguous configured
  single-repository bundle; reuse one stable workspace; save exact private-branch
  and runtime intent before HTTP or ACP creation. Never retry a lost create
  receipt. Confirm readiness and unchanged exact-base diff before returning a
  usable session. Store complete private evidence before cleanup and confirm
  destruction with a session 404; lost cleanup is reconciled by reads only.
  Regression caught shared runtime-component pointers in the launch plan; decode
  into a fresh value to freeze all nested configuration. Demo remains offline.
- Opened PR #176. Review reproduced cleanup accepting a changed runtime ID when
  the event ordinal stayed the same. Revalidate the full launch receipt before
  destruction; keep the session if checkout, runtime guard or readiness changed.
  Full root race/vet, frontend syntax and 99 tests, build and demo/fake-Mjolnir
  smoke passed before review; repeat root checks after this fix.
- PR #176 passed all CI and merged normally at b72f499 after the exact-head
  review. PR #177 contains the artifact/evidence layer. Its review found missing
  transcript positions defaulting to zero and ambiguous duplicate positions;
  both now refuse evidence rather than choosing a potentially wrong final answer.
- Next PR #154: reusable bundle/workspace mapping, exact checkout, durable
  creation/cleanup intents and uncertain-outcome recovery. Then #155: remote
  evidence and verified bundle import through independent worker boundaries.
  Finally #149: scheduler/ACP integration, local fixtures and isolated real-target
  acceptance. Keep incomplete issues open until their acceptance is met.
- #155 implementation: bind each finished answer to a complete stable private
  transcript, unchanged review tree or committed descendant repair, and the
  original runtime initialization. Refuse missing artifacts and resumed workers.
  Save an export intent before checkpointing, never retry an uncertain export,
  retain bundle/diff/transcript with digests, and recheck artifacts after export.
  Import only the advertised exact repair commit into a clean private local
  branch, verify history/nonempty diff, run operator checks there and require the
  same clean commit afterwards. Existing semantic review and GitHub gates remain
  required; dispatch wiring is the following #149 PR.
  Root race/vet, frontend syntax/99 tests, build and both local smoke checks pass.
- Review each exact PR head, fix findings, pass required checks and merge without
  bypass. Release only changed components, bots before Town where needed, using
  immutable project tags and Actions publishers; verify published assets/npm.

## Mjolnir launch receipts (#153/#154, first part implemented)

- User asked whether the four remaining issues can be started or are blocked.
  Rechecked GitHub on September 26: upstream #1162/#1163 are closed, implemented
  by merged Mjolnir PRs #1164/#1165. Audit their public contract at adf1304.
  The latest published upstream release, v2.22.0, predates those changes;
  development is unblocked while deployment needs a release containing them.
- Added Town's consumption of exact-checkout and runtime receipts. Preserve
  the target's opaque identity and component versions, provenance, event ordinal
  and observation time. Keep unknown provenance explicit and private diagnostics
  out of projections. Missing additive fields remain readable for older daemons,
  but cannot satisfy launch readiness.
- Require a matching checkout declaration and a live idle session without errors.
  Match the selected runtime against both the actual receipt and Mjolnir's saved
  expected-identity constraint, which guards prompt admission and replacement.
  A lookup alone is not a runtime pin or proof of unchanged HEAD; current diff
  evidence and repository mapping remain necessary before dispatch.
- Added fake-daemon coverage for preparation failures, replacement races, receipt
  retention, missing/invalid fields, partial responses, cancellation and demo
  isolation. Full root race tests/vet, frontend syntax/tests, Town build, demo
  and fake-Mjolnir smoke all pass. Keep build scratch directories hidden so Go
  package discovery does not include concurrently generated cgo files.
- The supplied checkout had detached HEAD; keep this coherent change on the
  local codex/mjolnir-launch-receipts branch without changing master.
- Keep #149/#153–155 open. Durable launch intents, runtime selection controls,
  worker protocol/ACP integration, complete remote review/repair and confirmed
  retention/cleanup remain follow-up work. No live automation or release.

## Mjolnir artifact evidence (#155, first part implemented)

- User asked to start the remaining repository issues after repairing the PR and
  release. The only open Town issues are #149 and #153–155. Upstream Mjolnir
  #1162/#1163 remain open; implement the supported artifact boundary now without
  claiming full managed dispatch, runtime pinning or real-target acceptance.
- Work on feat/mjolnir-artifact-evidence from merged master, separate from the
  immutable 0.7.2 source. Audit the supported HTTP contract at Mjolnir 4d6c0ce.
- Add bounded session identity, exact-base diff, repository-relative file,
  transcript and Git-bundle reads. Share the existing authenticated transport;
  preserve demo isolation, no redirects/proxies, cancellation and safe errors.
- Require exact review checkout and affirmative nonempty repair ancestry checks,
  while keeping complete review receipts and verified bundle import as separate
  requirements. Expose no branch-push export. An interrupted checkpoint/export
  remains unconfirmed and is never automatically retried.
- Preserve equal-sequence transcript groups beyond the requested page size;
  the upstream database deliberately returns whole groups. Reject skipped or
  missing evidence and keep private patch/text out of JSON projections.
- Fake HTTP fixtures cover refusals, partial bodies, limits, identity mismatch,
  unsupported ancestry, path escaping, cancellation and paging. A disposable-Git
  fixture imports a returned bundle and verifies its exact repair head/ancestry.
- Full root race/vet, frontend syntax/tests, demo isolation and fake-Mjolnir
  integration passed. Final deadline and byte-preservation cases also pass with
  the complete artifact race suite. PR #174 contains this first part. Keep
  #149/#153–155 open: durable remote launch/artifact receipts, worker integration
  and complete review/repair dispatch remain follow-up work.

## Repair PR #172 and publish Town 0.7.2 (complete)

- User requested investigation and cleanup of the broken PR and recent branch,
  then explicitly authorized closing superseded work and cutting a new release.
- Work on fix/pr172-ci-recovery from the exact PR head, preserving the original
  contributor branch. Retain its startup recovery and cancellation diagnostics.
- Reproduced the CI failures with TZ=UTC. Compare expected recovery evidence
  after the fixture's JSON round trip, so assertions retain every persisted
  field without requiring Go's unpersisted time-zone identity. Validate UTC and
  Europe/Paris, including the real worker protocol fixture with fake GitHub.
- The recent codex/npx-macos-release-check push contains a plan-only completion
  record; its code already merged in #167/#170. Integrate that record without
  restoring its older source tree or removing the later Guide/history work.
- Root race tests in UTC, vet, frontend syntax/tests, packaging/launcher and
  license checks passed. Recovery and real-worker fake-GitHub fixtures also pass
  in Europe/Paris. Local demo, all-eight offline npm worker lifecycle and
  fake-Mjolnir integrations passed; no live repository automation was used.
- Replacement PR #173 passed all nine modules and workflow lint, then merged
  normally as 5d0d1ce. GitHub also marked #172 merged through that ancestry.
  The failed 0.7.1 workflow was canceled and its empty unpublished draft removed;
  its immutable tag and the original contributor branches remain unchanged.
- Complete non-publishing 0.7.2 packaging passed at b85b9f1: four native
  archives, five npm packages and isolated native/npm version checks. After
  confirming that exact commit was merged and its tree matched master, pushed
  immutable v0.7.2-town. Actions run 36239901804 passed all Linux/macOS checks
  and publication. The four downloaded native archives and all five npm packages
  pass integrity, legal-file, payload and exact-source checks; GitHub marks this
  the latest release. The published binary passes isolated demo lifecycle smoke.
- npm initially served incomplete native-package metadata. After propagation,
  all five latest versions agree and a fresh isolated npx latest installation
  reports v0.7.2. No bot code changed; #155 work is on a separate branch.

## Preserve worker cancellation diagnostics (complete)

- User identified the logging gap while reviewing the startup repair. Town
  discards `context.Cause` and the worker stream's canceled-event detail, then
  persists only "Worker attempt was canceled" without a cancellation log.
- Preserved known operator-stop and service-shutdown causes, worker-reported
  details and the last phase in bounded, credential-scrubbed logs and outcomes.
  Service and supervisor cleanup now propagate their failure as the context's
  cancellation cause. Unknown causes stay explicit; write holds remain intact.
- Protocol-detail, operator-stop, signal-cause, unknown-cause, worker-canceled,
  credential-redaction and supervisor-failure regressions failed before the
  implementation and now pass, including saved outcomes and logs after restart.
- Full root race tests/vet, frontend syntax/tests, npm launcher tests, Town build
  and isolated demo lifecycle smoke pass. The first full rerun exhausted /tmp;
  removed this task's unpublished packaging artifacts and moved its build cache
  into ignored workspace storage, then successfully reran the suite.
- No bot code changed. Historical cancellation causes cannot be reconstructed
  from old records. User subsequently authorized publication of both fixes.

## Town 0.7.1 release (failed before publication)

- PR #172 and immutable tag v0.7.1-town point to a9a9545. Both Ubuntu CI
  runs failed in the new saved-state recovery tests. The release job is gated
  by those checks. The empty unpublished draft was removed after the replacement
  merged.
- The local 4577b06 plan-only commit records the attempted tag push. Its queued
  status is superseded by the observed failures. Keep the failed tag unchanged.
- Source-built and published-worker fixtures had passed locally, but UTC exposes
  a test assertion comparing Go's time.Local against JSON-decoded time.UTC.
  No evidence indicates the recovery records or historical logs were changed.

## Retire the persisted default-branch startup failure (complete)

- Read-only inspection of local state confirms an uninitialized town following
  the default branch, with `invalid branch` saved on September 24, followed by
  canceled inventory attempts and an uncertain repair recovery record. No
  later successful inventory or new branch-validation failure is recorded.
- Previously startup preserved the obsolete error until inventory succeeded.
  Added a narrowly scoped, durable startup repair for that legacy empty-branch
  failure. Retry enabled inventory promptly and preserve pauses, logs, saved
  work and uncertainty. Initialization still requires actual inventory.
- Four enabled/paused and recovery/no-recovery cases failed before the fix.
  Tests now cover immediate persistence, repeated restarts, cancellation, new
  failures, and successful scheduler inventory with real Repo/Issue Bot protocol
  executables and fake GitHub. Other errors and invalid configuration stay visible.
- Replayed a temporary copy of the actual local state: the obsolete error was
  removed from memory and disk before workers started; another restart was
  stable. Configuration, tasks, ownership, intents, logs, pauses and recovery
  records were preserved. Verified the original file's hash was unchanged.
- Full root `go test -race ./...`, `go vet ./...`, frontend syntax/tests, Town
  build and isolated demo lifecycle smoke pass. Local socket tests used the
  approved sandbox escalation. No release or live automation was started.

## Publish independent bot releases and Town 0.7.0 (complete)

- User requested releases on 2026-09-26 for every bot changed since its last
  release, then Town using the new npx lifecycle. All eight bots have source
  changes, confirmed against their published standalone GitHub tag trees and
  npm stable versions. Preserve independent versions and never reuse a version
  already distributed inside a Town bundle.

| Project | Previous stable | Previously bundled | New release |
| --- | --- | --- | --- |
| bug-bot | 0.3.5 | 0.6.0 | [0.6.2](https://github.com/BrokkAi/brokk-town/releases/tag/v0.6.2-bug-bot) |
| feature-bot | 0.1.2 | 0.4.0 | [0.4.2](https://github.com/BrokkAi/brokk-town/releases/tag/v0.4.2-feature-bot) |
| issue-bot | 0.5.4 | 0.5.9 | [0.5.11](https://github.com/BrokkAi/brokk-town/releases/tag/v0.5.11-issue-bot) |
| mayor-bot | 0.1.1 | 0.1.2 | [0.1.5](https://github.com/BrokkAi/brokk-town/releases/tag/v0.1.5-mayor-bot) |
| release-bot | 0.6.1 | 0.8.0 | [0.8.2](https://github.com/BrokkAi/brokk-town/releases/tag/v0.8.2-release-bot) |
| repo-bot | 0.1.0 | 0.1.2 | [0.1.5](https://github.com/BrokkAi/brokk-town/releases/tag/v0.1.5-repo-bot) |
| review-bot | 0.2.4 | 0.2.9 | [0.2.12](https://github.com/BrokkAi/brokk-town/releases/tag/v0.2.12-review-bot) |
| simplifier-bot | 0.1.1 | 0.1.3 | [0.1.6](https://github.com/BrokkAi/brokk-town/releases/tag/v0.1.6-simplifier-bot) |
| Town | 0.6.5 | — | [0.7.0](https://github.com/BrokkAi/brokk-town/releases/tag/v0.7.0-town) |

- Integrate master through ce49c44, retaining its Mjolnir model/effort discovery,
  execution selections and fake-daemon smoke alongside the npx compatibility
  checks. Validate the integration, then merge the release PR after its checks.
- Integrated root race tests/vet, frontend checks/tests, workflow lint, Town build,
  demo and fake-Mjolnir smoke pass. The bots are unchanged from the already-passing
  nine-module validation. All forty-five target npm versions are available.
- Release PR #165 includes the integration. Its initial module checks passed.
  Publisher review found Release Bot still builds its retained Python artifact
  alongside npm; add pinned setup-uv/uv versions so its publishing runner has
  the required build tool. Python publication remains a separate action.
- PR #165 merged as a967d8b with the concurrently merged attention hook. The
  combined root race/vet, frontend and integration checks passed locally, as did
  complete non-publishing Town 0.7.0 and Release Bot packaging builds.
- The first bot tag attempts passed Linux but macOS found a test comparing
  /var and /private/var as different directories. Publication was gated off;
  remaining attempts were stopped and stable npm versions remain unchanged.
  Preserve those immutable tags and use fresh patch tags in the table above.
  Reproduce the failure through a symlink on Linux, then check directory
  identity and canonical cwd while retaining the startup-version/reuse checks.
  The explicit symlink fixture fails before the assertion fix and passes after;
  full root race tests and vet also pass. Run both hosted platforms on this fix
  before tagging the replacement releases.
- PR #167 passed Linux and macOS and merged as 95239ea. A concurrent unrelated
  storage feature changed the merge tree, so freeze the release source at the
  exact tested candidate dbafc82. Eight replacement bot tag workflows started;
  Bug, Feature and Issue Bot publications succeeded.
- Mayor, Repo and Simplifier Bot have MIT project licenses and npm metadata,
  but their copied npm validators incorrectly require Apache-2.0. Publication
  stopped before upload. Preserve the attempted tags and use fresh patch tags
  above. Correct the validators to require MIT, retain exact legal-file checks,
  reject missing/wrong license fields, and exercise actual npm packing for all
  five packages from verified fixture assets. The packaging regression fails
  before the fix; all three full Python suites, race tests, vet, launcher tests
  and license checks pass after it. All fifteen replacement npm versions are unused.
  Complete non-publishing builds from 19dfa58 verify twelve native archives and
  fifteen npm packages. Integrate master f7901cb to resolve PR #170's plan-only
  conflict, preserving its storage and incremental-inventory changes. Revalidate
  the integrated root and Repo Bot; the other bot code remains unchanged.
- Publish immutable per-project suffix tags from each exact tested revision.
  Wait for bot workflows and all forty bot npm packages, then verify each
  published worker's v1 initialization and parent-loss lifecycle without jobs.
  Only then publish Town and verify its four native archives and five npm
  packages. Use the configured Actions publishers; do not bypass checks.

- PR #170 passed CI and merged as 3cfe01a. The exact tested candidate 492c6a1
  passed integrated root and Repo Bot race tests/vet, frontend checks/tests,
  demo isolation, all-eight npm worker compatibility and fake-Mjolnir smoke.
  All three corrected native/npm packaging builds also passed from that source.
- Review Bot's macOS runs at dbafc82 repeatedly hit an HTTP EOF in a CLI fixture.
  The integrated source already contains #168's readiness-pipe test correction:
  duplicate the descriptor so finalization cannot close a later unrelated socket.
  Preserve the failed 0.2.11 tag and release 0.2.12 from 492c6a1. Use that same
  validated source for Town; its complete non-publishing packaging build passes.
- All eight final bot releases passed Linux/macOS CI and are published. Bug,
  Feature, Issue and Release Bot use dbafc82; Mayor, Repo, Review and Simplifier
  Bot use 492c6a1. All forty npm packages pass version, integrity, payload and
  source checks. Each actual latest-to-exact npx worker serves the retained v1
  capabilities and exits on parent loss. All native manifests, asset sizes and
  GitHub digests match. Release notes are published for each bot.
- Waited for npm's latest/full/install metadata to agree. An early Repo Bot
  install saw stale native-package metadata; after propagation a fresh isolated
  npx installation passed. No repository jobs or live agents were dispatched.
- Only after all bots were verified, pushed v0.7.0-town at 492c6a1. Town release
  workflow 36236599175 passed and published the release. All four downloaded
  native archives pass checksum, payload and source checks and contain only bt.
  All five npm packages pass version, integrity, payload and exact-source checks;
  the actual npx latest launcher reports v0.7.0. GitHub's latest stable pointer
  is v0.7.0-town. Town release notes link all eight independent bot releases.
- Final audit: nine successful release workflows, forty-five verified npm
  packages, thirty-six native archives checked against source manifests and
  GitHub asset digests, and eight successful published-worker v1/lifecycle
  checks. Earlier failed tags remain unchanged and produced no published
  releases. No GitHub checks were bypassed and no live repository automation
  was used for tests.

## Restore independently released bots through npx (complete)

- User chose independent bot releases and npx startup; the proposed unified
  version/release change was canceled before any files changed.
- Town installs/builds only bt. Resolve each independent bot's latest stable npm
  release at worker startup, then launch that exact resolved version for the
  worker lifetime. Record its identity and check the API before dispatch.
  Every new bot retains older APIs; Town continues using v1 even when a bot
  advertises newer versions. Keep independent modules, persistent processes
  and uncertain-write recovery.
- Add a private liveness socket that survives the npm launcher chain. Keep
  inherited parent descriptors working for existing Town releases. Bound package
  preparation, cancellation and shutdown.
- Keep bot version numbers and release workflows independent from Town. The
  latest-release policy replaces the earlier proposed Town dependency pins.
- CI now propagates each failed module check instead of letting a later passing
  shell command mask it. A pre-existing live Muse test is now explicitly opt-in;
  its automatically started adapter probe was interrupted during validation.
- Ordinary dispatch and issue-job summaries reuse the running worker's recorded
  identity without querying npm again. Future bots advertising v2 alongside v1
  successfully serve the existing client's v1 request in the protocol fixture.
- All nine modules pass race tests, vet, license checks, Python packaging tests,
  npm launcher tests and installer syntax checks. Frontend checks/tests,
  actionlint and local demo lifecycle integration pass. The remote-head polling
  success fixture now allows three seconds for its Git subprocesses; the
  never-catches-up fixture retains its short deadline and uncertainty assertions.
- Real offline npx fixtures pass for all eight independently built bots, checking
  v1 initialization, every existing capability and shutdown on parent loss.
  Retain those capability checks, including policy, retries and issue summaries.
- Implementation committed as 07dab9b. Non-publishing v0.0.0-town packaging from
  that exact commit built and verified all four native archives and five npm
  packages. Content checks confirm only Town is packaged; the native executable,
  offline npm installation and npx launcher version checks pass.
- No release or tag was published. For the transition, publish bot releases with
  the additive parent-socket capability before the new Town release; publication
  still requires a release request.

## Standalone projects and explicit lifecycle (complete)

- Import eight standalone bot projects under bots/, preserving independent modules,
  CLIs, tests, packaging and source provenance. No shared bot module or cross imports.
- Build complete Town bundles from bundle.json; release projects with separate suffix
  tag workflows, independent bot versions and existing npm package identities.
- Delete TUI and Town/bot upgrade code and its dedicated tests. No migrations,
  compatibility shims or tests asserting deleted features are absent.
- Foreground service by default; -d starts a simple background process. Clients do
  not auto-start services. Remove login registration and automatic replacement.
- Start eight persistent workers per town, even when paused; schedule work separately.
  Stop workers and descendants with Town, including unexpected parent death.
- Replace Issue Bot imports with private protocol job-summary and retry operations.
  Preserve exact revisions, durable write intent and uncertain-outcome recovery.
- Validate nine modules with race tests/vet, browser checks, standalone launcher,
  packaging/license checks and isolated lifecycle integration. Commit on current
  branch; no publication, live automation, or original checkout changes.

## CLI for the foreground lifecycle (complete)

- The CLI surface was built around an always-running daemon; reshaped it for
  foreground-by-default with `-d` as the option. Clean break, no aliases.
- Removed `bt service status|stop`: `bt status` summarizes a running or stopped
  Town (`--json` keeps the full state) and `bt shutdown` stops it.
- Removed `serve`: bare `bt` runs Town and `-d` re-executes bare `bt` with
  `--state-dir` and `--listen`. Removed `serve --repo`, which duplicated `bt add` with different settings; `--config`
  stays for declarative setup.
- `capacity` folded into `bt settings --max-workers` (service-wide, no `--repo`),
  beside the service default `--quiet-hours`; both are validated before either
  is sent. `check-request` became `request --check --request-id ID`.
- Every command rejects flags it does not declare instead of ignoring them.
  `--listen` shows only on startup; `delete` no longer offers `--role`.
- `bt harnesses` reads (and `--refresh` updates) the local registry cache when
  Town is stopped, the same cache the next start loads.
- `bt -d` re-executes bare `bt` with `setsid` and waits on a readiness pipe
  (descriptor 3, named by `BT_READY_FD`, like sd_notify or s6's
  notification-fd) instead of polling for 60 seconds. Town writes `ready` once
  it serves; end of file means it exited, so a failed start is reported at once
  with the error log's tail. The child marks the descriptor close-on-exec and
  drops the variable before starting bots, so no descendant holds the pipe.
- Dropped what only served earlier versions or nothing: the old-log access key
  scrubber, the array config form (`--config` takes only the object with a
  `towns` array), and the unread `executable`/`started` connection fields.

## Validation and delivery

- Imported all eight clean source checkouts with original commit provenance.
- Removed Town's Issue Bot Go dependency, TUI, login registration, runtime version
  selection, update polling/installers and their tests. No compatibility migration.
- Added complete native/npm bundles, independent suffix release workflows and
  version validation. All 45 external publishing connections were repaired and
  verified on 2026-09-22 as part of the authorized release.
- Persistent processes start for every house. Sequential-job, overlap rejection,
  cancellation and parent-pipe tests pass across worker implementations. Issue Bot
  summaries/retry are covered by a real local protocol test preserving evidence.
- Copied 16 open source issues and 6 attributed discussion comments into Town
  (#96–111), with mapping in docs/imported-bot-issues.md. Originals unchanged.
- Nine-module Go race/vet validation, Python packaging/installer/license and npm
  launcher checks completed (Feature Bot's changed shutdown expectation corrected
  and its worker tests rerun). Browser: 39 tests and syntax pass. Demo foreground/
  daemon smoke and all eight real worker initialization/parent-loss checks pass;
  no real jobs, agents or GitHub automation were used for development tests.
- Built and validated all four native bundles and five npm packages from commit
  67e7262 using check-only mode; nothing uploaded. Workflow lint and final root/
  Issue Bot race tests and vet passed. Implementation committed on master.

- Published the consolidation to Brokk Town master, verified all eight destination
  directories, updated source README notices/descriptions/homepage links, and
  archived all eight original repositories at the user’s explicit request.

## Town 0.6.0 release (complete)

- User authorized a Town release and local installation update, plus repair of
  publishing trust for all eight bots. Publish only through GitHub Actions.
- Fixed Feature Bot shutdown using a separate bounded drain context after job
  cancellation; worker race tests passed 20 repetitions and vet passed.
- Advanced all eight bundled bot patch versions for their changed lifecycle code.
  Corrected the npm launcher description to browser and CLI clients.
- Town race/vet, 27 packaging tests, browser syntax and 39 browser tests pass.
- Replaced and read back all 45 npm publisher connections for Town and eight bot
  families, preserving environments and staging permissions. Each now trusts its
  release workflow in BrokkAi/brokk-town with direct publishing enabled.
- All 19 preparation CI checks passed. Tag v0.6.0-town at fe09bb7 triggered
  GitHub Actions run 35703157496; all 20 jobs succeeded. Four native bundles and
  five npm packages were submitted by Actions; npm reported processing pending.
- Installed the checksum-verified native v0.6.0 bundle in ~/.local/bin while npm
  processed its packages, and removed the superseded npm 0.5.0 installation.
  bt reports v0.6.0; all eight installed bots initialized at manifest versions
  and exited on parent loss with no jobs dispatched. Town remains stopped.

## Local startup repair after 0.6.0

- Plain installed bt failed with "read town state: invalid task kind": six obsolete
  upgrade tasks remained in three deleted local towns. Installation verification
  had missed loading the existing state.
- Backed up the local state with private permissions and removed only those six
  entries under the service lock. No product migration or upgrade code was added.
- Verified the installed binary starts using a temporary copy of the repaired
  state with all workers paused, serves authenticated HTTP 200, and shuts down
  cleanly. Actual worker settings and other saved work remain unchanged.

## Actionable attention inbox

- Replaced raw failure cards with a plain-language explanation and next step.
  Missing agent executables link directly to the affected bot’s agent settings.
- Release retry exhaustion links the previous failed GitHub workflow and offers
  the existing worker-protocol retry operation, with confirmation that it resumes
  work and may publish. Manual policy and recovery holds suppress the retry.
- Kept full errors in expandable technical details, explicit task/bot-log links,
  stable expanded state, keyboard focus, and visible request errors.
- Go race tests/vet and frontend syntax checks pass; 40 browser tests cover
  settings navigation, workflow links, canceled/accepted/rejected retry, policy
  restrictions, and uncertain-outcome guidance. No live actions used for tests.

## Town 0.6.1 release (complete)

- Released immutable tag v0.6.1-town at a733129 through GitHub Actions run
  35710266271. All 19 checks and the publishing job passed.
- Recovered an npm Linux ARM64 404 after verifying its correct publisher trust,
  then a duplicate draft caused by GitHub omitting drafts from by-tag lookup.
  Backed up and checksum-verified unpublished assets before recreating drafts;
  the release tag and source commit were unchanged.
- Fixed future draft lookup in Town and all eight standalone bot publishers,
  with mocked pagination, missing-draft, duplicate-draft, and existing-release
  tests. Root packaging tests (30) and all eight bot publisher test suites pass.
- Installed the checksum-verified native 0.6.1 bundle locally. Verified startup
  with a paused copy of current saved state, HTTP state and new inbox assets,
  and clean shutdown without dispatching repository jobs or requests.
  bt reports v0.6.1; the actual Town service is stopped and ready for the user.

## Per-house work policies (#3)

- `Config.BotPolicies` gives each house label filters, one pinned issue or pull
  request, a discovery focus, a per-run limit, an attempt limit, its own
  verification command, and for the release house the cadence, preflight,
  required workflows and required assets. Every field maps onto a setting the
  bundled bot already has; `policySupported` is the single table validation and
  the operator-facing summary both read.
- Carried over the worker protocol as `policy`, behind a new `policy`
  capability. A configured policy dispatched to a worker that does not
  advertise it is refused by name and version rather than run unfiltered. All
  eight bots read it; the town-wide `verify` is deliberately not treated as a
  policy, so towns that only set it keep dispatching unchanged.
- repo-bot now reports issue and pull-request labels, Reconcile stores them on
  the task, and Town applies the same filters to its own selection that it
  sends to the bot. Filtered work stays in the snapshot marked
  `policy_excluded` with a per-town count, and each house's inspector states
  its policy and how much inventory it holds back.
- Public config reports command arguments as lengths only, so verification and
  preflight arguments never reach a client snapshot.
- Settings API, `bt settings --role BOT` flags, and `bot_policies` in the
  config file all edit the same thing; `--clear-policy` removes one.

## Agent budgets (#6)

- `Config.Budget` bounds agent attempts and agent minutes per day, week or
  month of local wall clock. `Town.Budget` is a durable per-period ledger, so
  totals survive restarts and the snapshot path costs nothing to read.
- Only attempts that held an agent slot are charged: a repository inventory is
  not, a branch repair is. An attempt with no measured elapsed time is counted
  in `untimed` rather than billed as zero.
- An exhausted budget holds new agent dispatch and branch repairs for that town
  only. Running work finishes, cancellation and saved work are untouched, and
  the inventory keeps running so uncertain writes still reconcile.
- No token or monetary cap is offered. No released acp-go runner reports usage
  to a bot -- `runner.Execute` returns the agent's final text and nothing else
  -- so every bot result carries no usage and no cost. `BudgetState` leaves
  `usage` and `cost_usd` null and clients render "not reported"; the validation
  error, CLI help, Town Hall and README all state the limitation.

## issue-bot on acp-go 0.8.1 (#102, fixes #106)

- acp-go 0.8.1 released first: `clienthost.Host.Answer` ignored
  `ContentChunk.MessageID`, so an agent that ended a commentary message without
  a newline had it welded to the receipt that followed. That was the cause of
  #106, and it was still present in 0.8.0, so the upgrade alone would not have
  fixed it. BrokkAi/acp-go#7, tag v0.8.1.
- issue-bot moved from acp-go v0.1.0 to v0.8.1. No source changes were needed:
  `runner.Runner`, `Config`, `AgentConfig`, `SetupError` and `Execute` are
  unchanged, and feature-bot and release-bot already ran v0.7.0 and v0.8.0.
- Licence review: acp-go's LICENSE is byte-identical, and v0.8.1 adds a NOTICE
  the 0.1.0 tree did not have. Recorded both in `licenses/policy.json` and
  regenerated the notices, so Apache-2.0 attribution now ships with the bot.
- Added `agent_acp_test.go`: a credential-free simulated ACP agent over stdio
  that drives the real `agentProcess`, covering startup, session setup,
  model/effort selection ordering, transcripts, cancellation, and a rejected
  model. Nothing else in the suite exercised the runner, so a wire regression
  would have gone unnoticed. Confirmed the #106 case fails on v0.1.0 with
  "agent did not finish with an ISSUE_RESULT receipt" and passes on v0.8.1.
- `bundle.json` moves issue-bot to 0.5.7 at the upgrade commit. Town builds each
  bot from this checkout and stamps the version from that manifest, so the pin
  was the only stale part; leaving it at 0.5.6 would have shipped a binary
  labelled 0.5.6 that already contained acp-go 0.8.1. Verified the rebuilt
  bundle reports v0.5.7, links acp-go v0.8.1, and that the other seven bots are
  untouched on v0.1.0. The worker smoke test initializes all eight.
- Remaining: a Town release publishes this to users. RELEASING.md requires an
  explicit request for that.

## Town on acp-go 0.8.1 (fixes #59)

- The root module moved from acp-go v0.1.0 to v0.8.1, the latest GitHub
  release and the version issue-bot runs. v0.9.0 is tagged without a release
  and only changes the draft-v2 packages and `clienthost` tool-call titles.
- `runner.AgentConfig` keeps its JSON tags, so saved town state loads
  unchanged.
- `runAgent` calls acp-go's `runner.Runner.Execute` directly. v0.8.1
  `SetEffort` selects the thought_level category, else an uncategorized
  `reasoning_effort` ID; v0.1.0's uncategorized `thought_level` ID and
  reasoning_effort category fallbacks are gone (see "acp-go upgrade policy").
- The harness choice probe spoke the v0.1.0 hand-written types. It now reads
  generated `schema.SessionConfigOption`s through `sessionSelector`, the same
  lookup acp-go's `SetModel`/`SetEffort` use (acp-go does not export it),
  flattens grouped values the way acp-go does (the first entry decides), and
  maps them to Town's own `ChoiceValue`, so `/api/choices` keeps its
  `{value, name}` shape. It advertises session config options, as a run
  does, so both see the same selectors.
- Accepted behaviour changes from acp-go: `SetMode` now requires the mode to
  be advertised (in `modes`, or a mode config option) and fails the run
  otherwise, where v0.1.0 sent `session/set_mode` blindly. `session/new` now
  fails when an agent advertises a config option of an unknown type, where
  v0.1.0 kept it. Both surface as setup errors naming the cause.
- Licence review: LICENSE byte-identical, NOTICE new in v0.8.1. Both recorded
  in `licenses/policy.json`; notices regenerated.
- Added `internal/town/agent_acp_test.go`: a credential-free simulated ACP
  agent from the test binary, driving `runAgent` over stdio. Covers startup,
  model/effort selection before the prompt, permission auto-approval,
  transcripts, a rejected model as a setup error, and cancellation of a
  prompt in flight. The commentary-then-receipt case fails on v0.1.0.
- Bots keep their own pins; review-bot's upgrade is #107.

## Retargeted pull requests (#9)

- A pull request retargeted to another branch keeps its head and base commits,
  so a clean audit stayed valid and the merge gate, which compared SHAs only,
  still allowed it. Town could squash-merge into a branch it was never
  configured for.
- `MergeGate` now carries `baseRefName`, and `Allows` takes the town's branch
  and rejects unless the pull request and the gate both report it. Each
  observation is checked on its own so one lagging behind the other cannot let
  a merge through.
- `Reconcile` no longer skips a retargeted pull request silently: an existing
  task loses its audit and is blocked with `Offbranch`, which is the record
  that lets Town release its own block when the pull request comes back. On
  return the task is queued for a fresh review rather than resuming from the
  discarded audit. Merged and closed tasks are left alone.

## Stale Simplifier results (#95)

- `applySimplification` wrote its assessment to the task unconditionally. Repo
  Bot can observe a pull request closing while Simplifier is still running, so
  a late `auto`/`admit` result overwrote a confirmed `closed` with `queued` and
  handed the closed pull request to Review.
- The assessment is now bound to the intake it answers: it applies only while
  the task is still `house=simplifier stage=simplifying`. That also stops a late
  result from overriding a Mayoral decision taken in the same window, and stops
  a late failure from charging an attempt or a retry delay against work that
  already left intake.
- A discarded assessment records an event, so the operator sees the result was
  dropped rather than silently lost.

## Every bot runs standalone

- mayor-bot (`bmb`), repo-bot (`brp`) and simplifier-bot (`bsb`) served only
  `worker --socket`. Each now has the same no-config CLI as the other five:
  repository discovery (checkout, bare repository or URL) into a managed
  workspace under `$XDG_STATE_HOME/<bot>`, agent resolution with the `npx`
  fallback, and the shared `--config --branch --agent --agent-arg --model
  --effort --poll --timeout --once --json --plain` options plus `status` and
  `version`.
- `bsb` scans on its poll interval and adds `assess --issue|--pr --mode`.
  `brp` observes and repairs on its poll interval (`--agent ""` observes only).
  `bmb` writes a bulletin each interval from where the last recorded one ended,
  and adds `bulletin --since` and `judge --issue|--pr`; it still never writes to
  GitHub. Mayor and Repo configs gain a `poll` setting (24h and 15m).
- The worker protocol is unchanged.
- All eight bots have the terminal dashboard: overview, item browser with detail,
  activity, the same keys, `--plain`/`--json`, `NO_COLOR`, and an exit summary.
  Simplifier browses saved proposals; Mayor browses recorded bulletins, whose
  state now keeps each summary and item; Repo browses its last 100 standalone
  observations, saved in its state. Worker runs still report only phase and
  task. The PTY lifecycle tests pass for the three in a Linux container.
- Review of #120 found two simplifier scan bugs that made standalone `bsb`
  useless: the schedule check was inverted (a fresh workspace never scanned,
  a scanned one rescanned regardless of interval), and scans ran in the base
  clone, whose working tree never left its first checkout, so a scan after the
  branch advanced read stale code and failed its own revision check. Scans now
  run when due in a detached worktree at the fetched head. Town dispatches only
  item assessments, so its behavior is unchanged.
- A second review of #120 fixed three standalone gaps. `bmb` continued from
  "one interval ago" after a window that recorded no bulletin, so an empty
  window skipped the merges during its own run and a failed first window lost
  its whole range; the next window now starts where the last covered or failed
  one did. `brp` took no lock, so two standalone processes on one branch could
  both spend its repair budget and push repairs; a standalone watch now holds a
  per-branch and a per-state lock. Its saved history could outgrow the 1 MiB
  state read limit (escaped check output) and leave a state it could not read;
  the oldest observations now give way before the state is saved.

## Frontline theme (browser, presentation only)

- 2026-09-23 per-base faction clarity: the overview cards display three
  cropped structures from each base's faction atlas, faction color, and a named
  badge; the sidebar names the race too. Town cards keep their cottage art.
  Each town independently takes one of the three available race styles;
  repeats are allowed. A browser user can change a base's race with the
  selector. Browser tests cover repeating automatic races and matching art.
  Frontend syntax, 66 browser tests, `go vet ./...`, and
  `go test -race ./...` pass.

- 2026-09-23 attack and defender pass: a new transparent six-unit atlas gives
  every installation two faction ground defenders and an emplacement. Working
  status affects patrol stance; idle defenders remain visible. A committed
  delivery now fires human tracers, alien energy lances, or swarm spores as it
  approaches, with faction-specific impact and defender response. Art loads
  when the skin is selected, outside the draw loop. No event, command, or
  worker behavior changes. Frontend syntax and 56 browser tests pass;
  `go vet ./...` and `go test -race ./...` pass.

- 2026-09-23 visual refresh: four original transparent image atlases replace
  the flat canvas silhouettes when loaded: eight isometric buildings for each
  of the three factions and a matching three-craft strip. Canvas silhouettes
  remain a loading fallback. The map and HUD received a dark metal treatment;
  events, commands, faction choices, and Town art are unchanged. Verified with
  55 browser tests, frontend syntax checks, `go vet ./...`, and
  `go test -race ./...` (the race suite needed localhost access outside the
  sandbox for its `httptest` servers).

- Added `internal/web/skins.js` as the single surface both themes answer
  (landscape, structure, occupants, strike, impact, labels, faction, noun
  rewrite) and `internal/web/frontline.js` for the war art: three armies, a
  per-army name and tagline for all eight installations, terrain seeded by the
  repository, a structure per role, patrolling garrisons, strike craft carrying
  the real issue or pull request number, and impact bursts. No new image assets.
- Every themed string is wired through `data-skin-text`, so a theme is added by
  supplying strings and art, not by threading conditionals through render code.
  `index.html` carries the theme button, the faction picker and a note that the
  theme is a look rather than a lever.
- A base's automatic faction derives from its repository name and can be
  changed for that browser in `localStorage`.
  `?skin=frontline` opens the theme from a link. Nothing on
  this path touches town state, commands, the worker protocol or GitHub, and
  the delivered animation still follows committed events and reduced motion.
- 8 new module tests plus 2 app tests drive the real handlers (43 → 53 browser
  tests); `npm run check` covers both new files. Both themes were rendered in a
  headless browser against a stubbed snapshot to check the art and labels.

## Demo state recovery

- `bt --demo` refused to start with "read town state: missing worker" against a
  demo state written on 2026-09-16, before the Clarifier house and Mayor Bot
  existed. Demo state is disposable, so `Open` now sets an unreadable demo state
  aside as `state.rejected-<timestamp>.json`, keeps every byte at 0600, records
  a one-time `Store.Notice()` that `bt` prints, and starts an empty demo state
  that `town.Demo` seeds again.
- Only a state that parsed and reports `Demo: true` is replaced, and only for a
  store opened in demo mode. Real state — including real state left in the demo
  directory — is still refused and left exactly as it was, and a file Town
  cannot parse is never assumed to be disposable: that error now says where the
  demo keeps its state and how to start over.
- `validateState` now names the town and the missing house, so the failure that
  began this ("missing worker") can be diagnosed from the message alone.
- Verified against the real stale file: the demo started, the 50608-byte state
  was preserved as `state.rejected-20260923-071158.json`, and the seeded villages
  came back. Five store tests cover the reset, the protection of non-demo state,
  an unparseable file, a usable demo state, and the reseeding.

## PR #114 review fixes

- Demo recovery now reads the `demo` marker from the saved JSON itself. A file
  with no marker cannot open as demo or be set aside, even though the store's
  initial in-memory state is demo; regression tests cover both valid and stale
  files without the marker.
- Frontline keeps faction overrides in memory for the browser session. A denied
  `localStorage` write no longer resets the picker, and canvas redraws no
  longer read or parse browser storage. A browser test exercises both cases.
- Root `go test -race ./...`, `go vet ./...`, browser syntax and 54 browser
  tests pass. The fixes landed in PR #114 through follow-up PR #116.

## issue-bot --once after a reconciliation (#104)

- `step` reconciled a saved job's pull request and then fell through to fetch
  issues and attempt another one, and the fetch loop did the same with
  `continue`. `Run` checks `--once` only after `step` returns, so one
  invocation could reconcile issue 1 and still start an agent for issue 2,
  contrary to the README.
- Both reconciliation paths now end the step and report work. Without
  `--once` the loop steps again immediately, so the queue still drains.
- Retained status-comment retries run over every saved job before the first
  reconciliation, so ending the step early does not defer them.
- Regression tests cover the restart path (a lost PR response reconciled on
  the next step), a PR found while fetching, and a later job's retained status
  comment; each failed before its fix.
- A reconciliation error still aborted the whole step; see #105 below.
- `bundle.json` moves issue-bot to 0.5.8 at the final fix commit.

## issue-bot reconciliation failures (#105)

- `step` returned on the first saved-job lookup error, before fetching issues,
  so one issue branch holding a PR the bot could not prove it owned (missing
  marker, several PRs) stopped every other issue on every poll.
- Ownership failures are now sentinels. `lookup` records an `inspect branch`
  failure on the job, leaves its status, tries and URL alone, and the step
  skips that job for the rest of the scan, so it is never attempted or
  published again until its lookup succeeds. A later successful lookup
  reconciles it and clears the failure.
- Such failures are returned joined with the step's result. Any other error
  (network, auth, cancellation, state writes) still aborts the step. A status
  comment that fails after a saved reconciliation aborts too, since it cannot
  be told apart from a GitHub-wide failure; the job is already submitted, so
  the next step retries the comment and moves on.
- If the state write after a status comment fails, the job's comment stays
  pending in memory as well as on disk, so the running process retries it.
- A lookup that later finds no PR clears the stale `inspect branch` failure
  so it does not reach the next agent prompt.
- The daemon logs issue-only failures and keeps draining the queue; `--once`
  (Town's exact-issue worker) still does one unit of work and exits with the
  error, and the job summary carries the recorded failure.
- Regression tests cover repeated polls, restart from saved state, eventual
  reconciliation, a fresh job whose lookup fails, a global lookup failure,
  comment and state-write failures after a reconciliation, and the daemon
  and `--once` handling of issue-only failures.
- `bundle.json` moves issue-bot to 0.5.9 at the final fix commit.

## release-bot triage after a remote advance (#111)

- Triage cached its decision by the release head and released baseline only.
  When the bot's checkout holds unpushed commits, `releaseHead` is the local
  HEAD, so a new commit on the watched remote branch changed neither key and a
  cached `wait` hid it until the daily deadline.
- `TriageRecord` now also keys on the remote branch head, and the triage prompt
  and skill give the agent both heads so it inspects both histories. Ported
  from the unmerged upstream release-bot PR #18.
- Saved records from before the change have no remote head and are
  reassessed once rather than reused.
- A regression test (with and without a quiet period) failed before the fix.
- `bundle.json` moves release-bot to 0.6.4 at the fix commit.

## review-bot repository capitalization (#108)

- PR eligibility and state keys already treated `github.repo` case-insensitively,
  but `validatePublished` compared the review URL path byte-for-byte. With
  `github.repo: O/R` and GitHub's canonical `/o/r/pull/1`, a posted review
  stayed `posting` forever and every reconciliation reported it as uncertain.
- The URL check now compares only the owner/repository segment ASCII
  case-insensitively (it must still be a valid slug); scheme, host,
  credentials, query, raw path encoding, `/pull/<n>` and the review anchor
  stay exact.
- `ReadState` compares the saved repository the same way, and the saved host
  case-insensitively like the URL and lock checks, so restarting with a
  differently capitalized `github.repo` or `github.host` keeps the saved jobs
  and reconciles them instead of refusing the state. The loaded state takes
  the configured spelling, so `brv status` and the next save match the config.
- Tests cover ordinary success, lost-response recovery and restart with
  differing capitalization (each with exactly one POST), plus the URL fields
  that must still be rejected.
- `bundle.json` moves review-bot to 0.2.7 at the fix commit.

## Restoring deleted towns (#24)

- `Town.Restore` revives a deleted town under a complete, validated config;
  tasks, ownership, intents and recovery holds are kept, a branch change on an
  initialized town is refused, only the reporter is re-enabled and every
  worker's `Next` is cleared. The event says the town was restored.
- The add request (web, `bt add`) goes through `Supervisor.AddRepo`: a deleted
  town's kept config is the base, and only a non-empty merge policy and the
  agent settings are applied over it, so budget, policies, bot profiles,
  funnels (and their intents) and branch survive.
- `serve --config` restores a listed deleted town with the file's config (the
  same replacement a live town gets) and prints a notice. (`serve --repo` was
  removed with the CLI cleanup below.)
- Deleting a deleted town returns `unknown town` and appends no event.

## CLI polish (#28)

- `bt request` encodes without HTML escaping, so `<`, `>` and `&` no longer
  inflate sixfold and push a valid body past the service's 64 KiB limit. An
  oversized body now gets 413 and "request body exceeds the 64 KiB limit"
  instead of a decoder error.
- The browser link carries the access key, so `serve` and `bt -d` print it only
  when stdout is a terminal. The detached service's log and redirected output
  get "run bt web for the link"; `bt web` still prints the key on request.
- `request --check` (formerly `check-request`) on an unknown ID returns "unknown request" (404) rather than
  "does not need reconciliation".
- The stale `connection.json` PID was already handled by `processAlive`.

## review-bot on acp-go 0.8.1 (fixes #107)

- review-bot moved from acp-go v0.1.0 to v0.8.1, matching issue-bot and Town.
- `agentProcess.Execute` calls acp-go's `runner.Runner.Execute`, with the
  v0.8.1 effort selection described under "acp-go upgrade policy".
- Accepted as in Town: `SetMode` requires an advertised mode, and an unknown
  config option type fails `session/new`; both are setup errors.
- Licence review: LICENSE byte-identical, NOTICE new in v0.8.1; both recorded
  in `licenses/policy.json` and notices regenerated.
- Added `bots/review-bot/agent_acp_test.go`: a credential-free simulated ACP
  agent from the test binary driving `agentProcess` over stdio. Covers
  startup, model/effort selection before the prompt, permission
  auto-approval, transcripts, commentary-then-receipt, a rejected model and a
  missing command as setup errors, and cancellation of a prompt in flight.
- `bundle.json` moves review-bot to the next patch at the upgrade commit.

## Documentation drift (#29)

- `GitHubClient.Gate` and `api()` share one bounded `gh` runner
  (`GitHubClient.Timeout`, default one minute), so a stalled `gh pr view`
  cannot hold the review house's merge pass. An expired bound reports
  "gh ... timed out after 1m0s" rather than a killed process; a caller's own
  cancellation is reported as that. Worker jobs keep the two-hour
  `workerDeadline`.
- `bt settings --merge-policy bot|manual|all` sets the town policy through the
  existing settings API; it refuses `--role`.
- README states that config-file entries get no defaults, that a release retry
  starts a paused release house (a task retry does not), and what each
  deadline covers.
- Demo paper-trail opens with its own report instead of orchard's.
- The README keyboard map no longer exists; the browser help dialog lists the
  current 0–8 shortcuts. Release docs agree on 0.6.2.

## Reopened intake (#94)

- Reconcile sent every reopened or unlocked issue to `queued` without changing
  its house, so an issue closed before Simplifier or the Mayor handled it was
  left `simplifier/queued` or `hall/queued`, which no worker selects. A pull
  request reopened from Simplifier or Mayoral intake skipped intake and went
  straight to Review.
- `resume` now derives where the task returns from its house: Simplifier →
  `simplifying`, Town Hall → `awaiting_mayor` with a pending decision (issues
  and outside pull requests), a Mayoral or Simplifier decline stays `declined`,
  and work past intake returns to Issue or Review as before. It wakes the house
  that selects the task. A reopened pull request resumes before draft or lock
  state applies, so a later revision cannot carry it out of intake. `Blocked`,
  `Attempts` and `RetryAt` are left as they were, so a decision whose Mayor Bot
  attempts are exhausted still waits for the operator.
- A Simplifier auto-decline no longer yields to repository metadata: a new
  revision or a draft change no longer moves it to Review (`holdsIntake`). The
  Mayor (not Mayor Bot) can admit it anyway through the usual admit command
  (`bt admit`, or "Admit anyway" from Town Hall's "Declined by Simplifier"
  list), which clears the decline. A Mayoral decline stays final. Older state
  where a revision already carried the decline out of Town Hall (including a
  retired or reviewed pull request) has the stale decline cleared on the next
  inventory. Town's own pull request declined by Simplifier is closed and its
  issue started over (#126, below). "Admit anyway" asks for confirmation.
- The declined-issue closer claims each issue under the store just before the
  GitHub write: it rechecks the decline, and an auto-declined issue moves to
  `closing`, which admission refuses. An accepted close settles it `closed`
  (so a reopen before the next inventory is an appeal, not a close to retry),
  a definite 4xx rejection (`RejectedError`; not 408/429/rate limits) releases
  it to `declined`, and only an uncertain outcome keeps `closing`. A `closing`
  issue the full inventory no longer lists (deleted or transferred) is released
  to `declined`. The browser treats a `closing` issue as settled work. An
  admission that lands first makes the closer skip the issue.
- Someone reopening an issue Town closed on Simplifier's decline is an appeal:
  the issue waits for the Mayor with Simplifier's assessment attached, and the
  closer leaves it alone. Reopening neither re-closes it nor admits it, so no
  decision is made for the Mayor.
- Mayor Bot skips blocked decisions, as other selectors do, so it does not
  judge a pull request retargeted off this town's branch; the browser's "to
  decide" counts skip them too.
- Reconcile treats an explicit `"pull_request": null` in the issue inventory as
  an issue rather than a pull request.
- A pull request returning from another base branch resumes its intake or
  decline instead of going to Review; a pending one used to fail state
  validation there.
- Closure clears only a pending decision (the invariant keeps pending in Town
  Hall). A declined pull request its author closed is stored `closed` with its
  decline kept, so it is not counted as open or retired off-branch, and resume
  restores `declined` on reopen. An issue already stranded `queued` outside
  Issue is healed on the next inventory.

## bug-bot review model and effort (#97)

- Optional `review_model`/`review_effort` (`--review-model`/`--review-effort`)
  select the model and effort for evidence validation and every duplicate-review
  batch, including refreshed-history reviews and resumed pending candidates.
  Each omitted value inherits the discovery `agent.model`/`agent.effort`; CLI
  overrides JSON; explicit blank values are rejected.
- The review agent is the worktree config with only Model and Effort replaced,
  so the runner, startup retries, timeout and publication gates are shared. An
  unsupported selection is the runner's setup error: pending findings stay
  pending, no attempt is consumed, and nothing falls back to discovery.
- Logs tag agent sessions with `stage=discovery|review` and the effective
  selection; the dashboard shows both. Zero findings start no review session.
- Town and the worker protocol are unchanged: worker scans leave the overrides
  unset, so review inherits Town's agent settings as before.
- `bundle.json` moves bug-bot to 0.4.0 at the final feature commit.

## feature-bot review model and effort (#99)

- Optional `review_model`/`review_effort` (`--review-model`/`--review-effort`)
  select the model and effort for every review batch, coverage correction and
  review receipt recovery, including resumed pending candidates. Discovery and
  discovery receipt recovery keep `agent.model`/`agent.effort`. Each omitted
  value inherits its research setting; CLI overrides JSON; explicit blank
  values are rejected.
- The review agent is the scan worktree config with only Model and Effort
  replaced, built lazily for the first pending candidate, so zero findings
  start no review session. `agentProcess` selects effort with acp-go's
  `SetEffort`.
- A rejected selection is a setup error naming `--review-model`/`review_model`
  (or effort) and the adapter's available values. No attempt is consumed,
  candidates stay pending, no issue is created, and nothing falls back.
- `ReviewCheckpoint.reviewer` records the effective model/effort. A different
  reviewer, or a legacy checkpoint without one, restarts review from
  validation over every issue batch; discovery is not repeated. Equal
  effective values (explicit or inherited) reuse the checkpoint.
- Logs tag agent sessions `stage=discovery|review` with the selection; the
  dashboard shows both. Town and the worker protocol are unchanged.
- `bundle.json` moves feature-bot to 0.2.0 at the feature commit.

## Snooze one task (#62)

- `Task.DeferredUntil` and `Task.DeferReason` are operator state, published as
  `deferred_until` and `defer_reason`. Reconcile and funnel sync update tasks in
  place and never touch them; state validation rejects a reason over 200
  characters or a reason without a time.
- `Supervisor.Defer` sets or clears the snooze through one store update and
  wakes the scheduler. `/api/control` takes `action: "defer"` with an RFC 3339
  `until` and optional `reason`; `undefer` goes through `Control`. Refused: a
  past time, more than 366 days ahead, a multi-line or long reason, a commit or
  source task, a merged, closed, closing, declined or implemented task, and
  clearing a task that is not snoozed.
- Selection gates beside Blocked/RetryAt: `nextIssue`, `nextTask` (review,
  simplifier intake, repair "fixes"), `nextJudgment` and `mergeReady`. These
  take the clock explicitly so tests use fake time. A snooze stops new agent
  dispatch and merges for that task only; a running attempt finishes, and the
  Mayor's own admit/decline is not gated.
- Expiry: the scheduler's one-second pass clears ended snoozes, wakes the
  task's house (`Next` zero) and records "Snooze ended". Demo runs the same
  sweep on its own ticker, and the demo board seeds a snoozed `issue:707`.
  Selection ignores an ended snooze even before the sweep runs.
- Clients: `taskSnooze`/`taskStatus` show "Snoozed" (outranking blocked and
  failed, not a running attempt) until the resume time; snoozed work is not an
  attention item. The inspector offers **Snooze…** (dialog with local
  datetime and reason) and **Resume now**. CLI: `bt defer --until --reason`,
  `bt undefer`.
- Review follow-ups: an issue run's attempt and cost go to the issue it
  reports (the fallback skips snoozed issues); the reason limit counts
  characters on both service and browser; a snooze that ends on finished work
  is dropped silently; the inspector says retry keeps a snooze.

## bug-bot only-on-change polling (#98)

- Optional `only_on_change` (`--only-on-change`, default false). In daemon mode
  each poll still fetches; with no active scan left after reconciliation,
  pending retries and exhausted-revision checks, a fetched commit equal to the
  last completed scan for the same dry-run setting starts no investigation, no
  worktree and no history entry. `once` (and so Town's worker) always runs.
- State gains `last_completed {commit, dry_run}`, validated on read. Only a
  discovered scan that finishes without stale findings sets it, zero findings
  included. A scan with any `dry_run` finding records `dry_run: true`, so real
  publication can rescan that commit. Legacy state without it scans once.
- An unchanged poll reports `waiting` with an "unchanged" task; the dashboard
  shows it beside the next-check countdown without counting a completed scan.
- `bundle.json` moves bug-bot to 0.5.0 at the final feature commit.

## release-bot release_trigger_ignore (#110)

- Optional `release_trigger_ignore` config: repository-relative literal files
  and directory prefixes ending in `/`; empty by default. Absolute paths,
  backslashes, globs, surrounding whitespace, empty entries and empty, `.` or
  `..` segments are rejected. Matching is case-sensitive; a submodule is its
  bare path, so only a file entry ignores its bumps.
- Before a new automatic job (after pending-job recovery, before cadence and
  triage), `git diff --no-renames --ignore-submodules=none --name-status -z`
  (immune to `diff.ignoreSubmodules`) compares the released
  commit with the watched branch head and, when different, the local release
  head. A nonempty union of paths that are all ignored keeps monitoring with a
  "waiting" progress task that names the policy; no triage or preparation runs.
  Renames count both paths, deletions count, a net-empty diff and a release
  without a recorded time (first release, `initial_ref`) keep existing
  behavior, and a Git failure is returned as an error.
- The decision is recomputed from Git every cycle; nothing is saved, so the
  baseline never moves and a restart reaches the same answer. Commit counts,
  quiet period, publication and verification are unchanged. `--once --force`
  bypasses the policy.
- Town is unchanged: the worker protocol carries no such field (a non-goal of
  the issue), so the setting applies only to config-file runs.
- `bundle.json` moves release-bot to 0.7.0 at the final feature commit.

## Test coverage for GitHub, repair and result routing (#26)

- `github_client_test.go`: a recording fake `gh` on PATH (canned output per
  argv prefix, request bodies captured, unrouted calls fail) covers Pull,
  Actor, Discussion pagination and partial-read refusal, `pages` on a path
  with a query, Gate parsing, Merge with `merged:false` or no commit, the
  close/comment/create writes, DeleteBranch validation and "Reference does
  not exist", and Contains statuses and refusals.
- `repair_guard_test.go`: every repair guard driven to failure (dirty tree,
  left branch, amend, reset, no commit, empty and reverted commits, verify
  failing/committing/dirtying, redirected push remote), `checkRepairRef`,
  and `resumeRepair` against a bare remote whose pre-receive hook rejects
  the first push, including its saved-commit, dirty, verify and moved-PR
  refusals.
- `execute_routing_test.go`: table test of `execute` result routing.
- `worker_log_test.go`: redaction, bounding and drop-oldest behaviour.
- Fixed: `workerLog` printed grouped and `LogValuer` attributes whole, so a
  `token` nested in a group reached persisted logs; it now resolves and walks
  groups. Its 4000-byte cut could split a UTF-8 character; it now cuts on a
  rune boundary. Key-based redaction cannot see inside values passed with
  `slog.Any` (structs, maps, errors, slices), so every formatted line is also
  scrubbed of credential-shaped text (GitHub tokens, `x-access-token:` URLs,
  Anthropic keys, bearer values); a secret with no recognizable shape inside
  such a value is still printed.
- Git fixtures set `GIT_CONFIG_GLOBAL=/dev/null` and `GIT_CONFIG_NOSYSTEM=1`,
  so a developer's hooksPath or gpgsign cannot break them.
- internal/town coverage 74.4% -> 76.7%.

## feature-bot completed-workspace pruning (#101)

- `engine.finish` appends `completed_workspaces` (directory, scanned commit,
  completion time) only for a successful completion, in the same atomic state
  write; zero-finding and dry-run scans qualify, failed (still active) and
  discarded or stale scans do not. `ReadState` rejects invalid, duplicate or
  out-of-tree records.
- `bfb prune --older-than D` previews records completed strictly before now-D;
  `--apply` runs one-`--force` `git worktree remove` (removes untracked and
  ignored artifacts, never overrides Git locks). Both take `lockConfig` and
  refuse while research runs. No fetch, `gh`, agent or `git gc`; no records
  means no Git call at all.
- Removal requires a canonical (symlink-free) `scan-*` path under
  `<checkout>-scans`, a registered, unlocked, detached worktree of the managed
  clone at the recorded commit, matching common dir and gitdir back-link, no
  index flags that hide changes, and no tracked or staged changes. The active
  scan (including exhausted retries and `posting` candidates) is skipped by
  path and resolved alias.
- Each retirement is saved separately. A missing directory with a registration
  has its private index checked before removing the registration; with neither
  left, the record is retired. Removal failures keep the record, continue with
  the rest and return a joined error. Legacy workspaces stay external.
- A registered directory without `.git` (interrupted `worktree remove`) is
  finished with `RemoveAll` plus `worktree remove` only if the admin index
  equals the recorded commit, has no hiding flags and the remaining files show
  no non-deletion changes; otherwise it is a policy skip with README recovery.
- Policy skips exit zero; skips from Git/IO errors exit non-zero. Prune's Git
  calls drop inherited `git rev-parse --local-env-vars` overrides
  (`osrun.RunWithout`). Git 2.36+ is required for `worktree list -z`.
- Town and the worker protocol are unchanged; ported from the unmerged
  BrokkAi/feature-bot#17. `bundle.json` moves feature-bot to 0.3.0.

## Quiet hours (#50)

- `QuietWindow{days, start, end}`: days name the start day (mon..sun); times
  are HH:MM on the service's local wall clock, `24:00` only as an end, and an
  end at or before the start crosses midnight. Validation refuses no day, an
  unknown or repeated day, a malformed time, start equal to end, and more than
  32 windows. Overlapping or touching windows merge.
- `ServiceConfig.QuietHours` is the default; `Config.QuietHours *[]QuietWindow`
  is a town's own: nil follows the default, an empty list opts out. `SetCapacity`
  now edits only `max_workers`, and `/api/capacity` takes only that field.
- `Town.QuietState(now, service)` is derived, never stored: source, windows,
  active, `until` (merged end) or `next` (next start), reason. Membership is by
  wall-clock week minute, so DST skipped hours are never quiet and repeated
  hours are quiet twice.
- Gates: `dispatchEligibility` holds every agent-slot house beside the budget
  hold; `claimRepair` holds branch repairs; the inventory still runs, but
  `reconcile` skips `closeDeclinedProposals`, `closeRetiredPulls` and
  `fileFollowUps` until the window ends. Merges run inside Review dispatch, so
  they wait too. Operator-submitted issues and source lifecycle actions are
  not held. Running work finishes.
- `Town.Quiet` records the last seen state so "Quiet hours began/ended" is
  announced once, including across restarts; the gate reads the clock.
- Projection: towns carry `quiet_hours`; `PublicConfig.quiet_hours` is the
  town's own setting (null inherits); enabled waiting agent houses show status
  `quiet`, distinct from paused, working, failed. Paused state is `Enabled`,
  unchanged, so both survive restart.
- Editing: `/api/settings` `quiet_hours: {windows}` (null = default, [] =
  none); `/api/quiet-hours {windows}` for the default; `bt settings
  [--repo] --quiet-hours SPEC|none|default`; config file `quiet_hours` at top
  level and per town. Browser: Town settings (follow default / own / none) and
  the Capacity dialog; header chip, house dot and Town Hall explain the hold.
- Review follow-ups: HH:MM refuses signs; an empty schedule must be sent
  as `none` or an explicit `[]` (`/api/quiet-hours` requires `windows`);
  `until`/`next` across DST resolve to the real instant the clock reaches
  (the jump past a skipped reading, the repeated reading still ahead);
  `mergeReady` rechecks quiet hours just before writing its intent.

## acp-go upgrade policy

- An acp-go upgrade adopts the released API as is: no copied upstream code or
  compatibility shims without a demonstrated loss. Town, review-bot and
  feature-bot all run agents through acp-go's `runner.Runner.Execute` and
  select effort with its `SetEffort`. No copied runner lifecycle remains in
  Town or any bot, and the `setEffort`/`setAgentEffort` shims are removed.
- v0.1.0's uncategorized `thought_level` effort fallback is gone. An agent
  that advertises effort only that way gets a setup error when an effort is
  configured, and Town's choices list no efforts for it.
- feature-bot is on acp-go v0.10.0, whose typed setup errors replace the
  lifecycle it had copied from v0.7.0. `selectionError` reads
  `runner.SetupError.Phase`: in `select model`/`select effort`, an
  `acp.UnknownSelectionError` (value not offered) is reported as not
  accepted, naming the stage setting to change; any other failure there,
  including `acp.UnsupportedSelectionError` and untyped errors such as an
  unconfirmed selection, is a failure to select naming the same setting.
  Other phases pass through unchanged. An untyped error is never read as an
  agent rejection.

## bug-bot workspace setup command (#96)

- Optional `setup` argument array in the `--config` file (default none; `null`
  in the example). A present value needs a non-blank executable; `[]`, blank
  executables and non-string arrays are rejected before scanning. No worker
  protocol change.
- `step` runs it after preparing the worktree and counting the attempt, before
  loading issue history or any agent: once per attempt, including attempts that
  resume pending review, never per review batch or ACP startup retry. It runs
  through `osrun.StartCommand` in the scan worktree with `BUG_COMMIT`, under the
  attempt timeout (process-group kill on timeout/cancel), keeping a 16 KiB tail
  of combined output for the error. `checkout.verify` then requires unchanged
  HEAD and tracked source; untracked and ignored files are allowed.
- Failures are plain errors (not `runner.SetupError`), so they consume the
  attempt, keep pending findings, save a `workspace setup` failure and set the
  retry delay from failure completion. Reconciliation-only completion, waiting
  polls, `status` and `report` never reach it; dry runs do. Progress reports
  `preparing` / "Running workspace setup".
- `bundle.json` moves bug-bot to 0.6.0 at the final feature commit.

## release-bot outcome notifications (#109)

- Optional `notify` argument array and required positive `notify_timeout` in
  an explicit config file; empty (the default) disables it. Discovery and the
  worker never set it, and the worker protocol is unchanged.
- `Run` delegates to `engine.run`. `finish` records a `verified` event (job
  identity and tries captured before the job is cleared) only after the
  receipt and baseline save succeed; `run` emits it after the cycle. An
  `errAttemptsExhausted` cycle emits `exhausted` before `run` returns, so
  new and startup exhaustion each notify once per invocation. Cancellation,
  backoff, setup errors and ordinary failures emit nothing.
- The hook runs via `osrun.StartCommand` in `state_directory` with literal
  args, JSON (`version: 1`) on stdin, discarded stdout, a 4 KiB stderr tail
  and the timeout killing its process group. Missing, nonzero and timeout
  failures are classified and logged only; they never touch state or the
  returned error. Payload: event, time, repository slug, branch, job id,
  release id, target, commit, tag, attempts; no failure text or paths.

## Browser redraws, reconnect and accessibility (#27)

- Writes in flight live in `pendingWrites` (keyed by town, role, action and
  task), not on an element. The markup renders a pending control
  `disabled aria-busy="true"`, so a snapshot redraw keeps it disabled and the
  inspector's signature diff repaints when a write starts or settles. A second
  press, or the other decision on the same inbox task, sends nothing. Covers
  worker Start/Pause/Stop, admit/decline/retry/resume, the header wake and
  pause, inbox decisions, usefulness judgments, and request receipt checks.
  Focus returns to the pressed control when the write settles; `#inspection`
  joins the focus-restore surfaces (keyed by `data-action` or id).
- Admit and decline share one key per task (inspector and inbox alike), and
  the header's wake and pause share one key per town. Every tracked write,
  and the receipt check, has a 30s `AbortSignal.timeout`; at the deadline the
  control is released even if the request ignores its signal, and the page
  says the write may still have been applied rather than that it failed.
- `/api/events` reconnects with `reconnectDelay`: 1s doubling, capped at 30s,
  jittered into the upper half. A frame, not an accepted request, resets it.
  A hidden tab schedules no retry and skips painting snapshots; becoming
  visible paints the held snapshot and retries at once.
- The motion toggle's `aria-pressed` is true while motion is on. Every dialog
  has `aria-labelledby` pointing at its heading.
- Layout, checked headlessly on the demo at 375-2000px: the header wraps at
  every width (no two-line labels, no horizontal overflow; the version string
  truncates, and phones drop the version and capacity readouts). The map
  legend moved to the top strip above the upper houses, since the lower row's
  labels reach the bottom edge whenever the map is short (761-1000px and the
  three-column layout at 1300px as well as phones). Below 1251px the
  inspector drawer spans the full height, so a wrapped header cannot
  misalign it.

## feature-bot selected dry-run publication (#100)

- New candidates record `commit`, the scan revision at which discovery
  validated their cited files; it survives `finish` into `completed`. Legacy
  candidates without it stay readable and are refused by `publish` with a
  new-dry-run instruction; no commit is inferred. `report` shows the recorded
  commit only.
- `bfb publish --list` reads state only and prints a stable selector
  (first 12 hex of sha256 over the request ID, so the marker stays private),
  eligibility, commit and title for completed `dry_run` proposals and any
  unfinished selection.
- `bfb publish --proposal SEL` takes `lockConfig`, refuses any active `Scan`,
  fetches and requires the branch head to equal the recorded commit, then
  saves `publication` (request ID, commit, a new `scan-*` worktree) and clears
  the dry-run review checkpoint. It verifies cited files in the new worktree
  and calls the shared `reviewAndPublish`, now scoped by an explicit `*Scan`,
  with dry run forced off. Discovery never runs.
- Errors keep `publication` with its failure so the same selector resumes in
  the same worktree with its fresh checkpoints and request ID; a confirmed
  create rejection restores `dry_run`. Verdicts and success retire it and
  record the worktree for prune; a resumed selection whose candidate already
  has a saved outcome (crash or failed save before retirement) is retired and
  reported without review or create. A `posting` selection only reconciles its
  marker (never re-POSTs, allowed during an active scan) and blocks other
  selections; a non-posting one may be replaced, recording its worktree for
  prune. Branch advancement (before or during review) refuses without
  replacement discovery.
- `reviewAndPublish` saves a candidate as `duplicate` of any issue that already
  carries its request marker (e.g. a hand-copied dry-run body), never creating
  or claiming it.
- The engine test fixture isolates Git from global and system configuration.

## Declined own pull requests (#126)

- A declined Town-owned pull request stayed open with nothing selecting it,
  and reconcile kept its issue `implemented` while it was open. This held for
  Simplifier's auto decline and for the Mayor's decline in suggest mode.
- Decision: close it the way a failed second review does (`retirePull` already
  closes Town's own work and leaves contributors' to the Mayor). Skipping
  intake for own pull requests was rejected: the README documents that
  implementation pull requests pass Simplifier, and the Mayor sees them in
  suggest mode. Outside pull requests are still only ignored.
- `applySimplification` and `decideTask` leave the own pull request
  `hall/declined` and wake Repo Bot. After each inventory, outside quiet hours,
  `claimDeclinedPulls` moves it to `closing` under the store just before
  `closeRetiredPulls`, which closes it, comments, deletes Town's branch,
  comments on the issue and requeues it (`Requeue`). Until the claim, the Mayor
  can admit an auto decline; `closing` refuses admission. A snoozed pull request
  is not claimed until its snooze ends.
- The decline is kept through `closing` and `closed`: Simplifier's assessment,
  and a Mayoral `declined` (state validation now allows `closing`). Comments,
  events and the issue's detail name the cause.
- Outcomes: a definite GitHub rejection (`RejectedError`) of the close
  releases an auto decline back to `declined` for the Mayor; any other failure
  keeps the claim. Reconcile no longer settles a `closing` pull request that
  GitHub lists closed, so a close that happened with an uncertain outcome, or
  whose later steps failed, is finished by the next inventory instead of
  stranding the issue. A draft or lock change no longer overwrites `closing`.
- Reopen: Town's own pull request closed on Simplifier's decline is an appeal
  and waits for the Mayor with the assessment kept; one the Mayor declined is
  closed again, as a Mayor-declined proposal issue is.
- Review follow-ups: the issue is commented on and requeued only while live
  state ties it to the pull request (`requeuedIssue`: the issue is
  `implemented`, not already requeued for it, and no other open Town pull
  request implements it). History is not consulted, so a reopened pull
  request that fails review again requeues its issue again, while one closed
  after Issue Bot opened a new pull request leaves the issue alone. The
  requeue marker is `brokk-town:requeued pr=N close=K`, where `Task.Closes`
  counts Town's close decisions, so a legitimate second requeue is explained
  and an uncertain post of the same one is not repeated (`IssueComments`).
  The pull request's closing comment carries `brokk-town:closed-after-review
  pr=N close=K`; when GitHub already lists a `closing` pull request closed
  (its close or comment had an uncertain outcome), the closer posts that
  comment unless it is already there, instead of skipping it. After GitHub closed the pull request, a definite rejection of the PR or
  issue comment is noted on the task and the close is finished; other
  failures keep the claim. A refused branch delete finishes the close but
  holds the issue, because Issue Bot pushes its next attempt to the same
  branch name without force: the pull request is `closed` with `BranchKept`,
  the issue explains the branch must be deleted by hand, and each inventory
  retries the delete; once it succeeds (or the branch is gone) the issue is
  requeued. `ClosePull`, `Comment` and `DeleteBranch` now classify
  refusals as `RejectedError`, as `CloseIssue` does. The claim skips blocked
  (including off-branch) pull requests, and validation allows a Mayoral
  `declined` at `closing` only on Town's own pull request.

## Simplifier and Mayor decision-path coverage (#142)

- Exercise public Simplifier `Assess`/`Run` and Mayor `Judge`/`WriteBulletin`
  through module-owned fake ACP and gh subprocesses, with local Git origins.
  Cover independent assessment modes, invalid receipts, edited tracked files,
  changed HEAD, cancellation, dry runs, scan scheduling, bulletin windows and
  citations. Git fixtures ignore user/system configuration; tests use private
  state and never dispatch a real coding harness.
- Test complete GitHub pagination and failures on subsequent pages, configuration
  defaults/invalid input, state identity and corruption, and exclusive locks.
  A fake create checks the saved posting intent before returning or losing its
  response. Restart tests retain unknown outcomes and reconcile without another
  POST or agent attempt, including old evidence-free saved proposals.
- Regression tests exposed and fixed Simplifier's rejection of normal gh HTTP
  create responses (LF/CRLF, HTTP/1.1/2.0). Require HTTP 201 and a confirmed issue;
  marker reconciliation now validates the same identity and rejects PR receipts.
  New scan proposals require evidence, while old saved state remains readable.
- No public API, worker protocol, state format, dependency or bot-policy change.
  Mayor's existing decision behavior is unchanged. No arbitrary coverage floor:
  CI already runs every module's full suite, and a floor would reward gaming
  the metric instead of covering decisions.
- Worker wiring is covered at the dispatch boundary, mirroring the issue-bot
  precedent: bsb tests `appendLabels` and the `--socket` requirement plus the
  served bot identity and capabilities; bmb adds an unknown-mode refusal that
  returns an error event without running an agent; brp covers the `--socket`
  requirement and its served identity. Agent-backed dispatch stays in the bot
  package suites, which own the fake harness fixtures.
- The socket-binding tests put the socket under `os.MkdirTemp("", ...)`, like
  every other worker socket test: `t.TempDir()` embeds the test name and
  overflows the 104-byte macOS socket path limit, which only master CI runs.
  Startup failures report the worker's own error instead of a readiness
  timeout.
- Verified with Go 1.27.1: gofmt, `go vet` and the full `go test -race` suites
  pass for Mayor, Repo and Simplifier, including the new socket tests.
- Baseline root-package coverage: Simplifier 40.6%, Mayor 51.7%. Frontend syntax
  and all 74 browser tests pass, as does the isolated demo lifecycle smoke.
  Full ordinary Go tests and vet pass for Town and both affected modules; both
  bot launcher suites and their 29 packaging tests each pass. Root-package
  coverage is now 77.6% for Simplifier and 72.6% for Mayor.
- Fixed a validation-only web test hang by consuming large asset response
  bodies before server cleanup. New bot fixtures prohibit non-local Git
  transports, so even a broken recovery path cannot fetch a live repository.
- Race checks were attempted for Town and both bots with CGO_ENABLED=1, but this
  ARM64 WSL host aborts before tests: ThreadSanitizer unsupported VMA range,
  Found 47 / Supported 48. A compatible Linux/macOS runner is still required.
  Installer tests run from a temporary LF-normalized Linux copy because the
  Windows checkout's CRLF shell scripts cannot execute under /bin/sh.

## Repo Bot default-branch startup error

- A newly added town sends an empty branch to follow the repository default,
  but Repo Bot rejected it before reading GitHub metadata. The browser stayed
  at "Reading repository" with "invalid branch" and could never initialize.
- Allow that unresolved branch in one-shot worker runs; standalone watches
  still require a concrete branch before taking their branch lock. Validate
  GitHub's default against Town's supported branch syntax, then use that
  resolved branch for ancestry state, repair budgets, progress and Git writes.
- Repo dispatch sends the configured branch, not the last observed default,
  so subsequent inventories still discover the current default. Explicit
  branch choices remain pinned; existing branch-state identity checks remain.
- Added a real Repo Bot executable/protocol regression with fake GitHub for
  first inventory, a changed default and an explicit branch. Bot regressions
  cover resolved state reuse, invalid defaults, standalone validation and a
  verified repair to a local Git origin with a fake agent.
- Validation passed: Town and Repo Bot `go test -race ./...` and `go vet ./...`,
  frontend syntax/tests, Repo Bot launcher and all 31 packaging tests, and the
  isolated demo lifecycle smoke. All repository and agent fixtures stayed local.
- User requested a pull request; changes are on fix/repo-default-branch.

## Repository inventory after interrupted startup

- The local fixed Repo Bot already accepted an empty branch, but the saved
  town kept its earlier `invalid branch` error while a later interrupted scan
  held every subsequent inventory. Recovery also appeared as a scheduled wait.
- Resume repository reads automatically from existing recovery records and
  interrupted dispatches. An explicitly inventory-only dispatch creates no
  write hold; older records and runs that could repair retain their uncertainty.
  Recovery inventories cannot start repair agents or perform Town's follow-up
  GitHub writes, and an interrupted read preserves an existing repair record.
- Keep pauses and historical logs. Successful inventory clears the old town
  error; uncertain repairs remain visibly held until explicitly authorized.
  Both browser and CLI show current inventory/recovery ahead of the old error,
  and the browser offers recovery controls between reads.
- Added saved-state restart, cancellation, pause, no-write and repeated-restart
  regressions plus a real Repo Bot protocol case that must not launch an agent.
- Validation passed: full Town `go test -race ./...` and `go vet ./...`, frontend
  syntax/tests, complete bundle build, and isolated demo/worker lifecycle checks.
  All regression GitHub responses and agents were fake; no live state was edited
  and no repository automation or release was started.
- PR review found that merge confirmation could overwrite a finished repair's
  dispatch before scheduled cleanup saved its uncertainty. Repo dispatches now
  save recovery and retire their run under the reconciliation gate; scheduled
  cleanup leaves any newer confirmation dispatch intact.
- A deterministic concurrent regression checks the original repair identity,
  a held replacement repair, blocked follow-up GitHub writes, the next read's
  dispatch lifetime, and durable recovery after restart, using fake workers
  and GitHub only. It fails on the original PR before the fix.
- Fix validation passed: 20 repeated race runs of the interruption regressions,
  full Town race tests and vet, frontend syntax/tests, complete bundle build,
  and isolated demo/worker lifecycle smoke checks.

## Town 0.6.5 release (complete)

- User requested latest master and a new Town release. Fast-forwarded to
  a431b54, containing #160's inventory recovery fix and its concurrency
  regression. No bot sources changed since 0.6.4; all eight pins are retained.
- Master CI passed on Ubuntu and macOS. The local check-only publisher built
  and validated all four native archives and five npm packages at that exact
  commit; the native Town binary reports v0.6.5.
- Published immutable tag v0.6.5-town at a431b54 through GitHub Actions run
  36130844310. All checks and the publishing job passed; GitHub marks it as
  the latest stable release. Added release notes describing the recovery fix.
- Verified each native archive's size and GitHub SHA-256 digest against the
  published manifest and checksum list, including the exact source commit.
  npm accepted all five uploads with provenance, and after processing every
  package reports 0.6.5 as latest; the launcher pins all four platform packages
  to that version. No live repository automation was used for development tests.

## Remaining issue backlog: documentation and execution decisions

- #141: corrected the protocol's persistent eight-worker lifecycle, process-group
  ownership, parent pipe, cancellation and bounded shutdown. Keep the existing
  optional standalone shutdown endpoint; remove Town's unused client helper.
- #143: made the README a navigable quickstart, moved operator reference into
  linked configuration, workflow, operations, funnels and recovery documents,
  corrected all eight agent roles, combined the duplicate Hall entry, and
  explained the still-required legacy max_cycles field without changing policy.
- #142's requested Simplifier/Mayor decision-path and worker-boundary coverage
  is already present on master; no duplicate implementation is needed.
- #151: operator selected a town default with per-bot target overrides and
  Mjolnir-owned execution capacity shown read-only in Town. Recorded inheritance,
  explicit local selection, dispatched identity, mixed local/Mjolnir capacity,
  and dependent #150/#152–155 design constraints in docs/mjolnir-execution.md.
  These settings remain planned, not implemented.
- Validation: root Go race tests and vet, frontend syntax/tests, complete bundle
  build, local documentation link/anchor checks, and isolated demo and all-eight
  worker lifecycle smoke checks passed. No live repository automation was used.
- Next: #5 setup diagnostics and exact merge blockers. Attention hooks (#54),
  retention/incremental sync (#7/#21), Town Guide (#40), and the Mjolnir execution
  work remain separate implementation batches. No release requested for this work.

## Setup diagnostics and exact merge blockers (#5)

- Added authenticated on-demand setup diagnostics through `bt doctor --repo`,
  a browser house control and the shared API. Save timestamped public results
  so browser/CLI status and restart show the same facts. Probe Git/gh lookup,
  GitHub account/repository metadata access, effective per-role commands,
  verification overrides and release preflight setup. Never execute agents or
  verifiers, install runtimes, or write to GitHub. Package/auth readiness that
  cannot be established without starting a harness remains explicitly unknown.
- Bound metadata reads, serialize setup probes, cancel them with the requester,
  and reject results if settings changed or the town was deleted during the
  read. Demo reports simulated setup without touching real services. Private
  arguments, environment, credentials and raw process output stay out of reports.
- Read the merge gate through GitHub GraphQL so older gh versions can supply
  baseRefOid. Include squash policy, merge-queue presence and aggregate check
  state. Preserve exact review/branch/revision checks; unsupported strategy,
  queue, missing policy and conflicting check results cannot authorize a merge.
- Persist actionable blockers for stale reviews, checks, approvals, conflicts,
  behind branches and unavailable/unidentified GitHub requirements, with exact
  observed revisions and PR/check links. Clear corrected or superseded waits;
  a delayed read cannot overwrite a task already moved by fresh inventory.
- Validation: full root Go race suite and vet, frontend syntax/tests, complete
  bundle build, isolated demo lifecycle and all-eight worker shutdown checks
  passed. New fake-GitHub/local-command regressions cover every blocker, zero
  write intents before refusal, recovery after correction/restart, missing
  commands, per-role inheritance, unknown authentication, secret exclusion,
  cancellation, duplicate probes, stale settings and stale revision reads.
  No live repository automation, release or remote Mjolnir job was started.

## PR #161 review corrections

- Reviewed the complete PR and confirmed its queried fields against GitHub's
  read-only GraphQL schema. Found caller deadlines were treated as internal
  probe timeouts and could overwrite the last completed setup report. Preserve
  the previous result on both caller cancellation and expiration, including
  when waiting to commit; internal metadata timeouts still report unknown.
- A retargeted PR can retain its head/base/stage while inventory invalidates
  its audit. Guard delayed diagnostic writes against changed authority and
  review state, retire obsolete diagnostics on retarget/close/merge, and only
  remove a plain-text detail when it belongs to the old report. Browser/CLI
  preserve newer task explanations alongside any historical merge report.
- The final metadata recheck also omitted the target branch name: retargeting
  after the gate passed could create an intent and merge outside the town's
  configured branch. Compare that branch immediately before intent creation.
- Deterministic fake-GitHub regressions reproduced caller-deadline replacement,
  delayed retarget overwrites, completed-task stale blockers and the late
  retarget merge on the prior PR head. Added browser/CLI display checks and
  a separate internal-timeout test so unavailable results remain reportable.
- Validation passed: full root race tests/vet, frontend syntax/tests, complete
  bundle build, demo lifecycle and all-eight worker smoke checks.
  User authorized updating the PR and merging its exact head once green.

## Mjolnir launch catalog and execution selections (#152, first part of #150)

- Consume the daemon's versioned GET /api/v1/options with explicit private service
  connection settings. Background reads have timeout/size bounds, no redirects or
  proxies, and retain a sanitized, connection-scoped cache across daemon failures
  and Town restarts. API/browser/CLI catalog reads use memory only. No daemon
  autostart, real sessions, remote repository work, or live agents in development.
- Save town defaults and independent per-bot execution overrides containing only
  target/profile IDs. Missing overrides inherit; an empty pair explicitly selects
  direct local execution. Agent-profile edits and placement resets stay independent.
- Browser pickers use daemon-reported IDs and show availability reasons, stale
  errors, and missing saved selections. Hide redundant single-choice fields.
  bt execution reads the same catalog and writes through the same selection API.
- Freeze direct-local placement at dispatch and carry execution identity alongside
  exact revisions in interrupted-run recovery. Mjolnir-selected agent work remains
  explicitly held pending #153–155, without retry/budget charges or local fallback;
  Repo Bot inventory continues without a repair agent. No execution-capacity
  integration or successful remote acceptance demonstration is claimed here.
- Validation passed: full root Go race suite and vet, frontend syntax and 81
  browser tests, workflow lint, complete bundle build, demo lifecycle, all-eight
  worker shutdown, and fake-Mjolnir catalog/offline-restart/CLI smoke checks.
  Fake-daemon/API regressions cover inheritance, restart, refusal, cancellation,
  credentials and demo isolation; a real Repo Bot protocol fixture verifies
  managed placement still allows inventory without an agent.
- Pre-PR review fixed CLI receipt decoding/JSON flags, kept inherited agent probes
  independent of bot placement, and preserved existing recovery explanations.
  User authorized opening, reviewing, fixing and merging the PR once green;
  PR #162 contains this implementation and review on feat/mjolnir-launch-options.
  Merge is gated on the reviewed head passing CI; remote execution remains
  follow-up work under #150 and #153–155.
- PR review found execution saves could leave browser controls disabled forever
  if the write or follow-up snapshot refresh stalled. Add a 30-second deadline
  covering both, prevent duplicate submissions, and retain an uncertain-outcome
  message after timeout even if a late response arrives. Both regressions fail
  on the original PR and pass with the fix; frontend syntax and all 83 tests pass.

## Mjolnir profile discovery and execution contract audit (#153–155)

- Managed roles load model/effort choices from the daemon's versioned profile
  config API, with the selected model, instead of preparing/probing a local
  harness. Retain private token handling, no redirects/proxies, cancellation,
  five-second deadline, response bounds and sanitized actionable failures.
- Keep local harness commands and pinned registry definitions independent of
  remote model edits. Honor bot execution overrides during profile preparation;
  inheriting an agent profile must not inherit its placement. Demo stays offline.
- Browser settings show the runtime owner, omit local harness changes for
  managed selections and discard stale choices when execution changes while
  preserving unsaved model drafts. Shared settings/choices APIs serve clients.
- The source contract audit in docs/mjolnir-execution.md identifies missing ACP
  launch selectors, runtime identity and uncertain creation reconciliation;
  chooses reusable bundles/workspaces with isolated Mjolnir-owned checkouts;
  maps existing review/repair/publication checks to required remote evidence.
  Dispatch remains held. #149/#150/#153–155 are not complete, and no real remote
  acceptance run is claimed. #151's operator decisions are already recorded.
- Added `bt choices --repo OWNER/REPO [--role BOT] [--model ID] [--json]`
  through the same profile API; it reads selectors without saving settings.
- Validation: full root Go race suite and vet, frontend syntax/tests, bundle
  build, isolated demo, all-eight worker lifecycle and fake-Mjolnir/offline
  restart smoke checks passed. CLI additions separately passed race tests and
  vet. No live repository automation or real remote acceptance run was used.
- PR #163 review reproduced a mixed-case repository discovery failure with a
  failing regression. Normalize repository IDs in the shared ChoicesForRole
  boundary, matching persisted town identities; browser and CLI both benefit.

## Local attention hook (#54)

- Added private persisted service command/argv and enabled setting, disabled by
  default. Browser service settings, `bt attention-hook`, startup config and the
  authenticated API share the same setting; snapshots expose presence only.
- Capture public attention identity/reason transitions during state commit and
  consume them in a separate bounded watcher. Coalesced Watch signals cannot
  lose short-lived blocked states. Save claims before subprocess launch, retain
  pending notices across restart and never replay interrupted uncertain claims.
- Hooks receive a small JSON line, have a ten-second process-group deadline and
  discarded output. Failures log fixed outcomes without command/error contents
  and leave scheduling state intact. Quiet hours do not hold notifications;
  demo never invokes a hook. Completed delivery records are removed.
- Public service configuration now has an explicit projection, including the
  quiet-hours endpoint, so newly private service fields cannot leak there.
- Tests cover transitions, disabled/demo behavior, duplicate snapshots, short
  transitions, snooze expiry, restart uncertainty, timeout/failure privacy,
  API/CLI validation and browser draft lifecycle. The demo smoke configures a
  marker hook and checks it was never invoked across service restart.
- PR #163 merged after review, a reproduced mixed-case-ID fix and green CI;
  #151 closed. #142's previously implemented decision/worker tests were verified
  by the same all-nine-module CI run and the issue was closed without duplicate
  implementation. Remaining remote execution requires the contracts documented
  in docs/mjolnir-execution.md; no remote acceptance run has been claimed.
- Validation: full root race tests and vet, frontend syntax/tests, complete
  bundle build, demo hook-isolation smoke, all-eight worker lifecycle smoke and
  fake-Mjolnir smoke passed. The first race attempt exhausted the shared /tmp
  filesystem; rerunning with private temporary files on the workspace volume
  passed. No unrelated files were removed. No live automation or release.

## Inspectable local retention (#7)

- Add on-demand shared storage inventory/cleanup APIs, `bt storage` and a browser
  Storage panel. Report logical byte/file/artifact counts by town and role, age,
  task reference and an explicit retention reason; no disk/Git work in rendering.
- Record private transcript completion provenance after worker execution. Require
  a terminal task, age threshold and unchanged SHA-256/size before reclamation.
  Older, unmapped, failed, interrupted or changed evidence remains retained.
- Explicit cleanup accepts opaque inspection IDs, rebuilds the inventory and
  rechecks each removal under a scheduler reservation and cooperative bot locks.
  Refuse active workers, recovery and unresolved writes. Preserve all task,
  ownership, intent and worker-state identities; disable cleanup in demo mode.
- Replace forced orphan repair collection with exact confirmed commit/private
  branch ownership checks and tracked/untracked/ignored cleanliness checks.
  Unknown worktrees and branches remain available for inspection and recovery.
- Defaults, bounded inventory/verification, explicit deletion, uncertain cleanup
  outcomes and consistent private-directory backup/restore are documented.
- Validation passed: full root Go race suite and vet, frontend syntax/tests,
  full bundle build, isolated demo, all-eight worker lifecycle and fake-Mjolnir
  smoke checks. Local-Git/fake-service/browser regressions cover changed evidence,
  dirty/ignored edits, uncertainty, worker locks, stale receipts, cancellation,
  privacy, demo isolation and retained restart identities. An unrelated CLI EOF
  did not reproduce in 20 repeats or the final full run.
- Pre-PR review fixed incomplete transcript discovery being treated as an empty
  baseline and blocked terminal-task reopening during cleanup. No live agents,
  repository automation, release or deployment was used.
- PR #166 self-review added explicit age in hours and corrected cleanup receipts:
  a removed worktree whose branch deletion fails is partial, and an unconfirmed
  removal is uncertain. A local Git ref-lock regression proves the partial case
  retains the saved write identity. Targeted Go race tests, vet and all 89
  frontend tests pass after the correction.
- Integrated concurrent PR #165's independent npm-worker launcher. Re-ran the
  full root race suite/vet, frontend checks/tests, Town-only build, isolated demo,
  all-eight real offline npm package lifecycle checks and fake-Mjolnir smoke;
  all passed. Storage does not invoke or alter bot publication.

## Incremental inventory (#21, first part)

- Add optional v1 `incremental-inventory` capability and timestamp/branch cursor
  inputs to independent Repo Bot. Town omits these fields for older releases.
- Delta reads overlap the last successful scan start by five minutes, refresh
  open issues/PRs and fetch changed closed PRs individually. Fully paginated reads
  only; errors preserve the prior durable cursor. Exact release ancestry remains.
- Persist full-scan age and covered branch, force baseline/daily/branch-change
  resyncs, and preserve closing claims when delta entries are absent. Re-read a
  missing PR before the existing close workflow may write to GitHub.
- Tests use fake GitHub, local worker executables and protocol fixtures for
  pagination errors, identity mismatch, stale/future cursors, restart, branch
  changes and old-worker compatibility. A vanished changed PR triggers one full
  rescan instead of stranding the delta cursor. Validation passed: full root and
  Repo Bot race/vet suites, all 89 frontend tests, Repo Bot launcher/packaging
  checks, Town-only build, isolated demo, all-eight offline npm-worker lifecycle
  and fake-Mjolnir smoke. No live automation or releases.
- Historical task archival remains the second part of #21. This change does not
  close that issue or publish a new independent Repo Bot release.

- Diagnosed a recurring unrelated CLI test EOF/reset: the readiness fixture had
  two os.File owners for one descriptor, so finalization could close a later HTTP
  socket after descriptor reuse. Give the claimed descriptor its own dup, close
  it explicitly, and assert the original pipe remains usable. Fifty combined
  readiness/settings repeats and the final full race suite pass.
- PR #166 merged at 6cae436 after review corrections and exact-head green CI,
  completing #7. Incremental inventory is based on that merged implementation.

## Archived completed task history (#21, second part)

- PR #168 merged at f7901cb after self-review and green checks. Incremental
  inventory remains compatible with older released workers; no bot publication.
- Archive native closed/merged/shipped tasks after 30 days, at most 500 per
  inventory. Keep active, blocked, pending-claim, recovery, follow-up and source
  cursor work hot. Unresolved writes and active non-Repo workers hold archival.
- Save immutable bounded task objects, fsync their directories, then atomically
  save a digest index before removing exact matching hot records. A failed hot
  commit retains tasks; restart tolerates duplicates. Missing identity indexes
  fail startup; damaged objects fail detail/reopen without fresh-task fallback.
- Restore reopened tasks before revision checks and preserve saved decisions;
  suppress historical arrivals and already-shipped commits on full rescans.
  Newly configured GitHub funnels restore any matching archived native issue.
- Browser History and bt history share authenticated bounded page/detail reads.
  Rendering uses counts only, navigation is cancelable, and history is retained
  by storage cleanup. Transcript cleanup still recognizes cold terminal tasks.
- First archival upgrades state format to 2 so older versions cannot forget cold
  identities. Document complete private-directory backup/restore, 30-day policy,
  limits and durable records deliberately kept in the main snapshot.
- Validation passed: full root race tests/vet, frontend syntax and 92 tests,
  Town build, isolated demo, all-eight offline npm-worker lifecycle and fake
  Mjolnir smoke. No live agents, GitHub automation, release or deployment.
- PR #169 self-review reproduced a missing related-task restoration: reopening
  an archived owned PR left its originating issue cold. Restore both before
  reconciliation and retain the issue while the PR is live. A failing regression
  demonstrated the omission; the corrected case joins the full race suite.
- Rebased onto concurrent npm startup fix #167 and merged incremental #168;
  complete root race/vet and offline integration checks passed on that base.

## Conversational Town Guide (#40)

- Add a separate bounded ACP conversation using the town's saved default
  harness/model/effort. Require an advertised read-only/plan mode, expose no
  workspace tools and deny all permissions. Keep worker permission overrides,
  process output and GitHub/Town credential variables out of the Guide path.
- Commit bounded shared conversation state with sequence-based submission
  idempotency, explicit cancellation, restart interruption and monotonic updates.
  Background execution streams sanitized chunks; rendering only consumes state.
- Build context from worker summaries, recent failures, task counts and up to 30
  tasks, with public settings. Preserve unknown/recovery outcomes and exact
  review revision context. Redact private configuration values and token formats.
- Browser Town Hall chat and bt guide use the same authenticated commands and
  conversation. Guide may propose one pause; exact confirmation validates current
  worker state and commits through the existing control transaction. Other
  mutations remain in their established controls.
- Place Town Hall nearer the center and render an idle/waving Guide, window glow,
  contextual dotted paths and static reduced-motion status. No Guide deliveries
  or render-driven writes. Demo uses an internal fake and never calls ACP/GitHub.
- Validation passed: full root Go race tests/vet, fake ACP protocol tests,
  frontend syntax and 93 tests, Town build, demo marker-isolation/reconnect smoke,
  all-eight offline npm worker lifecycle and fake-Mjolnir smoke. The real demo
  smoke exposed its separate simulation loop missing the Guide queue; add a
  fake-only loop there and a startup regression. No live agents, GitHub
  automation or release has been used.
- Integrated merged archival #169 (da32cc5), retained both client controls and
  state validations, and passed full root race/vet, 96 frontend tests, build,
  demo Guide isolation/reconnect, fake-Mjolnir and offline npm lifecycle checks.
- Audited #150's configuration-specific acceptance against merged #162/#163 and
  existing inheritance, picker, frozen dispatch/recovery and demo tests; closed
  #150 with that evidence. Managed execution remains held under #149/#153–155;
  no non-local acceptance run has been claimed.
- PR #171 review added failing regressions for a model/effort change resetting
  the ACP mode and for SSE/HTTP acknowledgement ordering losing or retaining
  browser drafts incorrectly. Select read-only mode immediately before prompting
  and acknowledge a draft by exact submission identity without erasing edits.
- Concurrent #170 changed bot license validation only; integrated it and passed
  Repo, Mayor and Simplifier packaging/license tests with mocked publication.
- After the review fixes, the full root race suite/vet and all 98 frontend tests
  pass. The fake ACP fixture resets mode during model/effort selection, proving
  the final read-only selection precedes every prompt.
