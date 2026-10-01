# Configuration

Town has three levels of configuration, all stored in the state directory
(`--state-dir` selects the directory):

1. **Service** — one shared scheduler: capacity, default quiet hours, the
   attention hook.
2. **Town** — one repository: agent profile, merge policy, cadence, budget,
   funnels, execution placement.
3. **House** — one bot in one town: an independent agent profile, a work policy,
   and per-bot limits.

Change any of it in the browser. A `--config` file can seed it at startup.
Nothing is stored in the repository, and nothing is stored in the git checkout
Town manages.

## Changing settings

Settings edits the service, one town, or one house. It only offers fields a
house can honour, so a typo cannot quietly change another bot's behaviour.
Omitted fields preserve the stored value.

## Service settings

| Field | Configured in | Meaning |
| --- | --- | --- |
| `max_workers` | Settings | Active agent workers across all towns. 1–64, default 4. |
| `quiet_hours` | Settings or config file | Default weekly quiet windows for towns that set none. |
| `attention_hook` | Settings | Optional local notifier for blocked/completed attention events. |

The attention hook is disabled by default. When enabled it receives one small
JSON object on stdin and runs for at most ten seconds, independently of quiet
hours and never in demo mode. Command output is discarded. See
[operations.md](operations.md).

## Town defaults

| JSON field | Configured in | Default | Meaning |
| --- | --- | --- | --- |
| `repo` | Add Town in the browser | required | `OWNER/REPO` the town serves. |
| `branch` | config file | repository default | Branch the town covers. Empty follows GitHub's default. |
| `merge_policy` | Settings | `bot` | `bot` (Town-created PRs), `all` (external too), or `manual` (never; pauses Release). |
| `poll_seconds` | config file | 60 | Base cadence for intake and repair work. Minimum 10. |
| `report_seconds` | config file | 1800 | Cadence for periodic town reports. Minimum 60. |
| `max_cycles` | config file | 5 | Attempts allowed per task before it is held. 1–20. |
| `simplifier_mode` | config file | `suggest` | `suggest` routes arrivals through Town Hall; `auto` lets Town decline low-value work. |
| `bulletin_seconds` | config file | 86400 | Minimum interval between Mayor bulletins. At least 600. |
| `review_close_severity` | Settings | `P2` | Least severe finding that closes a PR after the second review. |
| `budget` | Settings | none | Agent attempt/minute cap per accounting period. |
| `quiet_hours` | Settings or config file | service default | Town's own windows; `[]` opts out. |
| `funnels` | config file | none | Named source funnels. |
| `execution` | Execution in the browser | direct local | Where this town's agent work runs. |
| `bot_execution` | Execution in the browser | inherit | Per-house execution override. |

## Agent profiles

A town has one default agent profile and each house can override it. A house with
an override stays independent when the town default changes; restoring
inheritance returns it to the default.

| Field | Configured in | Meaning |
| --- | --- | --- |
| `harness` | Settings | ACP harness ID, `anvil`, `muse-acp`, `draupnir`, or `custom`. |
| `agent.command` | Settings or config file | JSON argument array; only valid for `custom`. |
| `agent.model` | Settings | ACP model ID; empty uses the harness default. |
| `agent.effort` | Settings | Reasoning effort advertised by the harness. |
| `agent.auth_method` | config file | Optional advertised authentication method. |
| `agent.mode` | config file | Optional advertised session mode. |
| `agent.environment` | config file | Extra environment variables for the harness. |
| `harness_definition` | Settings | Pinned catalog entry, written when you select a version. |

The default harness is `codex-acp`. Selecting a different harness clears the
stored model, effort and mode, because those belong to the harness that
advertised them. Settings reads what a profile can actually offer before you
save it. See [harnesses.md](harnesses.md).

## Work policies

A work policy tells one house what to take. Only the fields a house supports are
accepted.

| Role | Supported policy fields |
| --- | --- |
| bug, feature | `labels`, `focus`, `limit`, `attempts`, `verify` |
| issue | `labels`, `exclude-labels`, `only`, `attempts`, `verify` |
| review | `labels`, `exclude-labels`, `only`, `focus`, `limit`, `attempts`, `verify` |
| release | `attempts`, `verify`, plus every release flag below |
| simplifier | `labels`, `limit`, `verify` |
| repo | `limit`, `verify` |
| hall | `limit`, `verify` |

- `labels` is the set of labels work must carry to be eligible.
- `exclude-labels` removes work carrying any of them.
- `only` pins a house to one issue or pull request number. It applies when Town
  has not already dispatched an exact item.
