# Brokk Mayor Bot

The Mayor of a Brokk Town: it judges arrivals and writes the town's bulletin.

`mayor-bot` is a Go package of the
[BrokkAi/brokk-town](https://github.com/BrokkAi/brokk-town) module. Town runs it
in process as the **Town Hall** house, so there is no standalone binary, server
or release tag to install. See [../../docs/bots.md](../../docs/bots.md) and
[../../docs/workflow.md](../../docs/workflow.md).

## What it does

Town asks the Mayor for one of two one-shot duties.

**judge** — decide one arrival waiting at Town Hall: an issue, an external pull
request, or a bot proposal. The Mayor reads Town's description of the arrival
(including any Simplifier advice or Town review), the live GitHub discussion, and
the repository at the exact revision in a detached worktree. It answers *admit*
or *decline* — *delay* for a bot update — with a reason a person can audit. Town
applies the decision through the same path an operator's click would use.

**bulletin** — summarise the pull requests merged into the covered branch in one
window, for the people who use the software: features gained and bugs fixed, in
plain language, each item citing the pull requests it came from. Town shows
bulletins as its work-completed feed, and the returned item list is what advances
its cursor — the prose is never trusted for that.

## What it never does

It never writes to GitHub and never edits the repository. A tracked edit or a
revision that moved during the run fails it. Judgment worktrees are detached and
checked against the exact revision, and a bulletin item may cite only pull
requests from its own window.

## Configuration

Town fills this configuration from the town's agent profile and the Town Hall
policy. The package's own fields are:

| Field | Meaning |
| --- | --- |
| `remote`, `branch`, `directory`, `state_directory` | Private checkout and state. |
| `instruction_files` | Repository instruction files handed to the agent. |
| `agent` | ACP command, environment, model and effort. |
| `github.host`, `github.repo` | Enterprise host, or a local mirror's GitHub repository. |
| `timeout`, `poll` | Attempt budget and bulletin interval. |
| `max_items` | Most items one bulletin may report (1–200). |
| `verify` | Operator command that must pass. |

Town's policy mapping is `limit` → `max_items` and `verify`. Defaults: a 1-hour
attempt budget, a 24-hour bulletin interval, and at most 40 items.

The town's `bulletin_seconds` setting additionally bounds how often a bulletin is
written, and only when something merged since the last one.

## Development

```sh
go test -race ./bots/mayor-bot/...
go vet ./...
```

Tests use simulated GitHub and ACP outcomes; they never run a paid model or write
to a live repository.

## License

MIT. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
