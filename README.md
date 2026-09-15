# Brokk Town

One repository, one little town. Brokk Town runs several repository towns from a
local Go service, with an animated browser village and a compact terminal control
panel. Each town owns its houses, queues, review loops, releases, and history.
Branches and private worktrees belong to their repository's town. Your machine
hosts the towns and shares a configurable pool of agent worker slots between
them (four by default, from one to 64).

The browser shows worker houses, wheelbarrows carrying completed handoffs, trucks
bringing external issues and PRs, and shipments leaving the release depot. Visit
a house to inspect its queue, activity, errors, and controls. Town hall holds
repo-bot's reports. The all-towns overview shows activity and attention counts
without visiting each repository.

[![CI](https://github.com/BrokkAi/brokk-town/actions/workflows/ci.yml/badge.svg)](https://github.com/BrokkAi/brokk-town/actions/workflows/ci.yml)

This is the first local implementation. The browser UI is embedded in the Go
binary; Node.js is only needed for UI development checks and agents that use it.
Linux and macOS are supported. No hosted service is required.

## Try the town

Build with the Go version in `go.mod`:

```sh
make build
./bin/bt tui --demo
```

Any `bt` command starts the town service in the background when it is not
running. Press `q` to leave the terminal panel; the town keeps going. Print the
browser address with:

```sh
./bin/bt web --demo
```

Demo mode uses an isolated state directory and simulated activity in two towns.
It never calls GitHub or launches agents. Orchard cycles through bug and feature discovery,
implementation, review, repair, merge, release, and external arrivals. The initial
board includes blocked, inconclusive, uncertain-write, queued, ready, and shipped
fixtures, with active worker profiles available to inspect. Paper-trail illustrates
a neighboring repository with a failed watchtower. Pause a house to hold its next step.

The browser and TUI attach to the same service. Closing either leaves workers
running. `bt` without a command opens the TUI. Use `bt web` to print the browser
address again; the local access key is persistent, so bookmarks and open tabs
survive restarts.

### The service keeps itself running

There is nothing to install or remember. The first `bt` command that starts a
real town also registers the service with your login session: a launchd agent
on macOS (`~/Library/LaunchAgents/ai.brokk.town.plist`) or a systemd user unit
on Linux (`~/.config/systemd/user/brokk-town.service`). From then on the town
restarts after a crash and returns after a reboot without any command, and the
browser page reloads itself when a new version comes up. The demo town is never
registered; it starts on demand and stays until stopped.

- `bt service status` shows the service, its registration, and log locations
  (`logs/serve.log` and `logs/serve.err.log` under the state directory).
- `bt service stop` stops it until the next `bt` command. `bt service restart`
  restarts it in place.
- `bt service off` keeps the service out of your login session; `bt` still
  starts it on demand, but it will not return by itself after logout or reboot.
  `bt service on` registers it again.
- `bt serve` still runs the service in the foreground for development. It
  refuses to start while another service holds the state directory and names
  that process.

The registration captures the `PATH` of the shell that created it, so `gh`,
`git`, `npx`, and your chosen agent are found without a login shell. A
`--listen` address given to any command is remembered for that town, so a later
command's default never moves a registered service to another port. On Linux,
`bt` enables lingering for your user so the town survives logout; if that needs
administrator approval, it says so. Over SSH on a Mac without a logged-in
session there is no launchd user domain, so the service runs unregistered.

When you rebuild or upgrade `bt`, the next `bt` command notices that the running
service is older and restarts it on the new binary. An older `bt` never
downgrades a running service.

The browser header switches among **Town**, **Board**, and **Compact** without
restarting. Town keeps the animated houses first-class; Board groups durable work
into fixed workflow columns with bounded, independently scrollable task lists;
Compact lists worker activity, profiles, scheduling
eligibility and tasks. Select **All towns** or a repository in the sidebar to
change scope. View and scope survive reloads. Cards and rows open the shared
inspector and controls, retaining repository context across SSE updates. Board
column headings stay visible while scrolling; live updates preserve each column’s
scroll position.

Use **T**, **B**, or **C** to switch views, **0** for all towns, and **1–7** for
houses. The view tabs also support arrow keys, Home and End. Tab/Enter open cards
and controls; Escape closes the inspector. Narrow screens stack operations
content and reduced-motion preferences keep the animated Town usable.

Blocked, failed, inconclusive, uncertain-write and GitHub-waiting states retain
separate labels. Active worker cards do not imply every task at that house is
running. Merged PRs and implemented/closed tasks remain distinct from shipped
commits; only release ancestry confirmed by repository reconciliation can mark
a commit shipped. Columns, prompts and transitions are not programmable.

## Installation and releases

Source is published at [BrokkAi/brokk-town](https://github.com/BrokkAi/brokk-town).
The stable release is available from [GitHub](https://github.com/BrokkAi/brokk-town/releases/tag/v0.1.1)
and npm:

```sh
npm install -g @brokkai/brokk-town
```

While the service is running, Town checks npm outside its input and render loops.
When a newer stable release is available, the browser offers an **Upgrade Town**
button and the TUI offers `u`. After confirmation, the service installs that exact
version through the channel it was installed from (npm for the npm package,
otherwise the checksum-verified release archive over the current binary) and
restarts itself in place; the browser page reloads when the new version is up.
Bots that were mid-run keep working through the restart and are reconnected.
A failed or offline check never interrupts local operation.

You can also build from source as above or install the current branch:

```sh
go install github.com/BrokkAi/brokk-town/cmd/bt@master
```

The release pipeline builds checksum-verified Linux/macOS archives for amd64 and
arm64, plus `@brokkai/brokk-town` and four native npm packages. All packages carry
the project license, attribution, and complete third-party notices. The supported
installer commands are:

```sh
sh install.sh                       # From a checkout; defaults to ~/.local/bin
npm install -g @brokkai/brokk-town   # Installs the bt launcher and native package
```

The GitHub release workflow and npm OIDC workflow follow the bot repositories.
The `packages-publish` environment accepts `v*` tags only. Actual npm trusted publishers are configured and verified for all five packages,
restricted to this repository, `publish-packages.yml`, and `packages-publish`.
Visibility is public and collaborator/developers-team access matches mjolnir. See [RELEASING.md](RELEASING.md)
for first-release setup, access checks, publishing, and recovery.

## Connect real repositories

Install and authenticate `git`, GitHub CLI (`gh`), Node.js/npm, and your chosen
coding agent. Town does not require a separate bot installation: each dispatch
uses `npx --yes` with an exact compatible release of bug-bot, feature-bot,
issue-bot, review-bot, or release-bot, then communicates with it over a private
Unix socket. Town never selects an ambient or floating bot version.
Each bot's Settings panel shows its current pin. **Check for bot update** reads
npm's stable tag, and **Use VERSION** stages that exact version for the Mayor to
save. The pin changes only for that town and takes effect on the bot's next run;
capability and reported-version checks still run before any repository work.

Town also checks npm's stable tags on its own, at start and every six hours.
When a bot has a newer stable release than a town's pin, the town receives a
Mayoral decision at Town Hall: **Upgrade now** pins the new version for the
bot's next run, **Delay a day** asks again after 24 hours, and **Decline** keeps
the current pin until an even newer release is published. The **Update bots
automatically** town setting (off by default) pins new stable releases as they
appear instead, including any offer already waiting. A registry outage never
changes a pin or stops a town.

The worker uses
standard-library HTTP/JSON, negotiates protocol and capabilities before work,
streams contiguous progress events, and returns explicit typed results. It never
requires Town to parse bot-private state. See
[docs/WORKER_PROTOCOL.md](docs/WORKER_PROTOCOL.md) for the contract.

Town defaults to the official ACP registry’s `codex-acp` npm distribution, which
requires Node.js and `npx`. Choose another harness in Settings as described below.
Git must already be able to clone and push your GitHub repositories; Town uses
the current Git/gh credentials.

Town resolves and hashes the `npx` executable for every dispatch and rechecks it
and the pinned bot's reported version afterward. A mid-dispatch executable or
version change fails uncertainly and is resolved through durable bot state and
GitHub reconciliation.

```sh
./bin/bt serve --repo BrokkAi/my-project
# In another terminal:
./bin/bt add --repo BrokkAi/another-project
./bin/bt tui
```

New towns start with the five automation workers **paused**. Repo-bot starts its
read-only inventory. The town header shows whether the town is paused, awake,
or partly awake, and its button offers the action that changes that state.
Inspect the town, then start individual workers or choose **Wake the town**.
Starting workers authorizes their real work: filing issues,
creating and repairing PRs, posting reviews, merging under the configured policy,
and publishing releases. Agents and verification commands run with your local
permissions. Use an isolated account or machine for repositories you don't trust.

```sh
./bin/bt start --repo BrokkAi/my-project --role bug
./bin/bt start --repo BrokkAi/my-project --role feature
./bin/bt pause --repo BrokkAi/my-project --role all
./bin/bt stop --repo BrokkAi/my-project --role issue
./bin/bt status
# Change the global pool; status also reports active and limit capacity.
./bin/bt capacity --max-workers 2
```

Pause finishes active work and stops scheduling more. Stop also cancels active
work. Enabled/paused settings survive restarts. A restart resumes enabled workers;
uncertain external writes retain their saved intent and are reconciled first.

Stopping or restarting the service (Ctrl+C, SIGTERM, `bt service restart`, or
an in-app upgrade) does not stop the external bots. Each bot process runs detached, and Town commits its handle (PID,
socket, and exact task) before requesting work. The next `bt serve` reconnects to
those processes before scheduling anything new. Bots that advertise the `detach`
capability replay the events Town missed and their results are applied normally.
Older bots finish on their own, and Town records that attempt as uncertain rather
than guessing; repo-bot then reconciles whatever landed on GitHub. Only an explicit
Stop, a town deletion, or the two-hour dispatch deadline ends a bot process.

Capacity is a persisted service setting shared by every town. It reserves only
non-reporter bot runs; repo-bot, issue publishing, and prompt-free model choice
discovery stay outside the pool. Lowering the limit lets current work finish and
holds new dispatches until a slot is free. `bt capacity` requires `--max-workers N`,
where `N` is an integer from 1 through 64.

## Town settings, requests, and deletion

Visit a town and choose **Settings**, then use **Configure agent for** to select
the bot and configure its agent, model, and reasoning effort independently. Bug,
feature, issue, review, and release bots can each use a different profile. Bots
without a profile inherit **Town defaults**. Choose **Use town defaults** and save
to remove a bot's independent profile. Save each changed profile before closing
Settings. Repo-bot only reports repository state and does not use an agent.

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
Adding the same repository again restores that history and its previous settings,
with automation paused and the reporter enabled. Wait for stopping workers to
finish before restoring a town.

## How work moves

| House | Work and handoff |
| --- | --- |
| Bug greenhouse | Runs bug-bot's investigation and verification; observed filed issues travel to issue-bot. |
| Feature study | Runs feature-bot to discover useful new capabilities, independently review their value and feasibility, and compare existing requests; confirmed feature issues travel to issue-bot. |
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

New external issues and PRs first wait at Town Hall for a durable Mayoral
decision. **Admit** sends the work to Issue Bot or Review Bot; **Decline** keeps
Town from acting on it without changing GitHub. Feature Bot proposals follow the
same route by default and can be exempted in **Town Settings → External
contributions**. The browser provides the primary decision UX; scripts may use
`bt admit --repo OWNER/REPO --task issue:123` or `bt decline ...`. Offered bot
upgrades use the same commands with `--task upgrade:feature` (or another bot
role), plus `bt delay ...` to be asked again in a day.

A review is bound to the exact base, head, PR description, and discussion snapshot. A suppressed
duplicate comment is still a finding to check. Complete coverage, explicit
resolution of every concern, and validation evidence are required for a clean
result. New commits invalidate review readiness. Uncertain reviews and exhausted
repair cycles become visible tasks needing attention.

The default merge policy is `bot`: auto-merge eligible Town-created PRs. `manual`
leaves merging to the operator; `all` also permits eligible external PRs. Town-managed merges require current clean Town evidence, GitHub mergeability, required checks
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
`merge_policy`, `mayoral_feature_review`, `poll_seconds`, `report_seconds`, and
`max_cycles`. The example
lists all required values. To persist global capacity alongside the town list,
use the object form `{"max_workers": 2, "towns": [...]}`; the legacy array form
remains accepted. When `serve --config` includes `max_workers`, that value
overrides the persisted service setting in the same atomic state update; an
array config without it preserves the saved limit. Town
uses the repository's default branch when omitted; an initialized town's branch
cannot be changed in place. Omitted towns are retained when loading a config.
Agent configuration uses acp-go's `command`, `environment`, `auth_method`, `mode`, `model`, and `effort` fields.

The top-level `harness` and `agent` define town defaults. `bot_agents` maps any of
`bug`, `feature`, `issue`, `review`, and `release` to a complete profile containing
its own `harness` and `agent`, with an optional saved `harness_definition`.
An absent role follows the town defaults. A configured role is independent:
blank model or effort uses its harness default, and omitted agent fields do not
inherit private command, environment, or authentication values from the town.
The example gives review-bot Claude Code, issue-bot Codex, and release-bot OpenCode;
bug-bot and feature-bot inherit the town defaults. Select models and efforts after
authenticating each harness. Verification and scheduling settings remain shared
at town level.

Repo-bot and issue/review scheduling use `poll_seconds` (default 60). Quiet reports
use `report_seconds` (1800). Bug-bot and feature-bot run at most every 30 minutes; release-bot
checks every five minutes and retains its own quiet window, minimum gap, and
batching decisions. Each worker attempt has a two-hour deadline. Repair cycles
default to five; failed PR attempts back off and block after three failures.

Feature-bot uses its own effective ACP harness, model, and effort, plus the town's
optional verifier, with a private workspace and durable publication state.
It proposes scoped features with user value, repository evidence and acceptance
criteria. A separate review rejects duplicate, already implemented, rejected or
uncertain proposals before filing. Its verifier receives `FEATURE_COMMIT` and
`FEATURE_FINDING`; shared verification commands must support the selected bot's
environment. New and upgraded towns keep feature discovery paused until started.

Private state defaults to `$XDG_STATE_HOME/brokk-town` or
`~/.local/state/brokk-town`. `--state-dir` selects another directory; `--demo`
appends `demo`. A single writer lock, atomic snapshots, per-role bot state, and
private worktrees keep towns separate. Events, logs, and reports have bounded
recent histories; task/intent history persists. Browser access uses a per-service
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
Use `bt status` to inspect saved details and GitHub to resolve conflicts.

The HTTP listener accepts loopback IPs only (default `127.0.0.1:8099`). Host,
origin, and bearer key checks protect the local API. A browser supporting WebMCP
can list towns and navigate to a house through optional page tools. Unsupported
browsers use the ordinary interface.

## Keyboard and development

Browser: `0` overview, `1`–`5` agent houses, `6` town hall, `?` help, Escape closes
the inspector. Motion follows reduced-motion preferences and can be switched off.
Terminal: `0` overview, Tab next town, `1`–`5` or `j`/`k` select a house, `s` start,
`p` pause, `x` stop, `a` wake the selected town, `d` delete with `y`/`n` confirmation,
`q` detach. Use `bt settings` and `bt request` for agent settings and new work, or
the forms in the browser. Bracketed paste is
ignored as commands. Network and agent work stay off the input/render loops.

```sh
make check
make smoke
```

Tests use fake GitHub and agents, temporary local Git remotes, authenticated HTTP
fixtures, and the isolated simulation. Do not use live repository automation as a
development test. See [CONTRIBUTING.md](CONTRIBUTING.md),
[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md), and
[licenses/README.md](licenses/README.md).

Licensed under [Apache-2.0](LICENSE). Attribution is in [NOTICE](NOTICE) and
[third-party notices](licenses/THIRD_PARTY_NOTICES.txt). Artwork provenance is in
[docs/ARTWORK.md](docs/ARTWORK.md). Native and npm packaging is configured, and the npm bootstrap and trusted
publishers are established. Stable GitHub release publication remains separate.
