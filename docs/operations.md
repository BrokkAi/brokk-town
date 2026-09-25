# Requests and task controls

[Back to Brokk Town](../README.md)

- [Create a request](#create-a-request)
- [Snooze one task](#snoozing-one-task)
- [Delete and restore a town](#delete-and-restore-a-town)

## Create a request

Choose **New request**, select **Feature request** or **Bug report**, and describe
the work. **Create GitHub issue** posts it to that town's repository and places
the confirmed issue in the workshop queue. This works while workers are paused;
start issue-bot when you want implementation to begin. Recent submissions show
the confirmation status and a link to the issue. Demo submissions stay local.

```sh
./bin/bt request --repo BrokkAi/my-project --kind feature --title 'Add keyboard navigation' --body-file request.md
# Use --kind bug for a bug report; --body-file - reads stdin.
./bin/bt request --repo BrokkAi/my-project --check --request-id SAVED_ID
```

Requests have durable IDs, and the CLI prints the ID before sending. If the
connection drops, reuse it with `--request-id` and the same content. The browser
retains the submitted draft in that tab until acknowledgment. An uncertain GitHub
POST is never automatically repeated: Town looks for its hidden receipt in all
open and closed issues every five minutes. **Check GitHub for receipt** requests
an earlier read. Inspect GitHub before manually filing an unconfirmed request again.

## Snoozing one task

To set one issue or pull request aside without pausing its house, snooze it
until a chosen time. Open the task in the browser and choose **Snooze…**, or:

```sh
./bin/bt defer --repo BrokkAi/my-project --task pr:123 --until 2d --reason "waiting on the vendor fix"
./bin/bt defer --repo BrokkAi/my-project --task issue:45 --until 2026-10-01T09:00:00Z
./bin/bt undefer --repo BrokkAi/my-project --task pr:123
```

`--until` takes an RFC 3339 time or a delay from now (`90m`, `4h`, `2d`); the
resume time must be in the future and within 366 days, and the reason is one
line of at most 200 characters. The house keeps working the rest of its queue.
Until the resume time, no agent starts for the snoozed task: Issue, Review and
Simplifier Bots skip it, Mayor Bot does not judge it, and Town does not merge
it. A run already under way when you snooze finishes. The Mayor can still decide
a snoozed arrival by hand.

The task shows as **Snoozed** with its reason and resume time in the browser
and in `bt status --json` (`deferred_until`, `defer_reason`). Snoozed is distinct from
blocked and failed, so a snoozed task does not appear in the inbox. At the
resume time Town clears the snooze, wakes the house and records the event; the
snooze is saved state, so it survives a restart and repository reconciliation.
Resume now (`bt undefer`) makes the task eligible immediately. Finished work
(merged, closed, declined, or an implemented issue) cannot be snoozed.

## Delete and restore a town

To remove a town, choose **Settings → Delete town** and confirm, or run:

```sh
./bin/bt delete --repo BrokkAi/my-project
```

Deletion cancels its workers, cancels queued issue submissions, and removes it
from the browser and terminal. GitHub repositories, issues, and PRs are preserved.
Local history, uncertain writes, and private worktrees remain as recovery records.
Adding the same repository again restores that history and its previous
settings, with automation paused and the reporter enabled. A merge policy or
harness/agent choice given with the add replaces the previous one; budgets, work
policies, bot profiles, funnels and the branch are kept. A deleted town listed in
`bt --config` is restored with the file's settings, and Town prints a notice. A town
that already worked on one branch cannot be restored onto another. Deleting a
town that is already deleted reports `unknown town`. Wait for stopping workers to
finish before restoring a town.

For interrupted work and explicit retries, see [Recovery](recovery.md).
