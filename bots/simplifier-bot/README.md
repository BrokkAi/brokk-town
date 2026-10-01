# Brokk Simplifier Bot

The complexity and value reviewer of a Brokk Town.

`simplifier-bot` is a Go package of the
[BrokkAi/brokk-town](https://github.com/BrokkAi/brokk-town) module. Town runs it
in process as the **simplifier clarifier** house, so there is no standalone
binary, server or release tag to install. See
[../../docs/bots.md](../../docs/bots.md) and
[../../docs/workflow.md](../../docs/workflow.md).

## What it does

Town runs it in two ways.

**assessing one arrival** — every incoming issue and external pull request is
assessed before normal issue or review work. The assessment is a bounded
admission or decline recommendation attached to a Mayoral decision. The Mayor
remains the decision maker in *suggest* mode.

**scanning the repository** — the bot proposes removal or replacement of
subsystems that add disproportionate complexity or deliver little value, and files
those proposals as marked GitHub issues.

The town's `simplifier_mode` chooses what happens to an assessment:

- **suggest** — the arrival goes to Town Hall and the Mayor decides.
- **auto** — Town admits routine work, declines low-value complex work, and closes
  low-value complex issues without a separate Mayoral decision. An operator can
  still admit an auto-decline before the closer claims it.

The implementation is deliberately conservative: it must not recommend removing
security, privacy, correctness, accessibility, durability, observability, or
legally required behaviour, and uncertain cases are admitted for human review.
Issues it files carry a hidden `<!-- simplifier-bot:… -->` marker and are not
routed back through it.

## What it never does

It never edits tracked source, commits, pushes, or writes to GitHub beyond filing
its own marked proposals. Assessments perform no GitHub writes at all. A tracked
edit or a moved revision fails an assessment.

## Configuration

Town fills this configuration from the town's agent profile and the simplifier
house's policy (`bt settings --role simplifier`). The package's own fields are:

| Field | Meaning |
| --- | --- |
| `remote`, `branch`, `directory`, `state_directory` | Private checkout and state. |
| `instruction_files` | Repository instruction files handed to the agent. |
| `agent` | ACP command, environment, model and effort. |
| `github.host`, `github.repo` | Enterprise host, or a local mirror's GitHub repository. |
| `poll`, `timeout` | Scan cadence and attempt budget. |
| `max_proposals` | Most proposals one scan may file (1–20). |
| `labels` | Labels added to filed proposals. |
| `verify` | Operator command that must pass. |
| `dry_run` | Save proposals without filing them. |

Town's policy mapping is `labels`, `limit` → `max_proposals` and `verify`.
Defaults: a scan every 30 minutes, a 2-hour attempt budget and at most 3
proposals per scan.

## Development

```sh
go test -race ./bots/simplifier-bot/...
go vet ./...
```

Tests use local Git fixtures and simulated GitHub and ACP outcomes; they never run
a paid model or create issues in a live repository.

## License

Apache-2.0. See [LICENSE](../../LICENSE) and [NOTICE](../../NOTICE).
