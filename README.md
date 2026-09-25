# Brokk Town

Brokk Town is a local service that coordinates independent repository bots through
private worker processes. Use the browser to watch and control work, or the CLI
for scripts. Everything runs on your machine.

The browser has two themes. The default is the town; Frontline draws every
repository as a base flying one of three armies — humans, humanoid aliens or a
swarm — and every committed delivery as a strike between its installations. The
theme is a look, not a lever: it reads the same snapshot, keeps the theme and
any race chosen for a base in that browser, and never changes what Town does or writes
to GitHub.

- [Install or build](#install-or-build)
- [First run](#first-run)
- [Houses and work](#houses-and-work)
- [Operator reference](#operator-reference)
- [Independent bot projects](#independent-bot-projects)
- [Development](#development)

## Install or build

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

To build from source, use the Go version declared in [go.mod](go.mod):

```sh
make build
export PATH="$PWD/bin:$PATH"
```

This builds all nine executables into `bin/`. A standalone `go install` of only
`bt` is not a complete Town installation.

## First run

Authenticate `gh` and your chosen ACP agent, then start Town:

```sh
bt
# In another terminal:
bt web
bt add --repo OWNER/REPO
bt start --repo OWNER/REPO --role issue
bt settings --max-workers 4
```

Open the browser to configure the town's agent profiles and admit work. Repo Bot
starts enabled to read the repository; the other houses start paused. All eight
worker processes start when a town is added, but agents start only when jobs are
dispatched. Repo Bot's configured agent is used only for branch repairs.

For an isolated demo that never invokes bots, agents or GitHub:

```sh
bt --demo
# In another terminal:
bt web --demo
```

Bare `bt` runs in the foreground and prints its browser URL. The URL contains the
access key and is printed only to a terminal; use `bt web` when output is
redirected. Ctrl+C, SIGTERM or SIGHUP stops Town, its bots and their agents.
Closing the browser leaves the service running.

```sh
bt -d           # start in the background
bt status       # show whether Town is running and which towns it serves
bt shutdown     # stop Town and its bots
```

Client commands require a running Town and never start one. `bt status` and
`bt harnesses` also work while it is stopped. Use `--state-dir` for separate state
and `--listen` for a loopback address. `bt COMMAND --help` lists a command's flags.
There is no login registration, terminal UI, automatic service replacement,
version polling, or in-app installer.

## Houses and work

| House | Work and handoff |
| --- | --- |
| Bug greenhouse | Runs bug-bot's investigation and verification; observed filed issues travel to issue-bot. |
| Feature study | Runs feature-bot to discover useful new capabilities, independently review their value and feasibility, and compare existing requests; confirmed feature issues travel to issue-bot. |
| Simplifier clarifier | Reviews every incoming issue/PR for disproportionate complexity or low value, advises the Mayor, and discovers removal/replacement proposals. |
| Town Hall (Mayor Bot) | Runs mayor-bot to judge every arrival awaiting a Mayoral decision and to write the town bulletin: the feed of features gained and bugs fixed, written for users. Also displays Repo Bot reports. Paused by default; decisions wait for you until it is started. |
| Issue workshop | Runs issue-bot on eligible issues, opens implementation-ready PRs, and repairs Town-owned PR branches from review feedback. |
| Review observatory | Runs review-bot, independently checks the full change and every retained finding, then returns fixes or waits for merge requirements. |
| Release depot | Runs release-bot's batching and publishing policy. Confirmed merged/direct commits accumulate here; a published stable release ships only commits proven to be its ancestors. |
| Repo watchtower | Polls GitHub, reconciles arrivals and revisions, summarizes new commit titles, reports queue growth and blocked work. |

The browser and CLI use the same committed state. Animation follows recorded
events and never writes to GitHub. Reviews require evidence tied to the exact
revision; zero new comments never proves a clean review. Interrupted writes stay
uncertain until reconciled.

See [How work moves](docs/workflow.md) for intake, Mayor decisions, review and
repair limits, merge authority, release ancestry, and automation outcomes.

## Operator reference

- [Configuration](docs/configuration.md): every bot's agent profile, harnesses,
  model choices, work policies, budgets, quiet hours, and config-file fields.
- [Requests and task controls](docs/operations.md): create work, snooze a task,
  and delete or restore a town.
- [Recovery and saved work](docs/recovery.md): interrupted workers, uncertain
  writes, setup diagnostics with `bt doctor`, merge blockers, and explicit task
  or release retries.
- [Source funnels](docs/funnels.md): GitHub and Slack intake, credentials,
  selectors, and source lifecycle guarantees.
- [Configuration example](docs/config.example.json).
- [Architecture](docs/ARCHITECTURE.md) and [worker protocol](docs/WORKER_PROTOCOL.md).

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

## Development

`make check` runs independent Go race tests and vet in all nine modules, browser
syntax/tests, launcher tests, packaging tests and license checks. `make smoke`
builds the complete suite and runs isolated demo integration checks. Use fake
GitHub and agents for write tests; never use live repository automation as a test.

[Imported open bot issues](docs/imported-bot-issues.md) retain their source links
and attributed discussion history.
