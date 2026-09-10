# Brokk Town

One repository, one little town. Brokk Town runs several repository towns from a
local Go service, with an animated browser village and a compact terminal control
panel. Each town owns its houses, queues, review loops, releases, and history.
Branches and private worktrees belong to their repository's town. Your machine
hosts the towns and shares four agent worker slots between them.

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
./bin/bt serve --demo
```

Open the browser address printed by the service. In another terminal:

```sh
./bin/bt tui --demo
```

Demo mode uses an isolated state directory and simulated activity in two towns.
It never calls GitHub or launches agents. Orchard cycles through bug and feature discovery,
implementation, review, repair, merge, release, and external arrivals. Paper-trail
illustrates a quiet neighboring repository. Pause a house to hold its next step.

The browser and TUI attach to the same service. Closing either leaves workers
running. Stop the foreground service with Ctrl+C; it cancels and waits for workers
before releasing the state lock. `bt` without a command opens the TUI of an
already-running service. Use `bt web` to print its browser address again.

## Installation and releases

Source is published at [BrokkAi/brokk-town](https://github.com/BrokkAi/brokk-town).
The bootstrap prerelease is available on npm:

```sh
npm install -g @brokkai/brokk-town@next
```

You can also build from source as above or install the current branch:

```sh
go install github.com/BrokkAi/brokk-town/cmd/bt@master
```

The release pipeline builds checksum-verified Linux/macOS archives for amd64 and
arm64, plus `@brokkai/brokk-town` and four native npm packages. All packages carry
the project license, attribution, and complete third-party notices. The initial `0.1.0-rc.1` npm bootstrap is published. After the first stable GitHub
release, the supported installer commands are:

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

Install and authenticate `git`, GitHub CLI (`gh`), and your chosen coding agent.
Town defaults to the official ACP registry’s `codex-acp` npm distribution, which
requires Node.js and `npx`. Choose another harness in Settings as described below.
Git must already be able to clone and push your GitHub repositories; Town uses
the current Git/gh credentials.

```sh
./bin/bt serve --repo BrokkAi/my-project
# In another terminal:
./bin/bt add --repo BrokkAi/another-project
./bin/bt tui
```

New towns start with the five automation workers **paused**. Repo-bot starts its
read-only inventory. Inspect the town, then start individual workers or choose
**Wake the town**. Starting workers authorizes their real work: filing issues,
creating and repairing PRs, posting reviews, merging under the configured policy,
and publishing releases. Agents and verification commands run with your local
permissions. Use an isolated account or machine for repositories you don't trust.

```sh
./bin/bt start --repo BrokkAi/my-project --role bug
./bin/bt start --repo BrokkAi/my-project --role feature
./bin/bt pause --repo BrokkAi/my-project --role all
./bin/bt stop --repo BrokkAi/my-project --role issue
./bin/bt status
```

Pause finishes active work and stops scheduling more. Stop also cancels active
work. Enabled/paused settings survive restarts. A restart resumes enabled workers;
uncertain external writes retain their saved intent and are reconciled first.

## Town settings, requests, and deletion

Visit a town and choose **Settings** to select any agent from the
[official ACP registry](https://agentclientprotocol.com/get-started/registry),
plus **Anvil**, **Muse ACP**, **Draupnir**, or a custom ACP command. The full
catalog loads from a bundled snapshot or local cache, then refreshes in the
background when stale. **Refresh registry** checks for new agents and versions;
failed refreshes preserve the last usable catalog. Demo stays offline.

Selecting a registry agent saves its version and launch definition with the town.
Refreshing the catalog does not upgrade existing towns. To upgrade, choose
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

**Load available choices** prepares and briefly starts the selected harness
without a work prompt, then lists its advertised models and reasoning efforts.
Authenticate it in your terminal first. Choose a model before effort: available
effort levels can depend on it. You can also enter an exact ACP selector value,
or leave either field blank for the harness default. Unsupported selections fail
visibly when the worker starts. Switching harnesses clears the previous harness's
private authentication, command, and mode settings. Changes apply to the next
worker run.

```sh
./bin/bt harnesses --refresh
./bin/bt settings --repo BrokkAi/my-project --harness opencode
./bin/bt settings --repo BrokkAi/my-project --harness BrokkAi/anvil
./bin/bt settings --repo BrokkAi/my-project --harness muse-acp
./bin/bt settings --repo BrokkAi/my-project --harness draupnir
./bin/bt settings --repo BrokkAi/my-project --model MODEL_ID --effort EFFORT_ID
./bin/bt settings --repo BrokkAi/my-project --model '' --effort ''
./bin/bt settings --repo BrokkAi/my-project --harness custom --agent-command '["my-agent", "--acp"]'
```

`codex` and `claude` remain aliases for `codex-acp` and `claude-acp`.

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

A review is bound to the exact base, head, PR description, and discussion snapshot. A suppressed
duplicate comment is still a finding to check. Complete coverage, explicit
resolution of every concern, and validation evidence are required for a clean
result. New commits invalidate review readiness. Uncertain reviews and exhausted
repair cycles become visible tasks needing attention.

The default merge policy is `bot`: auto-merge eligible Town-created PRs. `manual`
leaves merging to the operator; `all` also permits eligible external PRs. Town-managed merges require current clean Town evidence, GitHub mergeability, required checks
and approvals. Town uses an expected-head squash merge, without admin bypass.
Repositories that require a merge queue or prohibit squash merging need manual
merges for now. GitHub is the final authority at write time.

## Configuration and recovery

Copy [docs/config.example.json](docs/config.example.json), edit the repository,
agent command, verification command, and policy, then run:

```sh
./bin/bt serve --config /path/to/towns.json
```

Configuration is a JSON array. Each entry supplies `repo`, optional `branch` and `harness`,
`agent`, optional `verify` argument vector, `merge_policy`, `poll_seconds`,
`report_seconds`, and `max_cycles`. The example lists all required values. Town
uses the repository's default branch when omitted; an initialized town's branch
cannot be changed in place. Omitted towns are retained when loading a config.
Agent configuration uses acp-go's `command`, `environment`, `auth_method`, `mode`, `model`, and `effort` fields.

Repo-bot and issue/review scheduling use `poll_seconds` (default 60). Quiet reports
use `report_seconds` (1800). Bug-bot and feature-bot run at most every 30 minutes; release-bot
checks every five minutes and retains its own quiet window, minimum gap, and
batching decisions. Each worker attempt has a two-hour deadline. Repair cycles
default to five; failed PR attempts back off and block after three failures.

Feature-bot uses the same selected ACP harness, model, effort and optional verifier
as the other workers, with its own private workspace and durable publication state.
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
Agent commands/environment values are omitted from public configuration snapshots;
the selected harness, model, and effort are visible.
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
