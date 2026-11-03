# Operations

`bt` is both the service and its client. This page is the operator reference:
running the service, the commands, the browser and the local API.

## Running the service

```sh
bt                      # foreground, prints the browser URL
bt -d                   # background service
bt shutdown             # stop it
bt status               # running? which repositories? --json for the full snapshot
bt web                  # print the browser URL with its access key
bt --demo               # demo: a simulated repository, no GitHub or agents
```

`bt` starts the eight bot binaries installed beside it, unless a bot's URL is
configured; see [Bots](#bots).

The service is single-writer per state directory. Starting a second one on the
same directory fails with the identity of the process that holds it. Ctrl+C,
SIGTERM and SIGHUP stop a foreground service; a background service is stopped by
`bt shutdown`, which uses the authenticated local API on Windows where there is
no portable signal.

`bt status` and `bt version` work while the service is stopped. `bt web` and
`bt shutdown` require a running service. Day-to-day operation happens in the
browser.

### State directory

Default `$XDG_STATE_HOME/brokk-town` (`%LOCALAPPDATA%\brokk-town` on Windows), or
`--state-dir DIR`. Demo state lives in a `demo` subdirectory of the same base so
it can never open real state.

| Path | Contents |
| --- | --- |
| `state.json` | The committed snapshot. |
| `daemon.lock` | Single-writer lock. |
| `token` | Local API access key, mode 0600. Delete it to rotate. |
| `session-key` | Signs a shared Town's sign-in cookies, mode 0600. Delete it to sign everyone out. |
| `connection.json` | URL, PID and version advertised to clients. |
| `logs/serve.log`, `logs/serve.err.log` | Background service output. |
| `towns/<key>/<role>/checkout` | A bot's private git checkout, for a bot `bt` starts itself. |
| `towns/<key>/<role>/state` | A bot's durable state, likewise. |
| `towns/<key>/history/` | Archived terminal tasks (History). |
| `mjolnir-options.json` | Cached Mjolnir catalog. |

Back up the whole directory to preserve a repository. Do not edit `state.json` by
hand; the service validates it on open and repairs or reports what it can.

## Bots

Each of the eight bots is its own binary, and Town reaches every one over the
network through the bot contract ([bots.md](bots.md#bot-binaries)). There are
three ways to run them; Town picks per bot, so they can be mixed.

### On your machine

A release archive, `go install github.com/BrokkAi/brokk-town/cmd/...@latest`
and `make build` all put `bt` and the eight bot binaries in one directory.
For every bot without a configured URL, `bt` starts that bot's binary from the
directory `bt` is in (or `BROKK_TOWN_BOT_DIR`):

- on a loopback port the bot chooses, with a random token generated for that
  launch and passed in its environment;
- with Town's state directory as its data root, so its workspaces stay under
  `towns/<key>/<role>/` as before;
- with `bt`'s environment, so `gh`, `git` and the harness use your existing
  credentials.

`bt` restarts a bot that exits, backing off up to 30 seconds, with a new port
and token each time. A dispatch waits up to a minute for a starting bot and
otherwise fails with the reason, which the bot's card shows. When `bt` stops it
lets running bots report what they completed, then closes their standard
input; a bot also exits on its own if `bt` dies. A missing binary is reported
on the bot's card ("cannot find the review-bot binary"). Bot output appears in
`bt`'s log, prefixed by the bot.

### docker compose

`docker-compose.yml` runs `bt` and the eight bots, each in its own container
with its own volume, from the images `ghcr.io/brokkai/brokk-town/<name>`
(`BROKK_TOWN_IMAGES` and `BROKK_TOWN_VERSION` select another registry or tag;
`--build` builds them from the checkout's `Dockerfile`):

```sh
printf 'GH_TOKEN=%s\n' "$(gh auth token)" > brokk.env   # plus your agent API keys
docker compose up -d --build
docker compose exec bt bt web --state-dir /data         # browser link with its access key
```

A one-shot `bot-tokens` service writes a random token per bot to a volume on
first start; each bot mounts only its own, and Town mounts them all. Every
container reads `brokk.env`, when present, for `GH_TOKEN` and agent keys. Town
listens on `127.0.0.1:8080` of the host (`BT_PORT` changes it) and serves the
managed-review callback to the bots at `http://bt:8091`. The demo needs no
credentials:

```sh
docker compose -f docker-compose.yml -f docker-compose.demo.yml up -d --build
```

### Kubernetes

`deploy/kubernetes/base` is a kustomization: a `brokk-town` namespace, one
Deployment, Service and PersistentVolumeClaim per bot and for `bt`, network
policies that admit only `bt` to the bots and only the bots to `bt`'s callback
port, and two Secrets generated from local files:

```sh
deploy/kubernetes/generate-secrets.sh    # random bot tokens; fill in credentials.env
kubectl apply -k deploy/kubernetes/base
kubectl -n brokk-town port-forward svc/bt 8080
kubectl -n brokk-town exec deploy/bt -- bt web --state-dir /data
```

`brokk-town-bot-tokens` holds one token per bot, keyed by bot name; each bot
mounts only its own key, and `bt` mounts them all. `brokk-town-credentials`
holds `GH_TOKEN` and agent keys and is given to every pod as environment.
`deploy/kubernetes/demo` is the same deployment in demo mode. Deployments use
the `Recreate` strategy because each pod owns its volume.

### Running bt in a container

In a container `bt` listens on every interface and is reached through a
published or forwarded port. `--public-url http://127.0.0.1:8080` names the
address the browser uses; it allows a non-loopback `--listen`, and the browser
must use exactly that address. Town also answers at its own loopback port, which
`connection.json` records for `bt status`, `bt web` and `bt shutdown` run inside
the container. `--public-url` cannot be combined with `-d`. Containers are given
90 seconds (bots) and 120 seconds (Town) to stop, so running work reports what
it completed.

### Shared Town

By default Town has one operator: whoever holds the local access key. Started
with `--multi-user`, Town is shared instead. People sign in with GitHub or
Google, and everyone who signs in works on the same Town: the same
repositories, tasks, decisions, settings and Assistant conversations, updated
live in every browser. Activity names who made each change ("pause bug — by Ada
Lovelace (@ada)"), and the Assistant labels each question with who asked it.
There are no private Towns and no roles: everyone admitted can do what the
browser offers, with the exceptions below.

Multi-user mode is for docker compose and Kubernetes, behind a reverse proxy or
Ingress that terminates TLS. `--multi-user` needs `--public-url`, which may be
`https://` here: the address people open, which must also be the OAuth apps'
callback base.

Sign-in is configured from the environment. Configure GitHub, Google or both:

| Variable | Meaning |
| --- | --- |
| `BROKK_TOWN_GITHUB_CLIENT_ID` | A GitHub OAuth app's client ID. Its callback URL is `<public URL>/auth/callback/github`. |
| `BROKK_TOWN_GITHUB_CLIENT_SECRET_FILE` | A file holding that app's client secret. |
| `BROKK_TOWN_GOOGLE_CLIENT_ID` | A Google OAuth web client's ID. Its redirect URI is `<public URL>/auth/callback/google`. |
| `BROKK_TOWN_GOOGLE_CLIENT_SECRET_FILE` | A file holding that client's secret. |
| `BROKK_TOWN_ALLOW_GITHUB_ORGS` | Admit active members of these GitHub organizations. |
| `BROKK_TOWN_ALLOW_GITHUB_USERS` | Admit these GitHub logins. |
| `BROKK_TOWN_ALLOW_GOOGLE_DOMAINS` | Admit Google Workspace accounts of these domains (by the account's verified Workspace domain, not the address's suffix). |
| `BROKK_TOWN_ALLOW_EMAILS` | Admit these verified email addresses, from either provider. |

Lists are comma-separated and case-insensitive. Town refuses to start without
at least one allowlist rule, since a Town open to every GitHub or Google
account is never intended. Client secrets are read only from files, never from
the environment, because Town passes its environment to the commands it
starts. For an organization that restricts OAuth app access, an owner must
approve the app before membership can be read.

A sign-in lasts seven days in an HTTP-only, same-site cookie signed with the
`session-key` file in the state directory. Every request re-checks the
allowlist, so removing someone and restarting Town signs them out; organization
membership is checked when a person signs in. Delete `session-key` and restart
to sign everyone out.

A signed-in person cannot stop Town or name a command for Town's hosts to run:
a custom agent command, its model probe and the attention hook stay with the
operator, who sets them in the config file or with the local access key
(`bt web` inside the container prints a link that carries it). Choosing a
harness from the catalog is open to everyone.

With docker compose, put the client IDs and allowlist in `brokk-auth.env` and
the secrets as files in `./brokk-auth/` (readable by uid 10001), then:

```sh
BT_PUBLIC_URL=https://town.example.com \
  docker compose -f docker-compose.yml -f docker-compose.multi-user.yml up -d --build
```

On Kubernetes, `deploy/kubernetes/generate-secrets.sh` also writes
`deploy/kubernetes/multi-user/auth.env` and `oauth-secrets.env` to fill in.
Replace `town.example.com` in that overlay's `bt-multi-user.yaml` and
`ingress.yaml`, then `kubectl apply -k deploy/kubernetes/multi-user`.

### Checking the bots

In demo mode Town sends every bot a demo dispatch over the network shortly
after it starts and every five minutes. Activity records each bot's first
answer ("Review Bot v1.4.0 answered a demo dispatch over the network"), and
then only changes: a bot that stops answering, with the reason, and its
recovery.
`scripts/demo-dispatch-check.sh` waits for all eight answers from any `bt status
--json`, which is how CI checks the compose and Kubernetes deployments.

For a real dispatch Town first asks the bot for its identity. A bot that cannot
answer is not sent the work; the dispatch fails with the reason and the bot
backs off. The version it reports is recorded with the run.

## Commands

Run `bt COMMAND --help` for the exact flags. Global flags are `--state-dir` and
`--demo`; `--listen`, `--public-url`, `--multi-user` and `--config` apply to
the service.

| Command | Description |
| --- | --- |
| `bt` / `bt -d` | Run the service in the foreground or background. |
| `bt status [--json]` | Running state, served repositories, and full snapshot with `--json`. |
| `bt web` | Print the browser address. |
| `bt shutdown` | Stop the service and its work. |
| `bt version` | Print the version. |

The browser and local API own everything else: adding and deleting
repositories, starting and pausing bots, retrying or snoozing tasks, decisions
(accepting or declining a task), settings, diagnostics, History, Storage, the
Assistant, requests, harnesses, execution placement and the attention hook.

### Diagnostics

The browser's setup diagnostics run read-only checks: whether required commands
exist, whether `gh` can see the repository, whether the selected harness starts,
and whether the verification command is usable. They never run an agent or a
verification command. The report is saved in the repository's state.

### Requests

The browser's **New request** dialog files a GitHub issue as work for a
repository without needing repository write access yourself. It writes a marker
comment so a resubmission with the same request identity is recognized instead
of duplicated. Pending and uncertain submissions hold Storage cleanup.

### Attention hook

**Global settings** configure an optional local command that receives one small
JSON object on stdin when a repository needs attention — a blocked state, a
completed task, an exhausted retry budget. It is disabled by default, never
runs in demo mode, and runs for at most ten seconds independently of quiet
hours. Stdout and stderr are discarded.

The payload contains public identities and a fixed reason code only. Use it to
drive a desktop notifier or your own monitoring; do not expect repository
content or error text.

## Browser

`bt web` prints a loopback URL containing the access key; in a shared Town
people open the public URL and sign in instead. The browser reads the
same committed snapshot and writes only through the authenticated API. It is
plain HTML, CSS and JavaScript modules, with no build step, no animation, no
canvas drawing and no image assets.

### Views

- **Overview** — the default single-repository view. A row of summary metrics
  (bots working, open tasks, awaiting decision, blocked) sits above the
  pipeline: one card per bot in pipeline order, each with its stage, status and
  workload. Select a card to open the bot's details panel with its progress,
  logs and setup diagnostics. Below the pipeline, **Open tasks** lists every
  unsettled task (those needing attention first — blocked, failed, inconclusive
  or an uncertain write — then those awaiting a decision; a snoozed task waits
  its turn), and the **Activity** feed lists recent events: a handoff names
  both bots, such as `External → Triage`, with "External" for a submission
  from outside and "You" for your own action.
- **Board** — tasks in columns by stage (Open, Simplification, Queued, In
  progress, Blocked, Review, Ready, Shipped, Completed), for one repository or
  all of them.
- **Compact** — bots and tasks as a dense list, for one repository or all of
  them.

### Controls

- **Bot controls** — **Start** starts a bot (`start`); **Pause** pauses it
  (`pause`), letting active work finish; **Stop now** stops it (`stop`) and
  cancels active work. A task can be retried or snoozed.
- **Repository controls** — **Start all** starts every bot; Repo Bot is always
  running. When only some bots are running, **Start the rest** starts the
  others; **Pause all** pauses every bot. With `merge_policy` set to `manual`, Release
  Bot stays paused.
- **Repository status** — **Paused**, **Running · N bots**, **Partly running ·
  N of M**, or **Quiet hours · until …**.
- **Status legend** — **Working**, **Idle** (waiting), **Blocked** (blocked or
  failed), **Quiet hours**.

### Dialogs

- **Global settings** — service-wide capacity (`max_workers`), default quiet
  hours and the attention hook.
- **Needs attention** — every repository's work that is waiting on you, longest
  wait first: **Awaiting your decision** and **Blocked**. **Open** goes to the
  task; **Accept** and **Decline** decide it in place.
- **Add repository** — start watching a GitHub repository.
- **Ask the Assistant** — a read-only conversation about the repository; see
  [Assistant](#assistant).
- **New request** — submit an issue request; see [Requests](#requests).
- **History** — archived terminal tasks, with details and reopen.
- **Storage** — the artifact inventory and explicit cleanup.
- **Settings** — repository settings: repository and per-bot configuration,
  agent profiles, quiet hours, budgets and execution placement (Mjolnir
  targets, profiles and runtime selection).

The browser also shows the repository's status report, its scan reports and its
outcomes: attempt, artifact and outcome records with CSV export and operator
judgments. See [Outcomes](#outcomes).

### Keyboard

| Key | Action |
| --- | --- |
| `0` | All repositories. |
| `I` | Needs attention, across every repository. |
| `1`–`8` | Bots in pipeline order: Repo, Bug, Feature, Simplifier, Mayor, Issue, Review, Release. |
| `O`, `B`, `C` | Overview, Board and Compact views. |
| `Esc` | Close a panel. |
| `?` | Keyboard shortcuts. |

## Assistant

The Assistant is a bounded ACP conversation that answers questions about the
repository. It sees worker summaries, recent failures, task counts and up to
thirty tasks, plus public settings. It cannot read the workspace, run tools, or
write anything. It may propose pausing one bot (a `pause`); the proposal is
inert until you confirm its exact digest against current state.

Ask the Assistant from a repository in the browser. Answers stream from
service state. The API route is `/api/guide`.

In a shared Town each repository has one Assistant conversation that everyone
sees, and each question names who asked it. The Assistant answers one question
at a time; a question sent from a page that had not yet seen someone else's is
refused, and the browser asks you to read theirs and send yours again.

## Outcomes

Town records three kinds of outcome, which the browser shows as outcomes: **attempts** (one dispatch), **artifacts** (an issue, pull request or
release it produced) and **outcomes** (a settled transition). Each record has a
status from a fixed vocabulary, an optional revision and URL, and measured
elapsed time. Budget charges come from attempt records.

Agent usage and cost are reported as null when a harness does not report them;
they are never presented as zero. `GET /api/outcomes` returns the report as JSON
and CSV, and `POST /api/outcomes/judgment` records an operator judgment against a
record.

## Local API

The service binds loopback and serves the UI plus `/api/*`. Every API request
must carry `Authorization: Bearer <token>` where the token is the contents of the
state directory's `token` file, and must be same-origin. In a shared Town a
signed-in browser's session cookie works instead, and its writes must carry the
public URL as their `Origin`. `GET /api/session` needs no credentials: it says
whether Town is `local` or `multi-user`, which sign-in providers it offers and
who the caller is signed in as. The sign-in routes are `GET
/auth/login/{github|google}`, `GET /auth/callback/{provider}` and `POST
/auth/logout`. Responses are
`no-store`. `POST /api/shutdown` is present only when the service registered a
shutdown hook.

| Route | Purpose |
| --- | --- |
| `GET /api/session` | How this browser signs in, and as whom. |
| `GET /api/state` | The full public snapshot. |
| `GET /api/events` | Server-sent events of the snapshot stream. |
| `POST /api/control` | `start`/`pause`/`stop`/`retry`/`delete`/`admit`/`decline` a repository (`town`), bot (`house`) or task (`task`). |
| `POST /api/guide` | Ask, cancel or confirm an Assistant turn. |
| `POST /api/towns` | Add a repository. |
| `POST /api/settings` | Update service, repository or bot settings. |
| `POST /api/capacity` | Set `max_workers`. |
| `POST /api/quiet-hours` | Set service or repository quiet hours. |
| `POST /api/attention-hook` | Configure the attention hook. |
| `POST /api/storage`, `POST /api/storage/cleanup` | Storage inventory and cleanup. |
| `POST /api/history` | History pages and details. |
| `POST /api/task-detail` | Bounded detail for one active task. |
| `POST /api/choices` | Model and effort choices for a profile. |
| `GET /api/execution-options`, `POST /api/execution-options/refresh` | Mjolnir catalog. |
| `POST /api/execution`, `POST /api/execution-runtime` | Save placement and pin a runtime. |
| `GET /api/harnesses`, `POST /api/harnesses/refresh` | ACP harness catalog. |
| `POST /api/requests`, `POST /api/requests/check` | Submit or check an issue request. |
| `POST /api/diagnostics` | Run read-only setup diagnostics for a repository. |
| `GET /api/outcomes`, `POST /api/outcomes/judgment` | Outcome report and operator judgments. |
| `POST /api/shutdown` | Stop the service. |

A local Town's API is private to this machine. Do not expose its listen address
to a network; the token is the only authorization. Expose only a shared Town,
and only over HTTPS.
