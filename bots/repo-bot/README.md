# Repo Bot

Observe a repository, and keep the branch the precinct covers healthy.

`repo-bot` is a Go package of the
[SlopCop Squad](https://github.com/BrokkAi/brokk-town) module. Repo Bot staffs the
**Patrol** unit (role key `repo`), and the Squad runs it in process, so there is
no standalone binary, server or release tag to install. See
[../../docs/bots.md](../../docs/bots.md).

## What it does

One run has two duties.

**inventory** — a complete observation of the repository: the branch this
precinct covers, its exact head, every issue, pull request and release, the
commits the branch gained since the Squad last looked, and proof of which
commits a published release already contains. The Squad applies that inventory
to its own task graph; this bot never interprets the Squad's state.

A partial inventory is never reported as a whole one. A paginated read that
cannot be completed is an error, because the Squad would otherwise treat what is
missing as deleted.

**branch health** — read the checks GitHub reports on the branch head and, when
they are failing, repair the branch with one agent attempt and publish the fix.

## Repairing a failing branch

When the head is red the bot runs one agent attempt in a private worktree checked
out at that exact revision, and publishes the result only if it passes the
operator's verification command. The commit is fast-forwarded onto the branch, so
a branch that moved while the agent worked is left alone and observed again on
the next run.

Attempts are budgeted per revision and the budget is spent *before* the agent
starts, so a fast poll cannot start an agent on every tick, and a crash is not
free. A new failing revision starts a fresh budget. A push the repository's
protection rules refuse ends the repair immediately, because no further attempt
could land it.

The repair prompt names the failing checks and what they reported, and instructs
the agent that a check, test or assertion may never be disabled, skipped,
weakened or deleted to reach green. Publishing is the bot's own step; the agent
does not commit, push, or touch Git history.

The bot pushes the repair directly to the branch it covers. Where that branch is
protected, configure the branch's rules and the precinct's merge policy
accordingly; this bot does not open a pull request instead.

## Configuration

The Squad fills this configuration from the precinct's agent profile and the
Patrol unit's policy. The package's own fields are:

| Field | Meaning |
| --- | --- |
| `remote`, `branch`, `directory`, `state_directory` | Private checkout and state. |
| `instruction_files` | Repository instruction files handed to the agent. |
| `agent` | ACP command, environment, model and effort. |
| `github.host`, `github.repo` | Enterprise host, or a local mirror's GitHub repository. |
| `timeout`, `poll` | Attempt budget and observation interval. |
| `max_repairs` | Agent attempts allowed per failing revision (1–10). |
| `verify` | Operator command that must pass before a repair is published. |
| `dry_run` | Commit a repair locally without publishing it. |

The Squad's policy mapping is `limit` → `max_repairs` and `verify`. Defaults:
a 1-hour attempt budget, observation every 15 minutes, and at most 3 repair
attempts per revision. Repo Bot holds an agent slot only while repairing;
inventory runs without one.

## Development

```sh
go test -race ./bots/repo-bot/...
go vet ./...
```

Tests use local Git fixtures and simulated GitHub and ACP outcomes; they never
run a paid model or push to a live repository.

## License

Apache-2.0. See [LICENSE](../../LICENSE) and [NOTICE](../../NOTICE).
