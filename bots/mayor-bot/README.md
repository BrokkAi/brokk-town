# Mayor Bot

The Mayor Bot in Brokk Town: it triages arrivals, accepting or declining each
one, and writes each repository's status report.

Mayor Bot is the `mayor-bot` package (`bots/mayor-bot`), a Go package of the
[BrokkAi/brokk-town](https://github.com/BrokkAi/brokk-town) module. The package
name, the `hall` role key and the `mayoral_decision` field keep their original
names. Mayor Bot runs the **Triage** stage, and runs as its own binary,
`mayor-bot` (`cmd/mayor-bot`), which Town reaches over the network. It ships in
the same release as `bt` and as the `ghcr.io/brokkai/brokk-town/mayor-bot`
image. See
[../../docs/bots.md](../../docs/bots.md) and
[../../docs/workflow.md](../../docs/workflow.md).

## What it does

Town asks Mayor Bot for one of two one-shot duties.

**judge** — decide one arrival waiting for triage: an issue, an
external pull request, or a bot proposal. Mayor Bot reads Town's
description of the arrival (including any advice from Simplifier Bot or
Town's own review), the live GitHub discussion, and the repository at the
exact revision in a detached worktree. It answers *admit* (accept) or
*decline* — *delay* (defer) for a bot update — with a
reason a person can audit. Town applies the decision through the same path
your own click would use.

**bulletin** — summarise the pull requests merged into the covered branch in one
window, for the people who use the software: features gained and bugs fixed, in
plain language, each item citing the pull requests it came from. Town shows
these bulletins as the status report, its work-completed feed, and the returned item
list is what advances its cursor — the prose is never trusted for that.

## What it never does

It never writes to GitHub and never edits the repository. A tracked edit or a
revision that moved during the run fails it. Judgment worktrees are detached and
checked against the exact revision, and a status report item may cite only pull
requests from its own window.

## Configuration

Town fills this configuration from the repository's agent profile and the
Mayor Bot's policy. The package's own fields are:

| Field | Meaning |
| --- | --- |
| `remote`, `branch`, `directory`, `state_directory` | Private checkout and state. |
| `instruction_files` | Repository instruction files handed to the agent. |
| `agent` | ACP command, environment, model and effort. |
| `github.host`, `github.repo` | Enterprise host, or a local mirror's GitHub repository. |
| `timeout`, `poll` | Attempt budget and status report interval. |
| `max_items` | Most items one status report entry may report (1–200). |
| `verify` | Operator command that must pass. |

Town's policy mapping is `limit` → `max_items` and `verify`. Defaults: a
1-hour attempt budget, a 24-hour status report interval, and at most 40 items.

The repository's `bulletin_seconds` setting additionally bounds how often a
status report entry is written, and only when something merged since the last one.

## Development

```sh
go test -race ./bots/mayor-bot/...
go vet ./...
```

Tests use simulated GitHub and ACP outcomes; they never run a paid model or write
to a live repository.

## License

Apache-2.0. See [LICENSE](../../LICENSE) and [NOTICE](../../NOTICE).
