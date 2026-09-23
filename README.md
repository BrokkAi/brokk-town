# Brokk Town

Brokk Town is a local service that coordinates independent repository bots through
private worker processes. Use the browser to watch and control work, or the CLI
for scripts. Everything runs on your machine.

The browser has two themes. The default is the town; Frontline draws every
repository as a base flying one of three armies — humans, humanoid aliens or a
swarm — and every committed delivery as a strike between its installations. The
theme is a look, not a lever: it reads the same snapshot, keeps the theme and
the base's faction in that browser, and never changes what Town does or writes
to GitHub.

## Build and run

```sh
make build
./bin/bt --demo
# In another terminal:
./bin/bt web --demo
```

Bare `bt` runs in the foreground and prints its browser URL. The URL carries the
access key, so it is printed only to a terminal; redirected output and the
background service's log point to `bt web` instead. Ctrl+C, SIGTERM or
SIGHUP stops Town, its bots and their agent processes. Closing the browser does
not stop the service. Demo mode is isolated and never invokes bots, agents or GitHub.

```sh
bt -d                     # explicitly start in the background
bt service status
bt web                    # print the running service's browser URL
bt service stop           # stop Town and its bots
```

Client commands require a running service. There is no login registration,
automatic service replacement, terminal UI, version polling or in-app installer.
`bt serve` is an explicit alias for foreground operation. Use `--state-dir` for
an independent installation and `--listen` for a loopback address.

## Independent bot projects

The `bots/` directories are standalone projects with their own Go modules, CLI
commands, tests, documentation and packaging:

| Project | Command |
|---|---|
| bug-bot | bbb |
| feature-bot | bfb |
| issue-bot | bib |
| review-bot | brv |
| release-bot | brb |
| repo-bot | brp |
| simplifier-bot | bsb |
| mayor-bot | bmb |

Build any bot by running `go build ./cmd/<command>` inside its directory.
Town never imports bot Go packages. Each module chooses its own released acp-go
dependency. The only integration boundary is the local worker protocol.

`bundle.json` records the exact bot versions supported by this Town build and
original source commits. `make build` builds all nine executables into `bin/`.
Town resolves its bots beside its own executable and verifies their versions.
A standalone `go install` of only `bt` is not a complete Town installation.

## Installation and releases

```sh
npm install -g @brokkai/brokk-town
# Or install a published complete native bundle:
sh install.sh vX.Y.Z-town
```

Both installation paths include Town and all eight supported bots. To install a
new version, stop Town, install the complete package, and start Town again.
The shell installer requires Python 3 and keeps each complete installation in
its own directory under `INSTALL_DIR/.brokk-town`, with `bt` pointing to it.

Projects have independent versions and suffix release tags: `vX.Y.Z-town`,
`vX.Y.Z-issue-bot`, and equivalent tags for the other bots. A Town release bundles
the versions in its manifest without publishing unchanged standalone bots.
See [RELEASING.md](RELEASING.md) for workflows and publishing configuration.

## Connect repositories

Authenticate `gh` and your chosen agent first. Start Town, then add a repository:

```sh
bt
# In another terminal:
bt add --repo OWNER/REPO
bt start --repo OWNER/REPO --role issue
bt capacity --max-workers 4
```

Each configured town starts all eight bot processes immediately, including paused
houses. Pausing controls work; it does not remove the process. Agents start only
when jobs are dispatched. Repo Bot begins with read-only inventory; automation
houses start paused. Existing concurrency limits and GitHub authority checks apply.

Deleting a town stops its workers. Ending Town stops every worker. A lost parent
pipe also stops workers after an unexpected Town exit. An interrupted write remains
uncertain until repository reconciliation or an explicit retry establishes what
happened; a restart never blindly repeats it.

## Town settings, requests, and deletion

