# Brokk Town implementation plan

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
- acp-go v0.8.1 `SetEffort` dropped v0.1.0's fallback order (thought_level
  category, uncategorized `thought_level` ID, reasoning_effort category,
  uncategorized `reasoning_effort` ID); it sees only the thought_level
  category and the `reasoning_effort` ID. A harness using the others would
  lose its effort choices and a saved effort would fail every run.
  `runner.Execute` has no hook between session setup and the prompt, so
  `runAgent` now calls `executeACP` (`internal/town/agent_run.go`), a copy of
  the v0.8.1 runner lifecycle like feature-bot's `agentProcess`, whose
  `setEffort` finds the option in v0.1.0 order and tags a copy so acp-go
  still validates and confirms the value. Keep it aligned with the upstream
  runner on later upgrades.
- The harness choice probe spoke the v0.1.0 hand-written types. It now reads
  generated `schema.SessionConfigOption`s through the same `modelOption` and
  `effortOption` lookups a run uses, flattens grouped values the way acp-go
  does (the first entry decides), and maps them to Town's own `ChoiceValue`,
  so `/api/choices` keeps its `{value, name}` shape. It advertises session
  config options, as a run does, so both see the same selectors.
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
  transcripts, a rejected model as a setup error, cancellation of a prompt in
  flight, and the legacy `thought_level` effort in both the probe and a run.
  The commentary-then-receipt case fails on v0.1.0 and the legacy effort case
  fails with plain acp-go `SetEffort`; both pass here.
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

## Frontline theme (browser, presentation only)

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
- A base's faction derives from its repository name and can be pinned per base
  in `localStorage`; `?skin=frontline` opens the theme from a link. Nothing on
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
  same replacement a live town gets); `serve --repo` restores it with its kept
  config. Both print a notice.
- Deleting a deleted town returns `unknown town` and appends no event.

## CLI polish (#28)

- `bt request` encodes without HTML escaping, so `<`, `>` and `&` no longer
  inflate sixfold and push a valid body past the service's 64 KiB limit. An
  oversized body now gets 413 and "request body exceeds the 64 KiB limit"
  instead of a decoder error.
- The browser link carries the access key, so `serve` and `bt -d` print it only
  when stdout is a terminal. The detached service's log and redirected output
  get "run bt web for the link"; `bt web` still prints the key on request.
- Earlier versions wrote the key into `logs/serve.log`, and the key persists
  across restarts. Opening the service log redacts any `#token=<key>` link in
  place; a log over 8 MiB is truncated instead of read.
- `check-request` on an unknown ID returns "unknown request" (404) rather than
  "does not need reconciliation".
- The stale `connection.json` PID was already handled by `processAlive`.
