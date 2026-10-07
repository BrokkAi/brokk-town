# Issue Bot

Walk GitHub issues, give a coding agent one focused attempt at each, and open
pull requests for review.

`issue-bot` is a Go package of the
[SlopCop Squad](https://github.com/BrokkAi/brokk-town) module. Issue Bot staffs
the **Caseworker** unit (role key `issue`), and the Squad runs it in process, so
there is no standalone binary, server or release tag to install. See
[../../docs/bots.md](../../docs/bots.md) and
[../../docs/workflow.md](../../docs/workflow.md).

## What it does

1. Walk open, unlocked issues. The Squad may dispatch one exact issue; otherwise
   the unit takes its eligible caseload in order. Workspace policy labels and
   excludes are applied before the run.
2. Skip issues that already have a linked or cross-referenced pull request,
   whether open, draft, merged or closed, from any author, branch or fork.
3. Post an "I'm starting work on this issue" comment with a unique claim and
   expiry. Other instances skip an issue with a live claim and continue.
4. Create a persistent per-issue branch and isolated worktree. The agent reads the
   repository instructions, the issue and its discussion, implements a focused
   fix, runs checks, and commits locally.
5. Require a completion receipt with an explanation and validation evidence, then
   verify the branch, clean worktree, starting commit and real diff. An optional
   operator `verify` command must also pass. The claim and any existing pull
   request are rechecked before publishing.
6. Push without force and open a pull request. The Squad asks for a regular (not
   draft) pull request; the bot records its URL against the issue.

The bot does not merge pull requests or close issues.

### Superseded work

When the Squad closes a bot pull request after review, it dispatches the issue
again with that pull request number as *superseded*. The bot resets the issue's
saved job, discards the old worktree and branch, and records the closed pull
request so it is never treated as the issue's existing pull request again. The
new attempt starts from the current base branch.

## What it never does

It never force-pushes, merges, closes issues, or deletes work another bot or a
person owns. A pull request it cannot prove it owns keeps its state and reports
an inspect-branch failure instead of being retried blindly.

## Configuration

The Squad fills this configuration from the precinct's agent profile and the
Caseworker unit's work policy in the browser. The package's own fields are:

| Field | Meaning |
| --- | --- |
| `remote`, `branch`, `directory`, `state_directory` | Private checkout, per-issue worktrees and state. |
| `instruction_files` | Repository instruction files handed to the agent. |
| `agent` | ACP command, environment, model and effort. |
| `github.host`, `github.repo` | Enterprise host, or a local mirror's GitHub repository. |
| `poll`, `timeout`, `claim_timeout`, `retry_delay`, `attempts` | Cadence, claim lease and retry budget. |
| `labels`, `exclude_labels` | Selector for the issues the unit may take. |
| `issue` | Pin the unit to one issue number. The Squad sets this for an exact dispatch. |
| `draft` | Open draft pull requests instead of ready ones. |
| `verify` | Operator command that must pass before publishing. |

The Squad's policy mapping is `labels`, `exclude_labels`, `only` → `issue`,
`attempts` and `verify`. Defaults: poll every 5 minutes, a 2-hour attempt
budget, a 15-minute claim lease, a 15-minute retry delay and 3 attempts.

Claims are advisory coordination on GitHub, not an atomic lock: delayed comment
visibility can briefly allow duplicate local effort, which the pre-publication
rechecks reduce but cannot eliminate.

## Development

```sh
go test -race ./bots/issue-bot/...
go vet ./...
```

Tests use temporary Git remotes and simulated GitHub responses. They never run a
paid agent or create pull requests in a live repository.

## License

Apache-2.0. See [LICENSE](../../LICENSE) and [NOTICE](../../NOTICE).
