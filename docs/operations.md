# Operations

`bt` is both the service and a small client for it. This page is the operator
reference: deploying Town on Kubernetes, sign-in, the commands, the browser and
the API.

## Deploying on Kubernetes

Town runs on Kubernetes, and every Town is shared: people sign in with GitHub
or Google and work on the same repositories (see [Sign-in](#sign-in)).
`deploy/kubernetes` is a kustomization of the whole deployment:

```sh
deploy/kubernetes/generate-secrets.sh
# fill in credentials.env, auth.env and oauth-secrets.env in deploy/kubernetes,
# and replace town.example.com in bt.yaml and ingress.yaml
kubectl apply -k deploy/kubernetes
kubectl -n brokk-town exec deploy/bt -- bt web     # the operator's link
```

It creates a `brokk-town` namespace (restricted pod security); a Deployment,
Service and PersistentVolumeClaim for `bt` and for each bot; an Ingress that
publishes Town's browser port over HTTPS; network policies that admit only
`bt` to the bots and only the bots to `bt`'s callback port; and these objects,
generated from local files that are never committed:

| File | Object | Holds |
| --- | --- | --- |
| `bot-tokens.env` | Secret `brokk-town-bot-tokens` | One random token per bot, keyed by bot name. Each bot mounts only its own; `bt` mounts them all. |
| `credentials.env` | Secret `brokk-town-credentials` | `GH_TOKEN` and the agent harnesses' API keys, given to every pod as environment. |
| `auth.env` | ConfigMap `brokk-town-auth` | The OAuth client IDs, the allowlist and where the client secrets are mounted. |
| `oauth-secrets.env` | Secret `brokk-town-oauth` | The OAuth client secrets, mounted into `bt` as files under `/run/secrets/brokk-oauth`. |

`generate-secrets.sh` keeps files that already exist, and moves the ones an
earlier release wrote to `deploy/kubernetes/base/` and
`deploy/kubernetes/multi-user/` into place rather than generating new tokens
and credentials that would replace the live ones. The manifests use the
images `ghcr.io/brokkai/brokk-town/<name>:latest`; pin a release with an overlay
that sets `images`, or build them from a checkout with `make images`
(`IMAGES` and `IMAGE_TAG` choose the registry and tag).

Set the Ingress host, its TLS secret (`brokk-town-tls`) and, if the cluster has
several, its ingress class. Town listens on plain HTTP on port 8080 behind it.
`--public-url` in `bt.yaml` is the address people open: it must match the
Ingress host and the OAuth apps' callback URLs, and the browser's `Host` and
`Origin` must match it. For a trial without an Ingress, use
`--public-url http://127.0.0.1:8080` with
`kubectl -n brokk-town port-forward svc/bt 8080`; plain `http://` is accepted
only for `127.0.0.1`, `::1` or `localhost`, since sign-in cookies and the access
key would otherwise cross the network in the clear.

Deployments use the `Recreate` strategy because each pod owns its volume, and
Town holds a single-writer lock on its state: a second Town on the same state
directory fails with the identity of the process that holds it. Pods are given
90 seconds (bots) and 120 seconds (Town) to stop, so running work reports what
it completed.

### Bots

Each of the eight bots runs from its own image in its own Deployment, keeps its
workspaces on its own volume, and accepts only its own token. Town reaches
every bot over the network through the bot contract
([bots.md](bots.md#bot-binaries)); `bt.yaml` names each bot's address and token
file in `BROKK_TOWN_BOT_<ROLE>_URL` and `BROKK_TOWN_BOT_<ROLE>_TOKEN_FILE`, and
the managed-review callback, which Review Bot uses to reach the agent of a
managed review, in `BROKK_TOWN_CALLBACK_URL` (see
[configuration.md](configuration.md#bot-endpoints)). Town does not start unless every
bot has an address and a token.

Before a dispatch Town asks the bot for its identity. A bot that cannot answer
is not sent the work; the dispatch fails with the reason and the bot backs off.
The version it reports is recorded with the run. A bot from an older release
that reports demo mode is refused.

### State directory

Town keeps its state on the `bt-data` volume at `/data`, which the `bt` image
names in `BROKK_TOWN_STATE_DIR`. Elsewhere it is `--state-dir DIR`, or
`$XDG_STATE_HOME/brokk-town` by default.

| Path | Contents |
| --- | --- |
| `state.json` | The committed snapshot. |
| `daemon.lock` | Single-writer lock. |
| `token` | The operator's access key, mode 0600. Delete it to rotate. |
| `session-key` | Signs the sign-in cookies, mode 0600. Delete it to sign everyone out. |
| `connection.json` | URL, PID and version advertised to clients in the pod. |
| `towns/<key>/history/` | Archived terminal tasks (History). |
| `mjolnir-options.json` | Cached Mjolnir catalog. |

Back up the volume to preserve a repository. Do not edit `state.json` by hand;
the service validates it on open and repairs or reports what it can.

## Sign-in

Everyone who signs in works on the same Town: the same repositories, tasks,
decisions, settings and Assistant conversations, updated live in every browser.
Activity names who made each change ("pause bug — by Ada Lovelace (@ada)"), and
the Assistant labels each question with who asked it. There are no private
Towns and no roles: everyone admitted can do what the browser offers, with the
exceptions below.

Sign-in is configured from the environment, which `auth.env` supplies on
Kubernetes. Configure GitHub, Google or both:

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
an OAuth client and at least one allowlist rule, since a Town open to every
GitHub or Google account is never intended. Client secrets are read only from
files, never from the environment, because Town passes its environment to the
commands it starts. For an organization that restricts OAuth app access, an
owner must approve the app before membership can be read.

A sign-in lasts seven days in an HTTP-only, same-site cookie signed with the
`session-key` file in the state directory. Every request re-checks the
allowlist, so removing someone and restarting Town signs them out; organization
membership is checked when a person signs in. Delete `session-key` and restart
to sign everyone out.

A signed-in person cannot name a command for Town's hosts to run: a custom
agent command, its model probe and the attention hook stay with the operator,
who sets them in the config file or with the access key. `bt web` in Town's
pod prints a link that carries it. Choosing a harness from the catalog is open
to everyone.

## Commands

Run the commands in Town's pod, such as
`kubectl -n brokk-town exec deploy/bt -- bt status`, and `bt COMMAND --help`
for the exact flags. The global flag is `--state-dir`; `--listen`,
`--public-url` and `--config` apply to the service.

| Command | Description |
| --- | --- |
| `bt` | Run the service in the foreground; the `bt` image's entrypoint. `--public-url` is required. Town does not start, or apply its config file, unless sign-in is configured and every bot has a URL and a token. |
| `bt status [--json]` | Running state, served repositories, and full snapshot with `--json`. |
| `bt web` | Print the operator's browser link, with the access key. |
| `bt version` | Print the version. |

SIGTERM, which Kubernetes sends to stop a pod, Ctrl+C and SIGHUP stop the
service. `bt status` and `bt version` work while the service is stopped; `bt
web` requires a running service.

The browser and API own everything else: adding and deleting repositories,
starting and pausing bots, retrying or snoozing tasks, decisions (accepting or
declining a task), settings, diagnostics, History, Storage, the Assistant,
requests, harnesses, execution placement and the attention hook.

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

**Global settings** configure an optional command that receives one small
JSON object on stdin when a repository needs attention — a blocked state, a
completed task, an exhausted retry budget. It is disabled by default and runs
in Town's pod for at most ten seconds, independently of quiet hours. Stdout
and stderr are discarded.

The payload contains public identities and a fixed reason code only. Use it to
drive your own notifications or monitoring; do not expect repository content
or error text.

## Browser

People open the public URL and sign in; `bt web` prints the operator's link,
which carries the access key instead. The browser reads the same committed
snapshot and writes only through the authenticated API. It is
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
  from outside and "Operator" for an operator action, which Activity
  attributes to the person who made it.
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

## API

The service serves the UI plus `/api/*`. Every API request must be
same-origin and carry a signed-in browser's session cookie, whose writes must
carry the public URL as their `Origin`, or the operator's access key as
`Authorization: Bearer <token>`, where the token is the contents of the state
directory's `token` file. `GET /api/session` needs no credentials: it lists the
sign-in providers Town offers and who the caller is signed in as. The sign-in
routes are `GET /auth/login/{github|google}`, `GET /auth/callback/{provider}`
and `POST /auth/logout`. Responses are `no-store`.

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

Publish Town only over HTTPS, through the Ingress. The access key carries the
operator's powers; keep it to Town's pod and the operator's own browser.
