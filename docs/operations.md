# Operations

`sct` is both the service and its client. This page is the operator reference:
running the service, the commands, the browser and the local API.

## Running the service

```sh
sct                      # foreground, prints the browser URL
sct -d                   # background service
sct shutdown             # stop it
sct status               # running? which precincts? --json for the full snapshot
sct web                  # print the browser URL with its access key
sct --demo               # training exercise: a simulated precinct, no GitHub or agents
```

The service is single-writer per state directory. Starting a second one on the
same directory fails with the identity of the process that holds it. Ctrl+C,
SIGTERM and SIGHUP stop a foreground service; a background service is stopped by
`sct shutdown`, which uses the authenticated local API on Windows where there is
no portable signal.

`sct status` and `sct version` work while the service is stopped. `sct web` and
`sct shutdown` require a running service. Day-to-day operation happens in the
browser.

### State directory

Default `$XDG_STATE_HOME/slopcop-squad` (`%LOCALAPPDATA%\slopcop-squad` on Windows), or
`--state-dir DIR`. Demo state lives in a `demo` subdirectory of the same base so
it can never open real state.

| Path | Contents |
| --- | --- |
| `state.json` | The committed snapshot. |
| `daemon.lock` | Single-writer lock. |
| `token` | Local API access key, mode 0600. Delete it to rotate. |
| `connection.json` | URL, PID and version advertised to clients. |
| `logs/serve.log`, `logs/serve.err.log` | Background service output. |
| `towns/<key>/<role>/checkout` | A unit's private git checkout. |
| `towns/<key>/<role>/state` | A unit's durable bot state. |
| `towns/<key>/history/` | Archived terminal cases (the Case archive). |
| `mjolnir-options.json` | Cached Mjolnir catalog. |

Back up the whole directory to preserve a precinct. Do not edit `state.json` by
hand; the service validates it on open and repairs or reports what it can.

### Upgrading from Brokk Town

SlopCop Squad was called Brokk Town up to v0.10.0. An install from then keeps
working after the upgrade:

- `sct` replaces `bt`, with the same commands and flags. `sct shutdown` stops a
  service that `bt -d` started, so the upgrade needs no particular order.
- When the default `slopcop-squad` directory does not exist but `brokk-town` does
  in the same place, the Squad keeps using `brokk-town`. To move it, stop the
  service, rename the directory, and start it again. An explicit `--state-dir`
  is unaffected.
- `BROKK_TOWN_MUSE_PERMISSIONS` is still honoured alongside
  `SLOPCOP_SQUAD_MUSE_PERMISSIONS`, and `BT_MJOLNIR_API_URL`,
  `BT_MJOLNIR_TOKEN_FILE` and `BT_MJOLNIR_COMMAND` are still read when their
  `SLOPCOP_SQUAD_MJOLNIR_*` names are unset.
- Issues, comments and Slack replies written before the rename carry
  `<!-- brokk-town… -->` markers. The Squad still recognises them, so it never
  repeats a write it made under the old name; new writes carry
  `<!-- slopcop-squad… -->`.
- Each town you added is now shown as a precinct. The API, configuration and
  state still call it a `town`.
- The browser forgets its remembered view, selection and unsent drafts once, and
  an open tab needs the link from `sct web` again.

## Commands

Run `sct COMMAND --help` for the exact flags. Global flags are `--state-dir` and
`--demo`; `--listen` and `--config` apply to the service.

| Command | Description |
| --- | --- |
| `sct` / `sct -d` | Run the service in the foreground or background. |
| `sct status [--json]` | Running state, served precincts, and full snapshot with `--json`. |
| `sct web` | Print the browser address. |
| `sct shutdown` | Stop the service and its work. |
| `sct version` | Print the version. |

The browser and local API own everything else: adding and deleting precincts,
deploying and standing down units, retrying or snoozing cases, admitting or
dismissing rulings, settings, diagnostics, the Case archive, the Evidence
locker, the Desk Sergeant, requests, harnesses, execution placement and the
attention hook.

