# Configuration

Brokk Town has three levels of configuration, all stored in the state
directory (`--state-dir` selects the directory):

1. **Service** — one shared scheduler: capacity, default quiet hours, the
   attention hook. The browser calls these Global settings.
2. **Repository** — one repository (a `town`): agent profile, merge policy,
   cadence, budget, funnels, execution placement.
3. **Bot** — one bot in one repository (a `house`, named by its role key): an
   independent agent profile, a work policy, and per-bot limits.

Change any of it in the browser. A `--config` file can seed it at startup.
Nothing is stored in the repository, and nothing is stored in the git checkout
Town manages.

## Changing settings

Global settings edit the service; a repository's Settings (Repository settings)
edit that repository or one of its bots. Settings only offer fields a bot can
honour, so a typo cannot quietly change another bot's behaviour. Omitted fields
preserve the stored value.

## Service settings

| Field | Configured in | Meaning |
| --- | --- | --- |
| `max_workers` | Global settings | Active agent workers across all repositories. 1–64, default 4. |
| `quiet_hours` | Global settings or config file | Default weekly quiet windows for repositories that set none. |
| `attention_hook` | Global settings | Optional notifier for blocked/completed attention events. |

The attention hook is disabled by default. When enabled it receives one small
JSON object on stdin and runs for at most ten seconds, independently of quiet
hours. Command output is discarded. See
[operations.md](operations.md).

## Repository defaults

| JSON field | Configured in | Default | Meaning |
| --- | --- | --- | --- |
| `repo` | Add repository in the browser | required | `OWNER/REPO` the repository serves. |
| `branch` | config file | repository default | Branch the repository covers. Empty follows GitHub's default. |
| `merge_policy` | Settings | `bot` | `bot` (PRs Town created), `all` (external too), or `manual` (never; keeps Release Bot paused). |
| `poll_seconds` | config file | 60 | Base cadence for intake and repair work. Minimum 10. |
| `report_seconds` | config file | 1800 | Cadence for periodic scan reports. Minimum 60. |
| `max_cycles` | config file | 5 | Attempts allowed per task before it is held. 1–20. |
| `simplifier_mode` | config file | `suggest` | `suggest` routes arrivals through Mayor Bot for a decision; `auto` lets Simplifier Bot decline low-value work. |
| `bulletin_seconds` | config file | 86400 | Minimum interval between status report entries from Mayor Bot. At least 600. |
| `review_close_severity` | Settings | `P2` | Least severe finding that closes a PR after the second review. |
| `budget` | Settings | none | Agent attempt/minute cap per accounting period. |
| `quiet_hours` | Settings or config file | service default | The repository's own windows; `[]` opts out. |
| `funnels` | config file | none | Named source funnels. |
| `execution` | Execution location in Settings | direct local | Where this repository's agent work runs. |
| `bot_execution` | Execution location in Settings | inherit | Per-bot execution override. |

## Agent profiles

A repository has one default agent profile and each bot can override it. A bot
with an override stays independent when the repository default changes; restoring
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

A work policy tells one bot what to take. Only the fields a bot supports are
accepted.

| Bot (role key) | Supported policy fields |
| --- | --- |
| Bug Bot (`bug`), Feature Bot (`feature`) | `labels`, `focus`, `limit`, `attempts`, `verify` |
| Issue Bot (`issue`) | `labels`, `exclude-labels`, `only`, `attempts`, `verify` |
| Review Bot (`review`) | `labels`, `exclude-labels`, `only`, `focus`, `limit`, `attempts`, `verify` |
| Release Bot (`release`) | `attempts`, `verify`, plus every release flag below |
| Simplifier Bot (`simplifier`) | `labels`, `limit`, `verify` |
| Repo Bot (`repo`) | `limit`, `verify` |
| Mayor Bot (`hall`) | `limit`, `verify` |

- `labels` is the set of labels work must carry to be eligible.
- `exclude-labels` removes work carrying any of them.
- `only` pins a bot to one issue or pull request number. It applies when the
  Town has not already dispatched an exact item.
- `focus` is free text handed to a discovery or review prompt.
- `limit` is the per-run cap (issues filed, findings, proposals, repairs,
  status report items, or a page size depending on the bot).
- `attempts` is how many tries one item gets before it is held.
- `verify` is a command array that must pass; it overrides the repository-level
  `verify`.

Clearing a policy in Settings makes the bot take everything it is given again.

## Release policy

Set in Settings for Release Bot. These live under
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

A budget bounds how much agent work a repository starts in an accounting period.
It counts **attempts** and **agent minutes**, never tokens or dollars: no
bundled harness reports usage back to Town.

| Field | Configured in |
| --- | --- |
| `period` | Settings (`day`, `week`, `month`, `none`) |
| `max_attempts` | Settings |
| `max_agent_minutes` | Settings |

