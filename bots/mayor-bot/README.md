# Judge Bot

The Probation Judge in SlopCop Squad: it rules on arrivals, probation or the
Slop Tank, and keeps each precinct's blotter.

Judge Bot is the `mayor-bot` package (`bots/mayor-bot`), a Go package of the
[BrokkAi/brokk-town](https://github.com/BrokkAi/brokk-town) module. The package
name, the `hall` role key and the `mayoral_decision` field keep their original
names. Judge Bot staffs the **Probation Judge** unit, and the Squad runs it in
process, so there is no standalone binary, server or release tag to install. See
[../../docs/bots.md](../../docs/bots.md) and
[../../docs/workflow.md](../../docs/workflow.md).

## What it does

The Squad asks Judge Bot for one of two one-shot duties.

**judge** — rule on one arrival waiting in Probation Court: an issue, an
external pull request, or a bot proposal. Judge Bot reads the Squad's
description of the arrival (including any advice from the Magistrate or the
Squad's own review), the live GitHub discussion, and the repository at the
exact revision in a detached worktree. It answers *admit* (grant probation) or
*decline* (send to the Slop Tank) — *delay* (continue) for a bot update — with a
reason a person can audit. The Squad applies the ruling through the same path
your own click would use.

**bulletin** — summarise the pull requests merged into the covered branch in one
window, for the people who use the software: features gained and bugs fixed, in
plain language, each item citing the pull requests it came from. The Squad shows
these bulletins as the blotter, its work-completed feed, and the returned item
list is what advances its cursor — the prose is never trusted for that.

## What it never does

It never writes to GitHub and never edits the repository. A tracked edit or a
revision that moved during the run fails it. Judgment worktrees are detached and
checked against the exact revision, and a blotter item may cite only pull
requests from its own window.

## Configuration

The Squad fills this configuration from the precinct's agent profile and the
Probation Judge unit's policy. The package's own fields are:

| Field | Meaning |
| --- | --- |
| `remote`, `branch`, `directory`, `state_directory` | Private checkout and state. |
| `instruction_files` | Repository instruction files handed to the agent. |
| `agent` | ACP command, environment, model and effort. |
| `github.host`, `github.repo` | Enterprise host, or a local mirror's GitHub repository. |
| `timeout`, `poll` | Attempt budget and blotter interval. |
| `max_items` | Most items one blotter entry may report (1–200). |
| `verify` | Operator command that must pass. |

The Squad's policy mapping is `limit` → `max_items` and `verify`. Defaults: a
1-hour attempt budget, a 24-hour blotter interval, and at most 40 items.

The precinct's `bulletin_seconds` setting additionally bounds how often a
blotter entry is written, and only when something merged since the last one.

## Development

```sh
go test -race ./bots/mayor-bot/...
go vet ./...
```

Tests use simulated GitHub and ACP outcomes; they never run a paid model or write
to a live repository.

## License

Apache-2.0. See [LICENSE](../../LICENSE) and [NOTICE](../../NOTICE).
