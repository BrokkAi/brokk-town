# Brokk Town

Brokk Town is a local service that runs a team of repository bots against your
GitHub repositories. It watches what changes, files useful bug and feature
issues, implements work, reviews and certifies pull requests, decides what is
worth doing, keeps the branch it covers healthy, and ships releases. You drive it from a browser, with `bt`
starting and stopping the local service. Everything runs on your machine with
the `gh` and agent credentials you already have.

The eight bots are Go packages of this module. Town calls them in process:
there is no separate bot daemon to install, no worker protocol to keep in
version step, and no per-bot release to track. One binary (`bt`) and one
release tag (`vX.Y.Z`) cover the whole project.

## Requirements

- Go 1.27.1 or newer, the version in [go.mod](go.mod), to install with
  `go install`. The release binaries need no toolchain.
- Git.
- [`gh`](https://cli.github.com/) authenticated for the repositories you add.
- An ACP agent harness. Town ships an official registry catalog and
  installs or launches the harness you select; `codex-acp` through `npx` is a
  common choice.
- Node.js when the selected harness or Mjolnir is distributed through `npx`.

## Install

Go 1.27.1 or newer:

```sh
go install github.com/BrokkAi/brokk-town/cmd/bt@latest
export PATH="$(go env GOPATH)/bin:$PATH"
```

To pin a specific release:

```sh
go install github.com/BrokkAi/brokk-town/cmd/bt@vX.Y.Z
```

`go install` records the module version in the binary, so `bt version` reports
what you installed. Or build from a checkout:

```sh
make build
export PATH="$PWD/bin:$PATH"
```

### Without Go

Every release also publishes prebuilt archives for Linux, macOS and Windows on
amd64 and arm64 on the [releases page](https://github.com/BrokkAi/brokk-town/releases).
Download the archive for your platform, unpack it, and put `bt` on your `PATH`.
Linux and macOS ship as `.tar.gz`; Windows ships as `.zip`:

```sh
tar -xzf brokk-town_vX.Y.Z_linux_amd64.tar.gz
install -m 755 bt "$HOME/.local/bin/bt"
```

Each archive contains `bt`, `LICENSE`, `NOTICE` and
`licenses/THIRD_PARTY_NOTICES.txt`. The release's `checksums.txt` verifies the
download.

## First run

Authenticate `gh` and your chosen agent, then start Brokk Town:

```sh
bt                                     # foreground service
bt web                                 # print the browser address
bt status                              # what is running
bt shutdown                            # stop the service
```

Bare `bt` runs in the foreground and prints its browser URL, with the access key.
The server binds loopback only. `bt -d` starts the same service in the background;
`bt shutdown` stops it. Ctrl+C, SIGTERM or SIGHUP stops a foreground service.
Add, start and configure repositories in the browser.

`bt --demo` starts an isolated simulated repository that never contacts
GitHub, an agent or the network. It is the fastest way to look
around.

## Bots

Each repository is served by eight bots. Every bot has a lifetime agent
identity, a work policy and a stage in the pipeline. Configuration, the local
API and state call a bot's slot a `house` and name it by its role key.

| Bot | Stage | Role key | Work |
| --- | --- | --- | --- |
| Repo Bot | Repository | `repo` | Scans the repository and repairs the branch it covers. |
| Bug Bot | Bug discovery | `bug` | Investigates the repository and files new, non-duplicate bug issues. |
| Feature Bot | Feature discovery | `feature` | Researches valuable new capabilities and files concrete proposals. |
| Simplifier Bot | Simplification | `simplifier` | Weighs each arrival's complexity against its value. |
| Mayor Bot | Triage | `hall` | Accepts or declines each arrival, and writes the repository's status report. |
| Issue Bot | Implementation | `issue` | Implements accepted issues, and opens or repairs the pull request. |
| Review Bot | Review | `review` | Reviews each exact revision, certifies findings, and posts reviews. |
| Release Bot | Release | `release` | Batches unreleased commits and publishes a verified release. |

Each bot is a package under `bots/` (`bots/repo-bot`, `bots/bug-bot`, …). Repo
Bot starts enabled so Town can observe the repository. Every other bot starts
paused; start it from the browser. The [glossary](docs/workflow.md#glossary)
maps each browser term to the identifier configuration, the API and state use
for it.

## How work moves

Repo Bot performs the repository scan that becomes tasks in the repository's
snapshot; Town decides which bot owns each task; the owning bot runs one
focused session; Town commits the result, checks GitHub again, and moves the
task on. Town also makes its own bounded GitHub writes — the merge, closing a
declined issue or pull request, filing follow-ups, submitting a request — each
idempotent and gated on a fresh read. Nothing is dispatched twice, and an
interrupted session leaves a recovery hold instead of being silently retried.

An issue normally travels intake → simplification → triage → implementation →
review → merge. A pull request normally travels intake → simplification →
triage → review → merge. Work declined at simplification or triage is closed
instead. Town only merges a revision it independently certified, targeting the
branch it covers, with the checks GitHub reports green. Follow-ups below the
review close threshold are filed as issues rather than blocking the merge. See
[docs/workflow.md](docs/workflow.md).

## Browser

`bt web` prints a loopback URL carrying the access key. The browser shows the
same committed state as the API and never writes anything the service did not
record. It has three views of the same snapshot:

- **Overview** — the default single-repository view: summary metrics, the
  pipeline of eight bots with each one's status and workload, and the Activity
  feed of recent events. Select a bot to open its details panel.
- **Board** — tasks in columns by stage, for one repository or all of them.
- **Compact** — bots and tasks as a dense list, for one repository or all of
  them.

From a repository you can ask the Assistant about it, submit a new request,
open History and Storage, and change its repository settings. Global settings
hold the service-wide capacity, quiet hours and attention hook, and Needs
attention collects pending decisions and blocked work from every repository.
The browser is plain HTML, CSS and JavaScript modules, with no build step and
no image assets. See [docs/operations.md](docs/operations.md#browser).

## Command line

`bt` starts, stops and locates the local service. All day-to-day operation —
repositories, bots, tasks, decisions, settings, diagnostics, History and
Storage — happens in the browser.

| Command | Purpose |
| --- | --- |
| `bt` | Run the service in the foreground. |
| `bt -d` | Run the service in the background. |
| `bt status` | Show whether Town is running and what it serves (`--json` for state). |
| `bt web` | Print the browser address. |
| `bt shutdown` | Stop the service and its work. |
| `bt version` | Print the version. |

Every command documents its own flags: `bt COMMAND --help`. See
[docs/operations.md](docs/operations.md) for the service reference.

## Configuration

Settings live in Town's state directory, not in the repository. Use the
browser to change them. A `--config` file can seed service settings and
repositories at startup:

```sh
bt --config config.json
```

The file is either a JSON array of repository configurations or an object with
`max_workers`, `quiet_hours`, `attention_hook` and `towns` (the repositories). See
[docs/configuration.md](docs/configuration.md) and
[docs/config.example.json](docs/config.example.json).

## Development

```sh
make build     # build bin/bt
make check     # race tests, browser tests and vet
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for the full workflow and
[docs/architecture.md](docs/architecture.md) for how the pieces fit together.

## Releasing

A release is a tag push:

```sh
git tag vX.Y.Z && git push origin vX.Y.Z
```

The tag starts the `Release` workflow (`.github/workflows/release-town.yml`),
which runs [GoReleaser](https://goreleaser.com/) to build the archives for
Linux, macOS and Windows, checksum them, and publish a GitHub release.

## License

Apache-2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