Visit a town and choose **Settings**, then use **Configure agent for** to select
the bot and configure its agent, model, and reasoning effort independently. Bug,
feature, issue, review, release, simplifier, and repo bots can each use a different profile. Bots
without a profile inherit **Town defaults**. Choose **Use town defaults** and save
to remove a bot's independent profile. Save each changed profile before closing
Settings. Repo Bot inventories the repository without an agent, then uses its
configured profile only when repairing a failing branch. Choose **Repo Bot** in
the profile selector to configure that repair agent.

**Simplifier decisions** selects `suggest` (the default) or `auto`. Suggest keeps
the Mayor as decision maker and shows Simplifier Bot's advice on every pending
arrival. Auto lets the bot's assessment route routine work without a separate
Mayoral decision, including closing low-value complex issues.

### What each bot takes on

Each house can be told what work to accept and how much of it to produce. Every
setting here is one the bot already supports, and a house refuses a setting it
cannot honour rather than accepting it and ignoring it:

| Setting | Flag | Houses |
| --- | --- | --- |
| Required labels | `--labels` | bug, feature, issue, review, simplifier |
| Excluded labels | `--exclude-labels` | issue, review |
| One selected item | `--only` | issue (an issue number), review (a PR number) |
| Discovery focus | `--focus` | bug, feature, review |
| Per-run limit | `--limit` | bug/feature (issues filed), review (findings), simplifier (proposals), repo (repair attempts per revision), hall (bulletin items) |
| Attempts per item | `--attempts` | bug, feature, issue, review, release |
| Verification command | `--verify` | every house; overrides the town's `verify` |
| Release cadence, preflight, required workflows and assets | `--release-*` | release |

```sh
./bin/bt settings --repo BrokkAi/my-project --role issue --labels agent-ready --exclude-labels needs-discussion
./bin/bt settings --repo BrokkAi/my-project --role issue --only 412
./bin/bt settings --repo BrokkAi/my-project --role bug --focus 'the storage layer' --limit 3
./bin/bt settings --repo BrokkAi/my-project --role review --verify '["make","review-check"]'
./bin/bt settings --repo BrokkAi/my-project --role release --release-quiet-seconds 900 --release-burst 10 --release-burst-window-seconds 3600 --release-triage on
./bin/bt settings --repo BrokkAi/my-project --role issue --clear-policy
```

Config-file towns take the same settings under `bot_policies`, keyed by house;
the example lists every supported field. A town that sets none behaves exactly
as it did before these settings existed.

Town applies the same filters to its own queue that it sends to the bot, so the
queue you read is the queue the house will take from. Filtered work is not
hidden: each house's inspector states its policy, counts the inventory it holds
back, and marks each held item, so you can always see what your own filter
excluded. A house whose bundled bot is too old to read a policy is refused the
dispatch with the bot and version named, instead of running unfiltered.

### Agent budgets

A town can bound how much automation it starts in a repeating accounting period:

```sh
./bin/bt settings --repo BrokkAi/my-project --budget-period day --budget-attempts 40 --budget-agent-minutes 600
./bin/bt settings --repo BrokkAi/my-project --budget-period none
```

The period is `day`, `week`, or `month` on your local clock, and at least one of
`--budget-attempts` or `--budget-agent-minutes` is required. Config-file towns
take the same settings as `"budget": {"period": "day", "max_attempts": 40,
"max_agent_minutes": 600}`.

Town charges the budget for every attempt that started an agent. A repository
inventory reads GitHub without one and is never charged; a branch repair is.
When a ceiling is reached, Town stops dispatching new agent work for that town
and says so in Town Hall with the time the period resets. Work already running
finishes normally, cancellation and saved work are untouched, and the repository
inventory keeps running so uncertain writes can still be reconciled. Other towns
are unaffected: a budget is per town, not per worker slot.

