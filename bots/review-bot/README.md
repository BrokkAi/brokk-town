# Brokk Review Bot

Review a pull request's exact revision and certify what it found.

`review-bot` is a Go package of the
[Brokk Town](https://github.com/BrokkAi/brokk-town) module. Town runs it in
process as the **review observatory** house, so there is no standalone binary,
server or release tag to install. See [../../docs/bots.md](../../docs/bots.md)
and [../../docs/workflow.md](../../docs/workflow.md).

## What it does

1. Read the pull request's metadata, discussion, inline comments and prior review
   summaries. Fetch the exact base and head through the repository's pull request
   refs.
2. Inspect the complete merge-base-to-head diff and the surrounding code in a
   detached worktree owned by the bot's private repository.
3. Ask the agent for concrete defects *introduced* by the change: correctness,
   security, data loss, or a demonstrated performance regression. Style,
   speculation, feature requests and pre-existing bugs are excluded. The cap is
   ten findings by default and is never a quota.
4. Verify each candidate in a fresh session and worktree, comparing its cause and
   triggering conditions against every discussion entry and against findings
   already accepted in this batch. Unsupported, uncertain and duplicate candidates
   are dropped.
5. Validate each finding's source location against both the local diff and
   GitHub's patches. A verified finding without a valid inline anchor is reported
   in the summary with a link to the exact source commit.
6. Recheck eligibility, description, base and head, and the discussion, then
   submit a single `COMMENT` review bound to the head SHA.

The result is a certified audit Town uses to decide the next step: a clean review
advances the pull request, a first round of findings goes back to the issue house
for one repair, and a second round decides between merge with follow-ups and
closing.

## What it never does

It never approves, requests changes, merges, or pushes to a contributor's branch.
A review with no new findings reports coverage and limitations, not approval. A
dry run never grants publication or merge authority.

An audit describes exactly one revision. A review of any other base or head is
stale and is never attributed to the dispatched task.

## Configuration

Town fills this configuration from the town's agent profile and the review house's
policy (`bt settings --role review`). The package's own fields are:

| Field | Meaning |
| --- | --- |
| `remote`, `branch`, `directory`, `state_directory` | Private worktrees and state. |
| `instruction_files` | Repository instruction files handed to the agent. |
| `agent` | ACP command, environment, model and effort. |
| `github.host`, `github.repo` | Enterprise host, or a local mirror's GitHub repository. |
| `poll`, `timeout`, `retry_delay`, `attempts` | Cadence and retry budget. |
| `labels`, `exclude_labels` | Selector for the pull requests the house may take. |
| `max_findings` | Most findings one review may report (1–20). |
| `pr` | Pin the house to one pull request. Town sets this for an exact dispatch. |
| `focus` | Steer the review toward one area. |
| `verify` | Operator command that must pass. |
| `dry_run` | Inspect proposed review payloads without writing to GitHub. |
| `remote_agent` | Managed-execution placement, set by Town for Mjolnir runs. |

Town's policy mapping is `labels`, `exclude_labels`, `only` → `pr`, `focus`,
`limit` → `max_findings`, `attempts` and `verify`. Defaults: poll every 5 minutes,
a 2-hour attempt budget, a 15-minute retry delay, 3 attempts and at most 10
findings.

A revision gets at most two attempts at any step. The first incomplete attempt
earns one more after a short delay; the second retires the pull request for that
revision, which Town then closes or hands to the Mayor.

## Development

```sh
go test -race ./bots/review-bot/...
go vet ./...
```

Tests use local Git fixtures and simulated GitHub and ACP outcomes; they never run
a paid model or post a review to a live repository.

## License

Apache-2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
