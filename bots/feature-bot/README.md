# Brokk Feature Bot

Research valuable new features in a repository and file concrete GitHub
proposals for them.

`feature-bot` is a Go package of the
[Brokk Town](https://github.com/BrokkAi/brokk-town) module. Town runs it in
process as the **feature study** house, so there is no standalone binary, server
or release tag to install. See [../../docs/bots.md](../../docs/bots.md).

<img src="docs/feature-reader.png" alt="Studious feature bot with glasses and an open book" width="160">

## What it does

**The model decides whether a proposal duplicates an existing issue.** It compares
user goals, capabilities, scope and discussion across every open and closed
issue. There are no title-similarity thresholds and no feature fingerprints.

A proposal must fit the repository's purpose, demonstrate a real capability gap,
explain user value, and include bounded scope and testable acceptance criteria.
Bug fixes, refactor-only changes, speculative wishlists and previously rejected
features are excluded.

One scan:

1. Fetch the covered branch into Town's private checkout and make a detached
   worktree at the exact revision.
2. Download every open and closed issue and its comments, fully paginated.
3. Have the agent study the project's purpose, current workflows, source, docs and
   tests. Each candidate must state a user problem, the current workaround,
   proposed behaviour, user value, scope and non-goals, testable acceptance
   criteria, existing source paths, and evidence of both the gap and feasibility.
   It must separate observations from assumptions, and zero findings is valid.
4. Verify each candidate in a separate session, including a check that the
   capability is not already supported. Uncertain and invalid candidates are
   saved but never filed.
5. Recheck the revision and history, then file the proposals.

Filed issues carry a hidden `<!-- feature-bot:… -->` marker, and each planned
issue gets a request ID saved before the create call, so an interrupted
publication is reconciled instead of duplicated.

## What it never does

It never edits tracked source, commits, pushes, or writes to GitHub beyond
creating proposal issues. A changed HEAD or tracked-file edit fails the scan.

## Configuration

Town fills this configuration from the town's agent profile and the feature
house's work policy (`bt settings --role feature`). The package's own fields are:

| Field | Meaning |
| --- | --- |
| `remote`, `branch`, `directory`, `state_directory` | Where the private checkout and state live. |
| `instruction_files` | Repository instruction files handed to the agent. |
| `agent` | ACP command, environment, model and effort. |
| `review_model`, `review_effort` | Optional separate selection for verification. |
| `github.host`, `github.repo` | Enterprise host, or a local mirror's GitHub repository. |
| `poll`, `timeout`, `retry_delay`, `attempts` | Cadence and retry budget. |
| `labels` | Labels added to new issues. Town's policy labels merge into this. |
| `max_issues` | Most proposals one scan may file (1–20). |
| `focus` | Steer research toward one area. |
| `verify` | Operator command that must pass before filing. |

Town's policy mapping is `labels`, `focus`, `limit` → `max_issues`, `attempts`
and `verify`. Defaults: a scan every 30 minutes, a 2-hour attempt budget, a
15-minute retry delay, 3 attempts, and at most 3 proposals per scan.

## Development

```sh
go test -race ./bots/feature-bot/...
go vet ./...
```

Tests use local Git fixtures and simulated GitHub and ACP outcomes. They never
run a paid model or create issues in a live repository.

## License

Apache-2.0. See [LICENSE](../../LICENSE) and [NOTICE](../../NOTICE). The character art in
[docs/](docs/) is original and distributed under the same license.
