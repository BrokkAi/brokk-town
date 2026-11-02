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
| `connection.json` | URL, PID and version advertised to clients. |
| `logs/serve.log`, `logs/serve.err.log` | Background service output. |
| `towns/<key>/<role>/checkout` | A bot's private git checkout. |
| `towns/<key>/<role>/state` | A bot's durable state. |
| `towns/<key>/history/` | Archived terminal tasks (History). |
| `mjolnir-options.json` | Cached Mjolnir catalog. |

Back up the whole directory to preserve a repository. Do not edit `state.json` by
hand; the service validates it on open and repairs or reports what it can.

## Commands

Run `bt COMMAND --help` for the exact flags. Global flags are `--state-dir` and
`--demo`; `--listen` and `--config` apply to the service.

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

`bt web` prints a loopback URL containing the access key. The browser reads the
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
state directory's `token` file, and must be same-origin. Responses are
`no-store`. `POST /api/shutdown` is present only when the service registered a
shutdown hook.

| Route | Purpose |
| --- | --- |
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

The API is private to this machine. Do not expose the listen address to a
network; the token is the only authorization.
