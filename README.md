# SlopCop Squad

SlopCop Squad is a local service that puts a squad of repository bots on your
GitHub repositories; each repository under its watch is a precinct. It watches
what changes, files useful bug and feature issues, implements work, reviews and
certifies pull requests, rules on what is worth doing, keeps the branch it
covers healthy, and ships releases. You drive it from a browser, with `sct`
starting and stopping the local service. Everything runs on your machine with
the `gh` and agent credentials you already have.

The eight bots are Go packages of this module. The Squad calls them in process:
there is no separate bot daemon to install, no worker protocol to keep in
version step, and no per-bot release to track. One binary (`sct`) and one
release tag (`vX.Y.Z`) cover the whole project.

## Requirements

- Go 1.27.1 or newer, the version in [go.mod](go.mod), to install with
  `go install`. The release binaries need no toolchain.
- Git.
- [`gh`](https://cli.github.com/) authenticated for the repositories you add.
- An ACP agent harness. The Squad ships an official registry catalog and
  installs or launches the harness you select; `codex-acp` through `npx` is a
  common choice.
- Node.js when the selected harness or Mjolnir is distributed through `npx`.

## Install

Go 1.27.1 or newer:

```sh
go install github.com/BrokkAi/brokk-town/cmd/sct@latest
export PATH="$(go env GOPATH)/bin:$PATH"
```

To pin a specific release:

```sh
go install github.com/BrokkAi/brokk-town/cmd/sct@vX.Y.Z
```

Releases up to v0.10.0 were published as Brokk Town and install `cmd/bt`
instead. See [Upgrading from Brokk Town](docs/operations.md#upgrading-from-brokk-town).

`go install` records the module version in the binary, so `sct version` reports
what you installed. Or build from a checkout:

```sh
make build
export PATH="$PWD/bin:$PATH"
```

### Without Go

Every release also publishes prebuilt archives for Linux, macOS and Windows on
amd64 and arm64 on the [releases page](https://github.com/BrokkAi/brokk-town/releases).
Download the archive for your platform, unpack it, and put `sct` on your `PATH`.
Linux and macOS ship as `.tar.gz`; Windows ships as `.zip`:

```sh
tar -xzf slopcop-squad_vX.Y.Z_linux_amd64.tar.gz
install -m 755 sct "$HOME/.local/bin/sct"
```

Each archive contains `sct`, `LICENSE`, `NOTICE` and
`licenses/THIRD_PARTY_NOTICES.txt`. The release's `checksums.txt` verifies the
download.

## First run

Authenticate `gh` and your chosen agent, then start SlopCop Squad:

```sh
sct                                     # foreground service
sct web                                 # print the browser address
sct status                              # what is running
sct shutdown                            # stop the service
```

Bare `sct` runs in the foreground and prints its browser URL, with the access key.
The server binds loopback only. `sct -d` starts the same service in the background;
`sct shutdown` stops it. Ctrl+C, SIGTERM or SIGHUP stops a foreground service.
Add, start and configure precincts in the browser.

`sct --demo` starts a training exercise: an isolated simulated precinct that
never contacts GitHub, an agent or the network. It is the fastest way to look
around.

## Units

Each precinct is staffed by eight units, one per bot. Every unit has a lifetime
agent identity, a work policy and a place in the case flow. Configuration, the
local API and state still call a unit a `house` and name it by its role key.

| Unit | Callsign | Bot | Role key | Work |
| --- | --- | --- | --- | --- |
| Patrol | PTL | Repo Bot | `repo` | Inventories the repository and repairs the branch it covers. |
| Detectives | DET | Bug Bot | `bug` | Investigates the repository and files new, non-duplicate bug issues. |
| Intel | INT | Feature Bot | `feature` | Researches valuable new capabilities and files concrete proposals. |
| Slop Squad | SLP | Simplifier Bot | `simplifier` | Screens arrivals for disproportionate complexity and low value. |
| Courthouse | CRT | Judge Bot | `hall` | Rules on arrivals awaiting a ruling and keeps the precinct's blotter. |
| Task Force | TSK | Issue Bot | `issue` | Claims issues, implements them, and opens or repairs pull requests. |
| Forensics | LAB | Review Bot | `review` | Examines each exact revision, certifies findings, and posts reviews. |
| Release | REL | Release Bot | `release` | Batches unreleased commits and publishes a verified release. |

Each bot is a package under `bots/` (`bots/repo-bot`, `bots/bug-bot`, …);
Judge Bot is `bots/mayor-bot`. Patrol starts deployed (enabled) so the Squad
can observe the repository. Every other unit starts stood down (paused); deploy
it from the browser. The [glossary](docs/workflow.md#glossary) maps each browser
term to the identifier configuration, the API and state use for it.

## How work moves

Patrol (Repo Bot) performs the repository inventory that becomes cases (tasks)
in the precinct's snapshot; the Squad decides which unit owns each case; the
owning bot runs one focused session; the Squad commits the result, checks
GitHub again, and moves the case on. The Squad also makes its own bounded
GitHub writes — the merge, closing a dismissed issue or pull request, filing
follow-ups, submitting a request — each idempotent and gated on a fresh read.
Nothing is dispatched twice, and an interrupted session leaves a recovery hold
instead of being silently retried.

An issue normally travels intake → screening → Courthouse → Task Force →
Forensics → merge. A pull request normally travels intake → screening →
Courthouse → Forensics → merge. The Squad only merges a revision it
independently certified, targeting the branch it covers, with the checks GitHub
reports green. Follow-ups below the review close threshold are filed as issues
rather than blocking the merge. See [docs/workflow.md](docs/workflow.md).

## Browser

`sct web` prints a loopback URL carrying the access key. The browser shows the
same committed state as the API and never writes anything the service did not
record. It has three views of the same snapshot:

- **Precinct** — the default single-precinct view: the roster of units, the
  case flow lanes from leads to release, and the Radio, the precinct's live
  event log.
- **Board** — units and cases in columns by stage, for one precinct or all of
  them.
- **Compact** — units and cases as a dense list, for one precinct or all of
  them.

From a precinct you can ask the Desk Sergeant about it, file a report, open the
Case archive and the Evidence locker, and change its Precinct settings. Squad
settings hold the service-wide capacity, quiet hours and attention hook, and
Needs you collects the rulings and stuck work from every precinct. A small
cartoon layer — a robot cop on each unit card, a squad car for every transfer,
and the Slop Tank of dismissed cases — only decorates that committed state, and
the Motion toggle stops its animation. The browser is plain HTML, CSS and
JavaScript modules; there are no themes and no image assets. See
[docs/operations.md](docs/operations.md#browser).

## Command line

`sct` starts, stops and locates the local service. All day-to-day operation —
precincts, units, cases, rulings, settings, diagnostics, the Case archive and
the Evidence locker — happens in the browser.

| Command | Purpose |
| --- | --- |
| `sct` | Run the service in the foreground. |
| `sct -d` | Run the service in the background. |
| `sct status` | Show whether the Squad is running and what it serves (`--json` for state). |
| `sct web` | Print the browser address. |
| `sct shutdown` | Stop the service and its work. |
| `sct version` | Print the version. |

Every command documents its own flags: `sct COMMAND --help`. See
[docs/operations.md](docs/operations.md) for the service reference.

## Configuration

Settings live in the Squad's state directory, not in the repository. Use the
browser to change them. A `--config` file can seed service settings and
precincts at startup:

```sh
sct --config config.json
```

The file is either a JSON array of precinct configurations or an object with
`max_workers`, `quiet_hours`, `attention_hook` and `towns` (the precincts). See
[docs/configuration.md](docs/configuration.md) and
[docs/config.example.json](docs/config.example.json).

## Development

```sh
make build     # build bin/sct
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
