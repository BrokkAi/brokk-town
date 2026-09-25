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