Either cap may be omitted. When the budget is exhausted, bots show a wait with
the period that will reset them; the repository is still watched. The measured
ledger resets at the start of each period.

## Quiet hours

Quiet hours are weekly windows on the local clock of Town's pod, UTC unless its
time zone is set. During a window
Town starts no new agent work and makes none of its own GitHub writes.
Running work finishes, and the repository keeps being watched.

- One window is `DAYS HH:MM-HH:MM`; separate windows with `;`.
- Days are `mon`…`sun`, commas and ranges are accepted, and `daily`,
  `weekdays` and `weekends` are shorthand.
- A window that ends at or before its start runs past midnight.
- `none` removes the schedule. A repository inherits the service default unless
  it sets its own; `[]` in a config file opts one repository out.
- At most 32 windows.

## Funnels

Funnels bring work in from sources other than the repository's own issues. Each
has an ID, a provider, a location, an optional filter, optional lifecycle
mappings, and an overlap policy. Credentials are references resolved at request
time and never stored in state.

See [funnels.md](funnels.md) for the full field list and the GitHub and Slack
specifics.

## Execution placement

By default an agent runs locally through the selected harness. A repository can
place supported duties on an Mjolnir target instead:

Execution location, in a repository's Settings, lists cached targets and profiles,
refreshes the catalog, and saves a repository default or per-bot override. Local
execution and inheritance are explicit choices in the same place.

Managed review and managed issue repair are supported. Other duties show an
explicit hold until you select direct local execution. See
[mjolnir.md](mjolnir.md).

## Bot endpoints

Town reaches each bot over the network at its URL (see
[operations.md](operations.md#bots)), and does not start unless every bot has
a URL and a token. These are process settings, read at startup from the
environment or the config file's `bots` object; they are not stored in the
state directory and not shown in the browser. `deploy/kubernetes/bt.yaml` sets
them for every bot.

| Environment variable | Meaning |
| --- | --- |
| `BROKK_TOWN_BOT_<ROLE>_URL` | The bot's base URL, `http(s)://host:port`. `<ROLE>` is the upper-case role key: `BUG`, `FEATURE`, `ISSUE`, `REVIEW`, `RELEASE`, `SIMPLIFIER`, `REPO`, `HALL`. |
| `BROKK_TOWN_BOT_<ROLE>_TOKEN_FILE` | File holding the bot's bearer token, such as a mounted secret. |
| `BROKK_TOWN_BOT_<ROLE>_TOKEN` | The token itself, when no file is given. |
| `BROKK_TOWN_BOT_CA_FILE` | PEM certificates to trust for `https` bot URLs. When set, only these are trusted. |
| `BROKK_TOWN_CALLBACK_URL` | The address bots use to reach Town's managed-review callback, such as `http://bt:8091`, with no path. Required for managed reviews. |
| `BROKK_TOWN_CALLBACK_LISTEN` | Where Town listens for that callback. Default `:8091`. |
| `BROKK_TOWN_CALLBACK_TLS_CERT`, `BROKK_TOWN_CALLBACK_TLS_KEY` | Serve the callback over HTTPS. |

A missing URL, or a URL without a token, stops startup. Each bot's pod mounts
only its own token, so a bot never sees another bot's. An environment variable
outranks the config file. Each managed review mints its own callback token and route, valid
only while that review runs. The bot side of these settings is in
[bots.md](bots.md#bot-binaries).

## Config file

`bt --config FILE` seeds settings at startup. Two shapes are accepted.

**Array** — a list of repository configurations:

```json
[
  { "repo": "OWNER/REPO", "branch": "main", "merge_policy": "bot", "poll_seconds": 60, "report_seconds": 1800, "max_cycles": 5 }
]
```

**Object** — service settings plus repositories (the `towns` array):

```json
{
  "max_workers": 4,
  "quiet_hours": [{ "days": ["sat", "sun"], "start": "00:00", "end": "24:00" }],
  "attention_hook": { "enabled": false },
  "bots": {
    "review": { "url": "https://review-bot.internal:8443", "token_file": "/run/secrets/review-bot-token", "ca_file": "/etc/brokk/ca.pem" }
  },
  "towns": [
    { "repo": "OWNER/REPO", "branch": "main", "merge_policy": "bot", "poll_seconds": 60, "report_seconds": 1800, "max_cycles": 5 }
  ]
}
```

`bots` is keyed by role key. Each entry needs `url`; `token_file` and
`ca_file` are optional and take the place of the matching environment
variables in [Bot endpoints](#bot-endpoints). The config file never holds a
token itself.

Rules:

- Unknown fields are rejected, in the service object and inside every repository.
- An omitted `branch` keeps the repository's stored branch; an explicit `""`
  clears it so the repository follows GitHub's default branch again.
- A repository already present is updated; a deleted repository is restored with its
  recovery records.
- The object form requires a `towns` array; `max_workers` cannot be null.

A complete example is in [config.example.json](config.example.json).
