# Configuration

SlopCop Squad has three levels of configuration, all stored in the state
directory (`--state-dir` selects the directory):

1. **Service** — one shared scheduler: capacity, default quiet hours, the
   attention hook. The browser calls these Squad settings.
2. **Precinct** — one repository (a `town`): agent profile, merge policy,
   cadence, budget, funnels, execution placement.
3. **Unit** — one bot in one precinct (a `house`, named by its role key): an
   independent agent profile, a work policy, and per-bot limits.

Change any of it in the browser. A `--config` file can seed it at startup.
Nothing is stored in the repository, and nothing is stored in the git checkout
the Squad manages.

## Changing settings

Squad settings edit the service; a precinct's ⚙ Settings (Precinct settings)
edit that precinct or one of its units. Settings only offer fields a unit can
honour, so a typo cannot quietly change another bot's behaviour. Omitted fields
preserve the stored value.

## Service settings

| Field | Configured in | Meaning |
| --- | --- | --- |
| `max_workers` | Squad settings | Active agent workers across all precincts. 1–64, default 4. |
| `quiet_hours` | Squad settings or config file | Default weekly quiet windows for precincts that set none. |
| `attention_hook` | Squad settings | Optional local notifier for blocked/completed attention events. |

The attention hook is disabled by default. When enabled it receives one small
JSON object on stdin and runs for at most ten seconds, independently of quiet
hours and never in demo mode. Command output is discarded. See
[operations.md](operations.md).

## Precinct defaults

| JSON field | Configured in | Default | Meaning |
| --- | --- | --- | --- |
| `repo` | ＋ New precinct in the browser | required | `OWNER/REPO` the precinct serves. |
| `branch` | config file | repository default | Branch the precinct covers. Empty follows GitHub's default. |
| `merge_policy` | Settings | `bot` | `bot` (PRs the Squad created), `all` (external too), or `manual` (never; stands Release down). |
| `poll_seconds` | config file | 60 | Base cadence for intake and repair work. Minimum 10. |
| `report_seconds` | config file | 1800 | Cadence for periodic patrol reports. Minimum 60. |
| `max_cycles` | config file | 5 | Attempts allowed per case before it is held. 1–20. |
| `simplifier_mode` | config file | `suggest` | `suggest` routes arrivals through the Courthouse for a ruling; `auto` lets the Squad dismiss low-value work. |
| `bulletin_seconds` | config file | 86400 | Minimum interval between blotter entries from Judge Bot. At least 600. |
| `review_close_severity` | Settings | `P2` | Least severe finding that closes a PR after the second review. |
| `budget` | Settings | none | Agent attempt/minute cap per accounting period. |
| `quiet_hours` | Settings or config file | service default | The precinct's own windows; `[]` opts out. |
| `funnels` | config file | none | Named source funnels. |
| `execution` | Execution location in Settings | direct local | Where this precinct's agent work runs. |
| `bot_execution` | Execution location in Settings | inherit | Per-unit execution override. |

## Agent profiles

A precinct has one default agent profile and each unit can override it. A unit
with an override stays independent when the precinct default changes; restoring
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

A work policy tells one unit what to take. Only the fields a unit supports are
accepted.

| Unit (role key) | Supported policy fields |
| --- | --- |
| Detectives (`bug`), Intel (`feature`) | `labels`, `focus`, `limit`, `attempts`, `verify` |
| Task Force (`issue`) | `labels`, `exclude-labels`, `only`, `attempts`, `verify` |
| Forensics (`review`) | `labels`, `exclude-labels`, `only`, `focus`, `limit`, `attempts`, `verify` |
| Release (`release`) | `attempts`, `verify`, plus every release flag below |
| Slop Squad (`simplifier`) | `labels`, `limit`, `verify` |
| Patrol (`repo`) | `limit`, `verify` |
| Courthouse (`hall`) | `limit`, `verify` |

- `labels` is the set of labels work must carry to be eligible.
- `exclude-labels` removes work carrying any of them.
- `only` pins a unit to one issue or pull request number. It applies when the
  Squad has not already dispatched an exact item.
- `focus` is free text handed to a discovery or review prompt.
- `limit` is the per-run cap (issues filed, findings, proposals, repairs,
  blotter items, or a page size depending on the unit).
- `attempts` is how many tries one item gets before it is held.
- `verify` is a command array that must pass; it overrides the precinct-level
  `verify`.

Clearing a policy in Settings makes the unit take everything it is given again.

## Release policy

Set in Settings for the Release unit. These live under
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

A budget bounds how much agent work a precinct starts in an accounting period.
It counts **attempts** and **agent minutes**, never tokens or dollars: no
bundled harness reports usage back to the Squad.

| Field | Configured in |
| --- | --- |
| `period` | Settings (`day`, `week`, `month`, `none`) |
| `max_attempts` | Settings |
| `max_agent_minutes` | Settings |

Either cap may be omitted. When the budget is exhausted, units show a wait with
the period that will reset them; the repository is still watched. The measured
ledger resets at the start of each period.

## Quiet hours

Quiet hours are weekly windows on the machine's local clock. During a window
the Squad starts no new agent work and makes none of its own GitHub writes.
Running work finishes, and the repository keeps being watched.

- One window is `DAYS HH:MM-HH:MM`; separate windows with `;`.
- Days are `mon`…`sun`, commas and ranges are accepted, and `daily`,
  `weekdays` and `weekends` are shorthand.
- A window that ends at or before its start runs past midnight.
- `none` removes the schedule. A precinct inherits the service default unless
  it sets its own; `[]` in a config file opts one precinct out.
- At most 32 windows.

## Funnels

Funnels bring work in from sources other than the repository's own issues. Each
has an ID, a provider, a location, an optional filter, optional lifecycle
mappings, and an overlap policy. Credentials are references resolved at request
time and never stored in state.

See [funnels.md](funnels.md) for the full field list and the GitHub and Slack
specifics.

## Execution placement

By default an agent runs locally through the selected harness. A precinct can
place supported duties on an Mjolnir target instead:

Execution location, in a precinct's Settings, lists cached targets and profiles,
refreshes the catalog, and saves a precinct default or per-unit override. Local
execution and inheritance are explicit choices in the same place.

Managed review and managed issue repair are supported. Other duties show an
explicit hold until you select direct local execution. See
[mjolnir.md](mjolnir.md).

## Config file

`sct --config FILE` seeds settings at startup. Two shapes are accepted.

**Array** — a list of precinct configurations:

```json
[
  { "repo": "OWNER/REPO", "branch": "main", "merge_policy": "bot", "poll_seconds": 60, "report_seconds": 1800, "max_cycles": 5 }
]
```

**Object** — service settings plus precincts (the `towns` array):

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

- Unknown fields are rejected, in the service object and inside every precinct.
- An omitted `branch` keeps the precinct's stored branch; an explicit `""`
  clears it so the precinct follows the repository default again.
- A precinct already present is updated; a deleted precinct is restored with its
  recovery records.
- A real config is refused in demo mode.
- The object form requires a `towns` array; `max_workers` cannot be null.

A complete example is in [config.example.json](config.example.json).
