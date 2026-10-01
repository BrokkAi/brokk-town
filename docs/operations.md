# Operations

`bt` is both the service and its client. This page is the operator reference:
running the service, the commands, the browser and the local API.

## Running the service

```sh
bt                      # foreground, prints the browser URL
bt -d                   # background service
bt shutdown             # stop it
bt status               # running? which towns? --json for the full snapshot
bt web                  # print the browser URL with its access key
bt --demo               # isolated simulated town, no GitHub or agents
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
| `towns/<key>/<role>/checkout` | A house's private git checkout. |
| `towns/<key>/<role>/state` | A house's durable bot state. |
| `towns/<key>/history/` | Archived terminal tasks. |
| `mjolnir-options.json` | Cached Mjolnir catalog. |

Back up the whole directory to preserve a town. Do not edit `state.json` by
hand; the service validates it on open and repairs or reports what it can.

## Commands

Run `bt COMMAND --help` for the exact flags. Global flags are `--state-dir` and
`--demo`; `--listen` and `--config` apply to the service.

| Command | Description |
| --- | --- |
| `bt` / `bt -d` | Run the service in the foreground or background. |
| `bt status [--json]` | Running state, served towns, and full snapshot with `--json`. |
| `bt web` | Print the browser address. |
| `bt shutdown` | Stop the service and its work. |
| `bt version` | Print the version. |

The browser and local API own everything else: adding and deleting towns,
starting and pausing houses, retrying or snoozing tasks, admitting or declining
Mayoral decisions, settings, diagnostics, history, storage, Town Guide, requests,
harnesses, execution placement and the attention hook.

### Diagnostics

The browser's Diagnostics view runs read-only checks: whether required commands
exist, whether `gh` can see the repository, whether the selected harness starts,
and whether the verification command is usable. It never runs an agent or a
verification command. The report is saved in town state.

### Requests

The browser's request flow files a GitHub issue as work for a town without
needing repository write access yourself. It writes a marker comment so a
resubmission with the same request identity is recognized instead of duplicated.
Pending and uncertain submissions hold storage cleanup.

### Attention hook

The Settings view configures an optional local command that receives one small
JSON object on stdin when a town needs attention — a blocked state, a completed
task, an exhausted retry budget. It is disabled by default, never runs in demo
mode, and runs for at most ten seconds independently of quiet hours. Stdout and
stderr are discarded.

The payload contains public identities and a fixed reason code only. Use it to
drive a desktop notifier or your own monitoring; do not expect repository
content or error text.

## Browser

`bt web` prints a loopback URL containing the access key. The browser reads the
same committed snapshot and writes only through the authenticated API.

Views:

- **Town board** — every house, its state, current task, progress and logs, with
  controls to start, pause, stop, retry and defer.
- **Frontline** — the alternate theme. See [themes.md](themes.md).
- **Town Hall** — the Mayor's bulletin and the Town Guide conversation.
- **History** — archived terminal tasks, with details and reopen.
- **Storage** — the artifact inventory and explicit cleanup.
- **Settings** — service, town and per-house configuration, agent profiles,
  quiet hours, budgets and funnels.
- **Execution** — Mjolnir targets, profiles and runtime selection.
- **Outcomes** — attempt, artifact and outcome records with CSV export and
  operator judgments.
- **Diagnostics** — the saved setup report.

## Town Guide

Town Guide is a bounded ACP conversation that answers questions about the town.
It sees worker summaries, recent failures, task counts and up to thirty tasks,
plus public settings. It cannot read the workspace, run tools, or write
anything. It may propose one pause; the proposal is inert until you confirm its
exact digest against current state.

Town Guide is available in the browser. Answers stream from service state, and a
proposal remains inert until you confirm its exact digest against current state.

## Outcomes

Town records three kinds of outcome: **attempts** (one dispatch), **artifacts**
(an issue, pull request or release it produced) and **outcomes** (a settled
transition). Each record has a status from a fixed vocabulary, an optional
revision and URL, and measured elapsed time. Budget charges come from attempt
records.

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
| `POST /api/control` | Start/pause/stop/retry/delete/admit/decline a town, house or task. |
| `POST /api/guide` | Ask, cancel or confirm a Town Guide turn. |
| `POST /api/towns` | Add a town. |
| `POST /api/settings` | Update service, town or house settings. |
| `POST /api/capacity` | Set `max_workers`. |
| `POST /api/quiet-hours` | Set service or town quiet hours. |
| `POST /api/attention-hook` | Configure the attention hook. |
| `POST /api/storage`, `POST /api/storage/cleanup` | Inventory and cleanup. |
| `POST /api/history` | Archived task pages and details. |
| `POST /api/task-detail` | Bounded detail for one active task. |
| `POST /api/choices` | Model and effort choices for a profile. |
| `GET /api/execution-options`, `POST /api/execution-options/refresh` | Mjolnir catalog. |
| `POST /api/execution`, `POST /api/execution-runtime` | Save placement and pin a runtime. |
| `GET /api/harnesses`, `POST /api/harnesses/refresh` | ACP harness catalog. |
| `POST /api/requests`, `POST /api/requests/check` | Submit or check an issue request. |
| `POST /api/diagnostics` | Run read-only setup diagnostics for a town. |
| `GET /api/outcomes`, `POST /api/outcomes/judgment` | Outcome report and operator judgments. |
| `POST /api/shutdown` | Stop the service. |

The API is private to this machine. Do not expose the listen address to a
network; the token is the only authorization.