### Diagnostics

The browser's setup diagnostics run read-only checks: whether required commands
exist, whether `gh` can see the repository, whether the selected harness starts,
and whether the verification command is usable. They never run an agent or a
verification command. The report is saved in the precinct's state.

### Requests

The browser's **＋ File a report** dialog files a GitHub issue as work for a
precinct without needing repository write access yourself. It writes a marker
comment so a resubmission with the same request identity is recognized instead
of duplicated. Pending and uncertain submissions hold Evidence locker cleanup.

### Attention hook

**Squad settings** configure an optional local command that receives one small
JSON object on stdin when a precinct needs attention — a blocked state, a
completed case, an exhausted retry budget. It is disabled by default, never
runs in demo mode, and runs for at most ten seconds independently of quiet
hours. Stdout and stderr are discarded.

The payload contains public identities and a fixed reason code only. Use it to
drive a desktop notifier or your own monitoring; do not expect repository
content or error text.

## Browser

`sct web` prints a loopback URL containing the access key. The browser reads the
same committed snapshot and writes only through the authenticated API. It is
plain HTML, CSS and JavaScript modules: there are no themes, no canvas drawing
and no image assets; the cartoon layer is inline SVG and CSS.

### Views

- **Precinct** — the default single-precinct view. The roster lists every unit
  in roster order with its state, current case and caseload; selecting a unit
  shows its progress, logs and setup diagnostics. The case flow lays the
  precinct's open cases into lanes from leads through screening, court, Task
  Force and Forensics to release, with the Slop Tank below it. The **Radio** is
  the precinct's live event log: a transfer names both ends by callsign, with
  CIV for a civilian report and CPT, the captain, for you.
- **Board** — units and cases in columns by stage, for one precinct or all of
  them.
- **Compact** — units and cases as a dense list, for one precinct or all of
  them.

### Controls

- **Unit controls** — **▶ Deploy** starts a unit (`start`); **Ⅱ Stand down**
  pauses it (`pause`), letting active work finish; **■ Stop now** stops it
  (`stop`) and cancels active work. A case can be retried or snoozed.
- **Precinct controls** — **▶ Start patrol** deploys every unit; Patrol is
  always deployed. When only some units are deployed, **▶ Deploy the rest**
  deploys the others; **Ⅱ Stand down** stands every unit down. With
  `merge_policy` set to `manual`, Release stays stood down.
- **Precinct status** — **Off duty**, **On patrol · N units**, **Partly
  deployed · N of M**, or **Quiet hours · until …**.
- **Unit legend** — **On a case** (working), **Standing by** (waiting), **Stuck**
  (blocked or failed), **Quiet hours**.

### Dialogs

- **Squad settings** — service-wide capacity (`max_workers`), default quiet
  hours and the attention hook.
- **Needs you** — every precinct's work that is waiting on you, longest wait
  first: **Awaiting your ruling** and **Stuck**. **Open in Courthouse**
  goes to the case.
