# Brokk Bug Bot

Find new bugs in a repository and file useful GitHub issues for them.

`bug-bot` is a Go package of the
[Brokk Town](https://github.com/BrokkAi/brokk-town) module. Town runs it in
process as the **bug greenhouse** house, so there is no standalone binary,
server or release tag to install. See [../../docs/bots.md](../../docs/bots.md)
for the whole set and [../../docs/workflow.md](../../docs/workflow.md) for how
its findings move through the town.

## What it does

**The model decides whether a finding duplicates an existing issue.** It compares
root causes, triggering inputs, behaviour and discussion across every open and
closed issue. There are no title-similarity thresholds and no bug fingerprints.

One scan:

1. Fetch the covered branch into Town's private checkout and make a detached
   worktree at the exact revision. The operator's own checkout and uncommitted
   work are never touched.
2. Download every open and closed issue and its comments, fully paginated.
3. Have the agent investigate the source and run relevant checks. Each candidate
   must carry a root cause, source paths, a concrete reproduction, expected and
   actual behaviour, and observed evidence or a precise code proof. Zero findings
   is a valid result.
4. Verify each candidate in a separate session against the evidence, and compare
   it with the issue history. Uncertain and invalid candidates are saved for
   inspection but never filed.
5. Recheck the revision and the issue history, then create issues carrying the
   reproduction and the review explanation.

Filed issues carry a hidden `<!-- bug-bot:… -->` marker. Each planned issue gets
a random request ID saved *before* the create call, so an interrupted publication
is reconciled against that marker instead of being duplicated.

## What it never does

It never edits tracked source, commits, pushes, or writes to GitHub beyond
creating bug issues. A changed HEAD or a tracked-file edit fails the scan.

## Configuration

Town fills this configuration from the town's agent profile and the bug house's
work policy, so you normally set it with `bt settings --role bug` or in the
browser rather than by hand. The package's own fields are:

| Field | Meaning |
| --- | --- |
| `remote`, `branch`, `directory`, `state_directory` | Where the private checkout and state live. |
| `instruction_files` | Repository instruction files handed to the agent. |
| `agent` | ACP command, environment, model and effort. |
| `review_model`, `review_effort` | Optional separate selection for verification. |
| `github.host`, `github.repo` | Enterprise host, or a local mirror's GitHub repository. |
| `poll`, `timeout`, `retry_delay`, `attempts` | Cadence and retry budget. |
| `labels` | Labels added to new issues. Town's policy labels merge into this. |
| `max_issues` | Most issues one scan may file (1–20). |
| `focus` | Steer investigation toward one area of the repository. |
| `only_on_change` | Skip a scan while the commit is unchanged. |
| `verify` | Operator command that must pass before filing. |
| `setup` | Command that prepares test prerequisites in the worktree. |

Town's policy mapping is `labels` → `labels`, `focus` → `focus`, `limit` →
`max_issues`, `attempts` → `attempts`, and `verify` → `verify`. A `verify`
command runs in the scan worktree, outside the agent's writable tree, and blocks
filing on a non-zero exit.

Defaults: a scan every 30 minutes, a 2-hour attempt budget, a 15-minute retry
delay, 3 attempts, and at most 3 issues per scan.

## Development

```sh
go test -race ./bots/bug-bot/...
go vet ./...
```

Tests use local Git fixtures and simulated GitHub and ACP outcomes. They never
run a paid model or create issues in a live repository.

## License

Apache-2.0. See [LICENSE](../../LICENSE) and [NOTICE](../../NOTICE).
