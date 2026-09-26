# Configuration

[Back to Brokk Town](../README.md)

- [Agent profiles](#agent-profiles)
- [Harnesses and model choices](#harnesses-and-model-choices)
- [Mjolnir execution selections](mjolnir-execution.md#connect-and-select)
- [Work policies](#work-policies)
- [Agent budgets](#agent-budgets)
- [Quiet hours](#quiet-hours)
- [Configuration file](#configuration-file)
- [Private state and local API](#private-state-and-local-api)

## Agent profiles

Visit a town and choose **Settings**, then use **Configure agent for** to select
the bot and configure its agent, model, and reasoning effort independently. Bug,
feature, issue, review, release, simplifier, repo, and Mayor (`hall`) bots can each
use a different profile. Bots
without a profile inherit **Town defaults**. Choose **Use town defaults** and save
to remove a bot's independent profile. Save each changed profile before closing
Settings. Repo Bot inventories the repository without an agent, then uses its
configured profile only when repairing a failing branch. Choose **Repo Bot** in
the profile selector to configure that repair agent.

**Simplifier decisions** selects `suggest` (the default) or `auto`. Suggest keeps
the Mayor as decision maker and shows Simplifier Bot's advice on every pending
arrival. Auto lets the bot's assessment route routine work without a separate
Mayoral decision, including closing low-value complex issues.

## Harnesses and model choices

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

## Work policies

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
excluded. A house whose bot is too old to read a policy is refused the
dispatch with the bot and version named, instead of running unfiltered.

## Agent budgets

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

## Quiet hours

Quiet hours are weekly windows in which Town starts no new agent work and
makes none of its own GitHub writes: no filing, repairing, reviewing, merging
or releasing. Set a default for every town, give one town its own windows, or
opt one town out:

```sh
# Service default: weeknights and weekends.
./bin/bt settings --quiet-hours "mon-fri 19:00-07:00; weekends 00:00-24:00"
./bin/bt settings --quiet-hours none            # remove the service default
# One town's own windows, none at all, or back to the service default.
./bin/bt settings --repo BrokkAi/my-project --quiet-hours "daily 12:00-13:00"
./bin/bt settings --repo BrokkAi/my-project --quiet-hours none
./bin/bt settings --repo BrokkAi/my-project --quiet-hours default
```

Each window is `DAYS HH:MM-HH:MM`, joined with `;`. Days are `mon` through
`sun`, ranges such as `mon-fri` or `fri-mon`, or `daily`, `weekdays` and
`weekends`, and name the day a window starts. An end at or before the start runs
past midnight into the next day, and `24:00` ends a window at midnight.
Overlapping windows count as one. A window with no day, an unknown day, a time
that is not `HH:MM`, or the same start and end is refused with the reason. The
browser edits the same schedules: the service default under Capacity, and a
town's own in Town settings. Config files take `"quiet_hours": [{"days":
["mon"], "start": "19:00", "end": "07:00"}]` at the top level for the default
and in a town entry for its own, where `[]` opts the town out.

Windows are read on the local clock of the machine running Town, by the clock
on the wall: a window holds through a daylight-saving change, an hour the clock
skips is never quiet, and an hour it repeats is quiet both times.

Inside a window, work already running finishes as it would after Pause, and
the repository inventory keeps running so uncertain writes are still
reconciled. Town leaves closing declined issues and retired pull requests and
filing review follow-ups for the first inventory after the window. Issues you
submit yourself are still posted. Houses stay awake: the browser shows them as
*quiet* rather than paused, the town header reads "Quiet hours · until …",
Town Hall explains the hold, and `bt status --json` carries the same `quiet_hours`
state for each town. When the window ends, scheduling resumes on its own with
each house's usual cadence; nothing missed is replayed. Pause and quiet hours
are independent and both survive a restart: a paused house stays paused after
the window, and a woken one resumes. Release Bot's own release quiet period
(`--release-quiet-seconds`) is unrelated and keeps working as before.

## Configuration file

Copy [config.example.json](config.example.json), edit the repository,
bot profiles, verification command, and policy, then run:

```sh
./bin/bt --config /path/to/towns.json
```

Configuration is one JSON object: optional `max_workers` and `quiet_hours` for
the whole service, and a `towns` array. Each town supplies `repo`, optional `branch` and
`harness`, `agent`, optional `bot_agents`, optional `verify` argument vector,
`merge_policy`, `simplifier_mode`, optional `review_close_severity` (`P1`, `P2`
or `P3`, default `P2`), optional `budget`, optional `bot_policies`,
`poll_seconds`, `report_seconds`, and `max_cycles`. The example
lists all required values. A config-file entry gets no defaults for
`merge_policy`, `poll_seconds`, `report_seconds`, or `max_cycles`: omitting any
of them rejects the file. The defaults quoted below apply to towns added with
`bt add` or the browser. When `--config` includes `max_workers`, that value
overrides the persisted service setting in the same atomic state update; a
config without it preserves the saved limit. Town
uses the repository's default branch when omitted, and keeps following it: the
observed default is recorded as repository state, so renaming it moves the town
with it and never overwrites a branch you chose. An initialized town's branch
cannot be changed in place. A town saved by an older Town has the branch observed
at its first inventory pinned in its configuration; setting `"branch": ""`
explicitly releases that pin so the town follows the repository default again,
while omitting the field keeps whatever the town already uses. Omitted towns are retained when loading a config.
Agent configuration uses acp-go's `command`, `environment`, `auth_method`, `mode`, `model`, and `effort` fields.

The top-level `harness` and `agent` define town defaults. `bot_agents` maps any of
`bug`, `feature`, `issue`, `review`, `release`, `simplifier`, `repo`, or `hall`
to a complete profile containing
its own `harness` and `agent`, with an optional saved `harness_definition`.
An absent role follows the town defaults. A configured role is independent:
blank model or effort uses its harness default, and omitted agent fields do not
inherit private command, environment, or authentication values from the town.
The example gives review-bot Claude Code, issue-bot Codex, and release-bot OpenCode;
bug-bot and feature-bot inherit the town defaults. Select models and efforts after
authenticating each harness. Verification and scheduling settings remain shared
at town level.

Repo-bot and issue/review scheduling use `poll_seconds` (default 60). Quiet reports
use `report_seconds` (1800). Bug-bot and feature-bot discover work at most every 30 minutes; Simplifier
intake uses `poll_seconds`; mayor-bot
writes a bulletin at most every `bulletin_seconds` (21600) and only after something merged; release-bot
checks every five minutes and retains its own quiet window, minimum gap, and
batching decisions. Each worker attempt, including the repo inventory, has a
two-hour deadline, and each GitHub call Town makes itself, such as the merge
gate's read, gives up after one minute. A pull request
gets one fix round and two attempts per revision at any step.

`max_cycles` remains required because the persisted configuration validator
accepts only integers from 1 through 20, including for config-file entries.
Use `5`, the value supplied by `bt add`. It controls when an explicit task retry
resets the saved cycle counter; it does not extend the fixed review/repair limit.

## Private state and local API

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

Local logs and worktrees can contain repository content; keep this directory private.

The HTTP listener accepts loopback IPs only (default `127.0.0.1:8099`). Host,
origin, and bearer key checks protect the local API. A browser supporting WebMCP
can list towns and navigate to a house through optional page tools. Unsupported
browsers use the ordinary interface.

## Local attention hook

Under **Service settings → Local attention hook**, save a command as a JSON
argument array and enable it. The hook is disabled by default. The saved command
stays private: browser/CLI snapshots show only `enabled` and `configured`.
Leaving the command field blank preserves it; disable the hook and enter `[]`
to remove it. Closing the dialog clears any command you typed.

The CLI uses the same service setting. Store the argument array in a private file
(or use `--command-file -` to read it from stdin):

```sh
bt attention-hook                         # show enabled/configured
bt attention-hook --enable --command-file /path/to/hook.json
bt attention-hook --disable                # keep the saved command
bt attention-hook --json
```

A startup config can set `"attention_hook": {"enabled": true, "command":
["/path/to/notify-town"]}` alongside `max_workers` and `towns`. Omit it to keep
the persisted setting. The executable receives one JSON line on standard input:

```json
{"town":"acme/project","task":"issue:23","role":"issue","reason":"blocked","seq":42}
```

Reasons are `blocked`, `failed`, `inconclusive`, `uncertain_write`,
`worker_failed`, or `worker_recovery`; worker task IDs use `worker:ROLE`.
Payloads contain no titles, error text, commands, credentials or agent settings.
Each newly appearing identity/reason queues one invocation. Repeated snapshots
produce no duplicates, and transitions for an identity already queued coalesce.
Enabling the hook queues existing attention once. Snoozed tasks wait until their
snooze expires, except for uncertain writes or inconclusive reviews, which remain
visible attention just as in the inbox.

Delivery runs beside the scheduler with one command at a time, a ten-second
limit and process-group cancellation. The command receives no shell expansion
unless you explicitly configure a shell. Its working directory is Town's private
state directory. Town discards stdout/stderr; redirect output inside your hook
if you need it. Failures produce a short service-log entry and never change task
or worker state. Hooks run independently of quiet hours. Demo never invokes one.

Pending notices survive restart. A durable claim is saved before starting the
command; an interrupted claim has an uncertain delivery outcome and is logged
without replay. Failed deliveries are not retried automatically. A later new
attention transition can notify again. Disabling drops pending notices and lets
an already started command finish within its time limit. Keep `state.json` in
backups to preserve configuration and delivery records.