- **＋ New precinct** — put a repository under watch as a new precinct.
- **Ask the Desk Sergeant** — a read-only conversation about the precinct; see
  [Desk Sergeant](#desk-sergeant).
- **＋ File a report** — submit an issue request; see [Requests](#requests).
- **Case archive** — archived terminal cases, with details and reopen.
- **Evidence locker** — the artifact inventory and explicit cleanup.
- **⚙ Settings** — Precinct settings: precinct and per-unit configuration,
  agent profiles, quiet hours, budgets and execution placement (Mjolnir
  targets, profiles and runtime selection).

The browser also shows the precinct's blotter, its patrol reports and its
clearance stats: attempt, artifact and outcome records with CSV export and
operator judgments. See [Outcomes](#outcomes).

### Keyboard

| Key | Action |
| --- | --- |
| `0` | All precincts. |
| `I` | Needs you, across every precinct. |
| `1`–`8` | Units in roster order: Patrol, Detectives, Intel, Slop Squad, Courthouse, Task Force, Forensics, Release. |
| `P`, `B`, `C` | Precinct, Board and Compact views. |
| `Esc` | Close a panel. |
| `?` | Radio codes: the shortcut help. |

### Cartoon layer

The Precinct view adds a small cartoon layer. It only decorates committed state
and never sends a command; clicking a squad car or a slop blob only opens that
case.

- **Robot cops.** Each unit card shows a robot cop doing its unit's job. It
  dozes through quiet hours and waves under a red light when its unit is stuck.
  Units the Desk Sergeant is asking about show **ON THE LINE**.
- **Squad cars.** Every transfer is driven along the street under the case-flow
  lanes by a squad car, lights flashing ("WEE-OO"); click the car to open the
  case. The arriving lane gets a stamp: **BOOKED**, **ON THE DOCKET**,
  **ADMITTED**, **ASSIGNED**, **EVIDENCE IN**, **SENT BACK**, **CLEARED** or
  **RELEASED**. When Forensics sends findings back, the slop gets a **CITED**
  ticket. A unit that reports an error gets **STUCK** on its lane.
- **Slop Tank.** Below the case flow, the tank holds every case the precinct
  dismissed as a cuffed slop blob with its case number (the newest twelve are
  drawn, with a count of all); click one to open the case. When a case is newly
  dismissed, a slop blob is cuffed (**BUSTED!**) and a paddy wagon hauls it to
  the tank.

**Motion on/off** in the header, or the operating system's reduced-motion
setting, turns all of this animation off without changing anything underneath:
the robot cops stand still and no cars, stamps or arrests play. The Slop Tank
still lists its cases.

## Desk Sergeant

The Desk Sergeant is a bounded ACP conversation that answers questions about the
precinct. It sees worker summaries, recent failures, case counts and up to
thirty cases, plus public settings. It cannot read the workspace, run tools, or
write anything. It may propose standing down one unit (a `pause`); the proposal
is inert until you confirm its exact digest against current state.

Ask the Desk Sergeant from a precinct in the browser. Answers stream from
service state, and a proposal remains inert until you confirm its exact digest
against current state. The API route is still `/api/guide`.

## Outcomes

The Squad records three kinds of outcome, which the browser shows as clearance
stats: **attempts** (one dispatch), **artifacts** (an issue, pull request or
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
| `POST /api/control` | `start`/`pause`/`stop`/`retry`/`delete`/`admit`/`decline` a precinct (`town`), unit (`house`) or case (`task`). |
| `POST /api/guide` | Ask, cancel or confirm a Desk Sergeant turn. |
| `POST /api/towns` | Add a precinct. |
| `POST /api/settings` | Update service, precinct or unit settings. |
| `POST /api/capacity` | Set `max_workers`. |
| `POST /api/quiet-hours` | Set service or precinct quiet hours. |
| `POST /api/attention-hook` | Configure the attention hook. |
| `POST /api/storage`, `POST /api/storage/cleanup` | Evidence locker inventory and cleanup. |
| `POST /api/history` | Case archive pages and details. |
| `POST /api/task-detail` | Bounded detail for one active case. |
| `POST /api/choices` | Model and effort choices for a profile. |
| `GET /api/execution-options`, `POST /api/execution-options/refresh` | Mjolnir catalog. |
| `POST /api/execution`, `POST /api/execution-runtime` | Save placement and pin a runtime. |
| `GET /api/harnesses`, `POST /api/harnesses/refresh` | ACP harness catalog. |
| `POST /api/requests`, `POST /api/requests/check` | Submit or check an issue request. |
| `POST /api/diagnostics` | Run read-only setup diagnostics for a precinct. |
| `GET /api/outcomes`, `POST /api/outcomes/judgment` | Outcome report and operator judgments. |
| `POST /api/shutdown` | Stop the service. |

The API is private to this machine. Do not expose the listen address to a
network; the token is the only authorization.