**Town cannot cap token or dollar spend.** No bundled agent harness reports usage
back through the worker protocol -- the acp-go runner hands a bot the agent's
final text and nothing else -- so Town would be enforcing a limit against numbers
it never receives. Attempts and agent minutes are what it measures, so those are
what it enforces. Where usage or cost is missing, Town Hall prints "not
reported"; it never shows absent telemetry as zero. Attempts whose elapsed time
never arrived are counted and reported separately rather than billed as free.

For each profile, select any agent from the
[official ACP registry](https://agentclientprotocol.com/get-started/registry),
plus **Anvil**, **Muse ACP**, **Draupnir**, or a custom ACP command. The full
catalog loads from a bundled snapshot or local cache, then refreshes in the
background when stale. **Refresh registry** checks for new agents and versions;
failed refreshes preserve the last usable catalog. Demo stays offline.

Selecting a registry agent saves its version and launch definition with that
profile. Refreshing the catalog does not upgrade saved profiles. To upgrade, choose
**Use registry version** in Settings, or pass the version shown by `bt harnesses`
to `--harness-version`. Active work retains its starting settings.

Town prepares the registry's distribution on first use: npm packages run through
`npx --yes`, Python packages through `uvx`, and native archives download into
Town's private cache. Install Node.js/npm or uv when the selected entry requires
it. Native installs are shared across workers and check a checksum when the
registry supplies one. Agents without a distribution for your platform are
marked unavailable. Provider credentials and login remain the harness's own.

The additional harnesses use the executable installed on the service's PATH:

| Harness | Command | Setup |
| --- | --- | --- |
| [BrokkAi/anvil](https://github.com/BrokkAi/anvil) | `anvil` | `npm install -g @brokkai/anvil`; configure its model provider. |
| [BrokkAi/muse-acp](https://github.com/BrokkAi/muse-acp) | `muse-acp` | Install the adapter and Muse Code; authenticate with `muse login`. |
| [foundev/draupnir](https://github.com/foundev/draupnir) | `draupnir` | Install its release and configure its model provider. |

These three use your installed versions. Their project links and setup notes
also appear in Settings. A custom command supports other local ACP agents.

**Load available choices** prepares and briefly starts the selected bot's harness
without a work prompt, then lists its advertised models and reasoning efforts.
Authenticate it in your terminal first. Choose a model before effort: available
effort levels can depend on it. You can also enter an exact ACP selector value,
or leave either field blank for the harness default. Unsupported selections fail
visibly when the worker starts. Switching harnesses clears the previous harness's
private authentication, command, and mode settings in the selected profile.
Changing town defaults affects bots that inherit them; independent profiles keep
their settings. Changes apply to the next worker run.

```sh
./bin/bt harnesses --refresh
# Without --role, change the town defaults.
./bin/bt settings --repo BrokkAi/my-project --harness codex
# Give each bot its own harness and exact selectors from available choices.
./bin/bt settings --repo BrokkAi/my-project --role review --harness claude --model MODEL_ID --effort EFFORT_ID
./bin/bt settings --repo BrokkAi/my-project --role issue --harness codex --model MODEL_ID --effort EFFORT_ID
./bin/bt settings --repo BrokkAi/my-project --role release --harness opencode --model MODEL_ID
./bin/bt settings --repo BrokkAi/my-project --role bug --harness BrokkAi/anvil
./bin/bt settings --repo BrokkAi/my-project --role feature --harness custom --agent-command '["my-agent", "--acp"]'
# Blank selectors use this profile's harness defaults.
./bin/bt settings --repo BrokkAi/my-project --role review --model '' --effort ''
# Remove the review profile and follow the town defaults again.
./bin/bt settings --repo BrokkAi/my-project --role review --inherit
```

`codex` and `claude` remain aliases for `codex-acp` and `claude-acp`.
The model and effort IDs above are placeholders: use the exact values advertised
by the selected harness. An effort such as `xhigh` is available only when that
harness and model support it.

For an OpenRouter-backed release bot, select **OpenCode** as its harness,
authenticate OpenRouter in OpenCode with `/connect`, then load the release bot's
available choices and select the desired OpenRouter model. OpenCode provides the
ACP agent and OpenRouter supplies its model. See
[OpenCode's OpenRouter setup](https://opencode.ai/docs/providers/#openrouter) and
[ACP support](https://opencode.ai/docs/acp/).

Choose **New request**, select **Feature request** or **Bug report**, and describe
the work. **Create GitHub issue** posts it to that town's repository and places
the confirmed issue in the workshop queue. This works while workers are paused;
start issue-bot when you want implementation to begin. Recent submissions show
the confirmation status and a link to the issue. Demo submissions stay local.

```sh
./bin/bt request --repo BrokkAi/my-project --kind feature --title 'Add keyboard navigation' --body-file request.md
# Use --kind bug for a bug report; --body-file - reads stdin.
./bin/bt check-request --repo BrokkAi/my-project --request-id SAVED_ID
```

Requests have durable IDs, and the CLI prints the ID before sending. If the
connection drops, reuse it with `--request-id` and the same content. The browser
retains the submitted draft in that tab until acknowledgment. An uncertain GitHub
POST is never automatically repeated: Town looks for its hidden receipt in all
open and closed issues every five minutes. **Check GitHub for receipt** requests
an earlier read. Inspect GitHub before manually filing an unconfirmed request again.

To remove a town, choose **Settings → Delete town** and confirm, or run:

```sh
./bin/bt delete --repo BrokkAi/my-project
```

Deletion cancels its workers, cancels queued issue submissions, and removes it
from the browser and terminal. GitHub repositories, issues, and PRs are preserved.
Local history, uncertain writes, and private worktrees remain as recovery records.
Adding the same repository again restores that history and its previous
settings, with automation paused and the reporter enabled. A merge policy or
harness/agent choice given with the add replaces the previous one; budgets, work
policies, bot profiles, funnels and the branch are kept. A deleted town listed in
`bt serve --config` is restored with the file's settings, and one named by
`bt serve --repo` with its previous settings; `bt serve` prints a notice. A town
that already worked on one branch cannot be restored onto another. Deleting a
town that is already deleted reports `unknown town`. Wait for stopping workers to
finish before restoring a town.

## How work moves

| House | Work and handoff |
| --- | --- |
| Bug greenhouse | Runs bug-bot's investigation and verification; observed filed issues travel to issue-bot. |
| Feature study | Runs feature-bot to discover useful new capabilities, independently review their value and feasibility, and compare existing requests; confirmed feature issues travel to issue-bot. |
| Simplifier clarifier | Reviews every incoming issue/PR for disproportionate complexity or low value, advises the Mayor, and discovers removal/replacement proposals. |
| Town Hall (Mayor Bot) | Runs mayor-bot to judge every arrival awaiting a Mayoral decision and to write the town bulletin: the feed of features gained and bugs fixed, written for users. Paused by default; decisions wait for you until it is started. |
| Issue workshop | Runs issue-bot on eligible issues, opens implementation-ready PRs, and repairs Town-owned PR branches from review feedback. |
| Review observatory | Runs review-bot, independently checks the full change and every retained finding, then returns fixes or waits for merge requirements. |
| Release depot | Runs release-bot's batching and publishing policy. Confirmed merged/direct commits accumulate here; a published stable release ships only commits proven to be its ancestors. |
| Repo watchtower | Polls GitHub, reconciles arrivals and revisions, summarizes new commit titles, reports queue growth and blocked work. |
| Town hall | Stores periodic and event-triggered repo-bot reports. |

The first complete inventory establishes existing work without a burst of arrival
animations. Subsequent deliveries follow durable, sequenced state changes.
Animation never initiates a GitHub write. Reconnecting does not replay previously
seen deliveries. External PRs receive reviews but their branches are left to their
authors; only locally recorded Town-created branches enter automatic repair.

Every new issue and PR first waits at Simplifier Bot (including work filed by
Bug Bot and Feature Bot and implementation PRs created by Issue Bot). In the
default **suggest** mode, its bounded assessment is attached to the resulting
durable Mayoral decision: **Admit** sends the work to Issue Bot or Review Bot;
**Decline** keeps Town from acting on it. In **auto** mode, Simplifier Bot can
admit routine work and decline low-value complex work itself; a declined issue is
closed through Repo-bot, while a declined PR is ignored rather than closed.
Simplifier Bot's own marked proposals do not recursively pass through intake.

**Mayor Bot** lives in Town Hall. Start it like any other house and it judges
every arrival that waits for a Mayoral decision: one worker run per arrival,
in a detached worktree at the exact revision, from the town's description of
the item (with Simplifier Bot advice or the Town review attached) and the live
GitHub source. It answers admit, decline, or, for a bot update, delay, with a
reason kept on the task; decisions go through the same path as your own clicks
and are recorded under the Mayor Bot name. A judgment that fails is retried
twice, fifteen minutes apart, then left for you. The same house writes the
**town bulletin**, shown in Town Hall under "What changed": after work merges,
at most every `bulletin_seconds` (default 21600), it summarizes the merged pull
requests for the people who use the software as features, fixes and
improvements, each citing its pull requests.

A review is bound to the exact base, head, PR description, and discussion snapshot. A suppressed
duplicate comment is still a finding to check. Complete coverage, explicit
resolution of every concern, and validation evidence are required for a clean
result. New commits invalidate review readiness.

Every pull request Town works on ends merged or closed, and nothing waits on a
person to press retry. The first review sends every finding back to Issue Bot
for one fix round. If the second review still finds work at or above the town's
`review_close_severity` (default `P2`, so P1 and P2 close), Town closes the pull
request, deletes its branch, leaves the findings on the issue, and queues the
issue for a fresh attempt from the current branch. Findings below the threshold
are filed as follow-up issues and the pull request merges. A revision gets at
most two attempts at any step: a reviewer or repair failure earns one more
attempt after a short delay, and a second failure retires the pull request the
same way. A contributor's pull request is never closed by Town; it goes to the
Mayor instead. Review Bot picks pull requests that have never been tried before
ones that failed, so one bad pull request cannot hold the queue.

The default merge policy is `bot`: auto-merge eligible Town-created PRs. `manual`
leaves every merge to the operator; because the current Release Bot protocol cannot
limit its authority to publish while forbidding its release-preparation PR merges,
Town pauses Release Bot and rejects attempts to start or retry it under this policy.
`all` also permits eligible external PRs. Town-managed merges require current clean Town evidence, GitHub mergeability, required checks
and approvals. Town uses an expected-head squash merge, without admin bypass.
Change this at any time under **Town Settings → External contributions**.
Repositories that require a merge queue or prohibit squash merging need manual
merges for now. GitHub is the final authority at write time.

## Configuration and recovery

Copy [docs/config.example.json](docs/config.example.json), edit the repository,
bot profiles, verification command, and policy, then run:

```sh
./bin/bt serve --config /path/to/towns.json
```

Configuration is a JSON array for town-only files. Each entry supplies `repo`, optional `branch` and
`harness`, `agent`, optional `bot_agents`, optional `verify` argument vector,
`merge_policy`, `simplifier_mode`, optional `review_close_severity` (`P1`, `P2`
or `P3`, default `P2`), optional `budget`, optional `bot_policies`,
`poll_seconds`, `report_seconds`, and `max_cycles`. The example
lists all required values. To persist global capacity alongside the town list,
use the object form `{"max_workers": 2, "towns": [...]}`; the legacy array form
remains accepted. When `serve --config` includes `max_workers`, that value
overrides the persisted service setting in the same atomic state update; an
array config without it preserves the saved limit. Town
uses the repository's default branch when omitted, and keeps following it: the
observed default is recorded as repository state, so renaming it moves the town
with it and never overwrites a branch you chose. An initialized town's branch
cannot be changed in place. A town saved by an older Town has the branch observed
at its first inventory pinned in its configuration; setting `"branch": ""`
explicitly releases that pin so the town follows the repository default again,
while omitting the field keeps whatever the town already uses. Omitted towns are retained when loading a config.
Agent configuration uses acp-go's `command`, `environment`, `auth_method`, `mode`, `model`, and `effort` fields.

The top-level `harness` and `agent` define town defaults. `bot_agents` maps any of
`bug`, `feature`, `issue`, `review`, `release`, and `simplifier` to a complete profile containing
its own `harness` and `agent`, with an optional saved `harness_definition`.
An absent role follows the town defaults. A configured role is independent:
blank model or effort uses its harness default, and omitted agent fields do not
inherit private command, environment, or authentication values from the town.
The example gives review-bot Claude Code, issue-bot Codex, and release-bot OpenCode;
bug-bot and feature-bot inherit the town defaults. Select models and efforts after
authenticating each harness. Verification and scheduling settings remain shared
at town level.

Repo-bot and issue/review scheduling use `poll_seconds` (default 60). Quiet reports
use `report_seconds` (1800). Bug-bot, feature-bot, and simplifier-bot run at most every 30 minutes; mayor-bot
writes a bulletin at most every `bulletin_seconds` (21600) and only after something merged; release-bot
checks every five minutes and retains its own quiet window, minimum gap, and
batching decisions. Each worker attempt has a two-hour deadline. A pull request
gets one fix round and two attempts per revision at any step; `max_cycles` is
accepted for compatibility but no longer extends that.

Feature-bot uses its own effective ACP harness, model, and effort, plus the town's
optional verifier, with a private workspace and durable publication state.
It proposes scoped features with user value, repository evidence and acceptance
criteria. A separate review rejects duplicate, already implemented, rejected or
uncertain proposals before filing. Its verifier receives `FEATURE_COMMIT` and
`FEATURE_FINDING`; shared verification commands must support the selected bot's
environment. New towns keep feature discovery paused until started.

Town Hall's **Automation outcomes** report separates worker attempts, filed
findings, submitted implementation PRs, repository-confirmed merges, repair
rounds, blocked or abandoned work, and verified releases for a selectable
period. A submitted PR is an artifact rather than an accepted fix. Operators
can mark each filed finding useful or a false positive only by adding an
explanation. Records retain task and revision provenance and show elapsed time,
token usage, and cost as unknown when the worker does not provide them. The CSV
export includes the repository on every row for comparisons across runs and
towns.

Private state defaults to `$XDG_STATE_HOME/brokk-town` or
`~/.local/state/brokk-town`. `--state-dir` selects another directory; `--demo`
appends `demo`. A single writer lock, atomic snapshots, per-role bot state, and
private worktrees keep towns separate. Events, logs, and repo-bot reports have
bounded recent histories; task, intent, and automation outcome history persists.
A worker's own log lines and phase changes are committed in batches of at most a
couple of seconds, because every commit rewrites and fsyncs the whole snapshot;
the newest line and phase always win, and everything buffered is committed before
a run's result is.
Browser access uses a per-service
local key in a URL fragment. `connection.json` and state snapshots are mode 0600.
Private agent commands, environment, and authentication configuration are omitted
from public configuration snapshots for both town defaults and bot profiles;
each bot's effective harness, model, and effort are visible.

### Source funnels

Optional `funnels` make issue intake source-neutral. Each named funnel declares a
provider, validated location/filter, a local credential reference, explicit
priority policy, overlap policy, and allowlisted lifecycle mappings. Credential
references are resolved by adapters at request time; secret values never enter
Town state, model prompts, events, logs, or public snapshots. Public state shows
the safe funnel configuration and each normalized item's source identity, URL,
revision, external state, eligibility, capabilities, priority policy, last sync,
and typed outcome.

GitHub funnels support query, `selected_issues`, and comma-separated
`include_labels`/`exclude_labels`. Focused selections are read by issue identity
instead of relying on search indexing. `working` and `blocked` mappings translate
the normalized lifecycle into confirmed label changes. Slack is the second real
adapter: it reads a configured channel through paginated Web API calls and maps
configured lifecycle actions to reactions plus bounded thread replies. Slack
tokens are obtained from a private resolver immediately before each request.
Read-only funnels and empty per-transition mappings stay visibly unsupported.

Funnels also recognize provider-native done signals on inbound reads. A Slack
message with a present `:white_check_mark:` or `:heavy_check_mark:` reaction is
normalized as **Done**; a GitHub issue whose state is `closed` is normalized as
**Closed**. Both remain in the durable inventory with their source provenance,
but become ineligible and leave the worker queue. A GitHub selector must include
closed issues (for example `is:issue`, not `is:issue is:open`) if Town is to
observe that transition. Done is based only on an explicit item state or reaction;
an incomplete or empty inventory never closes missing work by implication.

Lifecycle mutations use a durable intent before the provider call. A lost
response remains `uncertain`; reconciliation is read-only and absence of a
receipt never permits a duplicate label, reaction, comment, or thread reply.
Incomplete reads, partial discovery, authentication failures, rate limits,
unsupported actions, and uncertain writes remain distinct outcomes. Funnel
declaration order is never scheduling priority. Overlapping source identities
are retained, deduplicated, or rejected only according to the explicit overlap
policy. Demo and tests use synthetic or fake providers and perform no live source
or agent automation. See the complete JSON example for GitHub and Slack shapes.
Local logs and worktrees can contain repository content; keep this directory private.

If a push or merge response is lost, Town checks GitHub rather than assuming
success. Inspect the task and its saved intent, then use **Reconcile and retry** or:

```sh
./bin/bt retry --repo BrokkAi/my-project --task pr:123
```

Retry explicitly permits another attempt after fresh checks. For an uncertain
repair push, it reuses and verifies the saved commit; it does not rerun the agent.
If the PR moved, the saved worktree is retained for inspection. A retry also resets
an exhausted repair budget. Never delete an uncertain intent to force progress.
Retry clears that one task's block; it never starts a paused house. If the house
is paused, the task says so and waits for you to start it.

An uncertain repair is settled by ancestry, not only by an equal revision: once
the commit Town pushed is proven to be in the pull request's history, the push
landed, the intent is closed, and the newer revision goes back for review. Town
never republishes the saved commit over work that built on it.

A worktree is kept only while a saved intent still needs it. Repairs that fail
before anything is pushed release their worktree and its `town-repair-*` branch
immediately, and the issue house collects worktrees whose intent has since been
confirmed, so an unattended town does not accumulate them.

When the release house reports that Release Bot's retry budget is exhausted, fix
the reported failure and ask Town to lift the budget:

```sh
./bin/bt retry --repo BrokkAi/my-project --role release
```

The next release run first calls the bot's `POST /v1/retry` worker API, which
resets the pending release's attempt budget in its own workspace, then resumes
the same release. Town never edits the bot's private state. The pinned Release
Bot must advertise the `retry` capability; older pins report that plainly.
Use `bt status` to inspect saved details and GitHub to resolve conflicts.

The HTTP listener accepts loopback IPs only (default `127.0.0.1:8099`). Host,
origin, and bearer key checks protect the local API. A browser supporting WebMCP
can list towns and navigate to a house through optional page tools. Unsupported
browsers use the ordinary interface.


## Development

`make check` runs independent Go race tests and vet in all nine modules, browser
syntax/tests, launcher tests, packaging tests and license checks. `make smoke`
builds the complete suite and runs isolated demo integration checks. Use fake
GitHub and agents for write tests; never use live repository automation as a test.

[Imported open bot issues](docs/imported-bot-issues.md) retain their source links
and attributed discussion history.
