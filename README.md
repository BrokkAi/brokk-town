# Brokk Town

Brokk Town is a local service that runs a town of repository bots against your
GitHub repositories. It watches what changes, files useful bug and feature
issues, implements work, reviews and certifies pull requests, decides what is
worth doing, keeps the branch it covers healthy, and ships releases. You drive
it from a browser, with `bt` starting and stopping the local service. Everything
runs on your machine with the `gh` and agent credentials you already have.

The eight bots are Go packages of this module. Town calls them in process: there
is no separate bot daemon to install, no worker protocol to keep in version
step, and no per-bot release to track. One binary (`bt`) and one release tag
(`vX.Y.Z`) cover the whole project.

## Requirements

- Go 1.27.1 or newer, the version in [go.mod](go.mod), to install with
  `go install`. The release binaries need no toolchain.
- Git.
- [`gh`](https://cli.github.com/) authenticated for the repositories you add.
- An ACP agent harness. Town ships an official registry catalog and installs or
  launches the harness you select; `codex-acp` through `npx` is a common choice.
- Node.js when the selected harness or Mjolnir is distributed through `npx`.

## Install

Go 1.27.1 or newer:

```sh
go install github.com/BrokkAi/brokk-town/cmd/bt@latest
export PATH="$(go env GOPATH)/bin:$PATH"
```

To pin a specific release:

```sh
go install github.com/BrokkAi/brokk-town/cmd/bt@v0.8.1
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
tar -xzf brokk-town_v0.8.1_linux_amd64.tar.gz
install -m 755 bt "$HOME/.local/bin/bt"
```

Each archive contains `bt`, `LICENSE`, `NOTICE` and
`licenses/THIRD_PARTY_NOTICES.txt`. The release's `checksums.txt` verifies the
download.

## First run

Authenticate `gh` and your chosen agent, then start Town:

```sh
bt                                     # foreground service
bt web                                 # print the browser address
bt status                              # what is running
bt shutdown                            # stop the service
```

Bare `bt` runs in the foreground and prints its browser URL, with the access key.
The server binds loopback only. `bt -d` starts the same service in the background;
`bt shutdown` stops it. Ctrl+C, SIGTERM or SIGHUP stops a foreground service.
Add, start and configure towns in the browser.

`bt --demo` starts an isolated simulated town that never contacts GitHub, an
agent or the network. It is the fastest way to look around.

## Houses

Town is organised as houses, one per bot. Every house has a lifetime agent
identity, a work policy and a place in the pipeline.

| House | Bot | Work |
| --- | --- | --- |
| Bug greenhouse | bug-bot | Investigates the repository and files new, non-duplicate bug issues. |
| Feature study | feature-bot | Researches valuable new capabilities and files concrete proposals. |
| Simplifier clarifier | simplifier-bot | Reviews arrivals for disproportionate complexity and low value. |
| Town Hall | mayor-bot | Judges arrivals awaiting a Mayoral decision and writes the town bulletin. |
| Issue workshop | issue-bot | Claims issues, implements them, and opens or repairs pull requests. |
| Review observatory | review-bot | Investigates each exact revision, certifies findings, and posts reviews. |
| Release depot | release-bot | Batches unreleased commits and publishes a verified release. |
| Repo watchtower | repo-bot | Inventories the repository and repairs the branch it covers. |

The repo watchtower starts enabled so Town can observe the repository. Every
other house starts paused; start it from the browser.

## How work moves

Repo Bot performs the repository inventory that becomes tasks in the town
snapshot; Town decides which house owns each task; the owning bot runs one
focused session; Town commits the result, checks GitHub again, and moves the task
on. Town also makes its own bounded GitHub writes — the merge, closing a declined
issue or pull request, filing follow-ups, submitting a request — each idempotent
and gated on a fresh read. Nothing is dispatched twice, and an interrupted
session leaves a recovery hold instead of being silently retried.

An issue normally travels intake → simplification → Hall → issue workshop →
review → merge. A pull request normally travels intake → simplification → Hall →
review → merge. Town only merges a revision it independently certified,
targeting the branch it covers, with the checks GitHub reports green. Follow-ups
below the review close threshold are filed as issues rather than blocking the
merge. See [docs/workflow.md](docs/workflow.md).

## Browser

`bt web` prints a loopback URL carrying the access key. The browser shows the
same committed state as the API and never writes anything the service did not
record. Views include the town board, Town Hall conversation with Town Guide,
outcome history, storage inventory, settings, execution placement and the
service diagnostics.

The board has two themes. **Town** is the default village view. **Frontline**
draws each repository as a base flying one of three armies and each delivery as
a strike between installations. The theme is only a look: it reads the same
snapshot and changes nothing Town does. See [docs/themes.md](docs/themes.md).

## Command line

`bt` starts, stops and locates the local service. All day-to-day operation —
towns, houses, tasks, decisions, settings, diagnostics, history and storage —
happens in the browser.

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

Settings live in Town's state directory, not in the repository. Use the browser
to change them. A `--config` file can seed service settings and towns at startup:

```sh
bt --config config.json
```

The file is either a JSON array of town configurations or an object with
`max_workers`, `quiet_hours`, `attention_hook` and `towns`. See
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

The tag starts the `Release town` workflow, which runs
[GoReleaser](https://goreleaser.com/) to build the archives for Linux, macOS
and Windows, checksum them, and publish a GitHub release.

## License

Apache-2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
