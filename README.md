# Brokk Town

Brokk Town is a shared service that runs a team of repository bots against your
GitHub repositories. It watches what changes, files useful bug and feature
issues, implements work, reviews and certifies pull requests, decides what is
worth doing, keeps the branch it covers healthy, and ships releases. Your team
drives it from a browser, signed in with GitHub or Google, and everyone who
signs in works on the same repositories, decisions and Assistant
conversations.

Town and its eight bots run on Kubernetes, each in its own pod with its own
volume. The bots are Go packages of this module, and each runs as its own
binary that Town reaches over the network (see
[docs/operations.md](docs/operations.md#bots)). One release tag (`vX.Y.Z`)
covers the nine container images, `ghcr.io/brokkai/brokk-town/<name>`, for
`linux/amd64` and `linux/arm64`.

## Requirements

- A Kubernetes cluster with an Ingress controller that terminates TLS and a
  storage class for `ReadWriteOnce` volumes.
- `kubectl`, whose built-in kustomize applies the manifests.
- A GitHub token (`GH_TOKEN`) for the repositories you add.
- A GitHub OAuth app or a Google OAuth client for sign-in.
- The API keys of an ACP agent harness. Town ships an official registry catalog
  and installs or launches the harness you select; `codex-acp` through `npx` is
  a common choice. The images carry `git`, `gh`, `node`/`npx` and `uv`/`uvx`.

## Deploy

```sh
git clone https://github.com/BrokkAi/brokk-town && cd brokk-town
deploy/kubernetes/generate-secrets.sh
# In deploy/kubernetes: set GH_TOKEN and your agent keys in credentials.env,
# the OAuth client and the allowlist in auth.env and oauth-secrets.env, and
# replace town.example.com in bt.yaml and ingress.yaml with your address.
kubectl apply -k deploy/kubernetes
```

People on the allowlist then open your address and sign in. Add, start and
configure repositories in the browser. The operator, who sets the commands
Town runs on its hosts, gets a link carrying the access key from Town's pod:

```sh
kubectl -n brokk-town exec deploy/bt -- bt web
```

See [docs/operations.md](docs/operations.md) for the deployment, sign-in and
the API.

## Bots

Each repository is served by eight bots. Every bot has a lifetime agent
identity, a work policy and a stage in the pipeline. Configuration, the
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

People open Town's address and sign in with GitHub or Google. The browser shows
the same committed state as the API and never writes anything the service did
not record. Activity names who made each change. It has three views of the
same snapshot:

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

`bt` is Town's service and a small client for it. All day-to-day operation —
repositories, bots, tasks, decisions, settings, diagnostics, History and
Storage — happens in the browser.

| Command | Purpose |
| --- | --- |
| `bt` | Run the service; the `bt` image's entrypoint. |
| `bt status` | Show whether Town is running and what it serves (`--json` for state). |
| `bt web` | Print the operator's browser link, with the access key. |
| `bt version` | Print the version. |

Run them in Town's pod, such as `kubectl -n brokk-town exec deploy/bt -- bt
status`. Every command documents its own flags: `bt COMMAND --help`. See
[docs/operations.md](docs/operations.md#commands).

## Configuration

Settings live in Town's state directory, on its volume, not in the repository.
Use the browser to change them. A `--config` file, mounted into Town's pod and
added to its arguments in `bt.yaml`, can seed service settings and repositories
at startup.

The file is either a JSON array of repository configurations or an object with
`max_workers`, `quiet_hours`, `attention_hook`, `bots` and `towns` (the
repositories). See [docs/configuration.md](docs/configuration.md) and
[docs/config.example.json](docs/config.example.json).

## Development

Go 1.27.1 or newer (the version in [go.mod](go.mod)) on Linux, and Node.js for
the browser tests:

```sh
make build     # bin/bt and the eight bot binaries
make check     # race tests, browser tests and vet
make images    # the nine container images
```

Tests host the bots in their own process with fakes, and never reach GitHub
or an agent. To try a change end to end, build the images and deploy them to a
local cluster such as [kind](https://kind.sigs.k8s.io/), as CI does. See
[CONTRIBUTING.md](CONTRIBUTING.md) for the full workflow and
[docs/architecture.md](docs/architecture.md) for how the pieces fit together.

## Releasing

The nightly workflow (`.github/workflows/nightly.yml`) tags the next minor
version when `master` has unreleased commits and CI is green. A tag push also
releases:

```sh
git tag vX.Y.Z && git push origin vX.Y.Z
```

The `Release` workflow (`.github/workflows/release-town.yml`) builds and pushes
the nine images for `linux/amd64` and `linux/arm64`, tagged with the version
and `latest`, then publishes the GitHub release with generated notes and the
third-party license file.

## License

Apache-2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