- `focus` is free text handed to a discovery or review prompt.
- `limit` is the per-run cap (issues filed, findings, proposals, repairs,
  bulletin items, or a page size depending on the house).
- `attempts` is how many tries one item gets before it is held.
- `verify` is a command array that must pass; it overrides the town-level
  `verify`.

Clearing a policy in Settings makes the house take everything it is given again.

## Release policy

Set in Settings for the release house. These live under
`bot_policies.<role>.release` in a config file.

| JSON field | Configured in | Default | Meaning |
| --- | --- | --- | --- |
| `daily_seconds` | Settings or config file | 86400 | Deadline after which unreleased commits are released. |
| `minimum_gap_seconds` | Settings or config file | 7200 | Shortest interval between two releases. |
| `quiet_seconds` | Settings or config file | 900 | Branch must be still this long before a release. |
| `burst` | Settings or config file | 5 | Commits inside the burst window that trigger an early release. `0` disables. |
| `burst_window_seconds` | Settings or config file | 7200 | The burst window. |
| `triage` | Settings or config file | on | Ask the agent whether unreleased commits warrant an early release. |
| `preflight` | Settings or config file | none | Command that must pass before publishing. |
| `verification_timeout_seconds` | Settings or config file | 1800 | Bound on the independent publication check. |
| `workflows` | Settings or config file | discovered | GitHub workflows a release must see succeed. |
| `assets` | Settings or config file | none | Asset patterns the release must publish. |

See [workflow.md](workflow.md) for the cadence rules.

## Budgets

A budget bounds how much agent work a town starts in an accounting period. It
counts **attempts** and **agent minutes**, never tokens or dollars: no bundled
harness reports usage back to Town.

| Field | Configured in |
| --- | --- |
| `period` | Settings (`day`, `week`, `month`, `none`) |
| `max_attempts` | Settings |
| `max_agent_minutes` | Settings |

Either cap may be omitted. When the budget is exhausted, houses show a wait with
the period that will reset them; the repository is still watched. The measured
ledger resets at the start of each period.

## Quiet hours

Quiet hours are weekly windows on the machine's local clock. During a window
Town starts no new agent work and makes none of its own GitHub writes. Running
work finishes, and the repository keeps being watched.

- One window is `DAYS HH:MM-HH:MM`; separate windows with `;`.
- Days are `mon`…`sun`, commas and ranges are accepted, and `daily`,
  `weekdays` and `weekends` are shorthand.
- A window that ends at or before its start runs past midnight.
- `none` removes the schedule. A town inherits the service default unless it
  sets its own; `[]` in a config file opts one town out.
- At most 32 windows.

## Funnels

Funnels bring work in from sources other than the repository's own issues. Each
has an ID, a provider, a location, an optional filter, optional lifecycle
mappings, and an overlap policy. Credentials are references resolved at request
time and never stored in state.

See [funnels.md](funnels.md) for the full field list and the GitHub and Slack
specifics.

## Execution placement

By default an agent runs locally through the selected harness. A town can place
supported duties on an Mjolnir target instead:

The Execution view lists cached targets and profiles, refreshes the catalog, and
saves a town default or per-house override. Local execution and inheritance are
explicit choices in the same view.

Managed review and managed issue repair are supported. Other duties show an
explicit hold until you select direct local execution. See
[mjolnir.md](mjolnir.md).

## Config file

`bt --config FILE` seeds settings at startup. Two shapes are accepted.

**Array** — a list of town configurations:

```json
[
  { "repo": "OWNER/REPO", "branch": "main", "merge_policy": "bot", "poll_seconds": 60, "report_seconds": 1800, "max_cycles": 5 }
]
```

**Object** — service settings plus towns:

```json
{
  "max_workers": 4,
  "quiet_hours": [{ "days": ["sat", "sun"], "start": "00:00", "end": "24:00" }],
  "attention_hook": { "enabled": false },
  "towns": [
    { "repo": "OWNER/REPO", "branch": "main", "merge_policy": "bot", "poll_seconds": 60, "report_seconds": 1800, "max_cycles": 5 }
  ]
}
```

Rules:

- Unknown fields are rejected, in the service object and inside every town.
- An omitted `branch` keeps the town's stored branch; an explicit `""` clears it
  so the town follows the repository default again.
- A town already present is updated; a deleted town is restored with its
  recovery records.
- A real config is refused in demo mode.
- The object form requires a `towns` array; `max_workers` cannot be null.

A complete example is in [config.example.json](config.example.json).
