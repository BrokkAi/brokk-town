# Recovery and saved work

[Back to Brokk Town](../README.md)

Check `bt status --json` first. Worker capacity, queued tasks, per-task errors, and
`workers.<role>.recovery` distinguish scheduling delays from unfinished work.
Keep the saved state and write intents: they prevent duplicate publication.

## Simplifier intake

The scheduler uses the normal polling interval for intake. On startup, Town
shortens an older successful idle schedule when eligible intake is queued.
Paused workers, active runs, failure backoff, and task retry delays stay intact.

## Stale reviews

Review Bot must include the exact-base fetch fix: GitHub may report a PR base
older than the target branch tip. The bot fetches that commit and still verifies
the pull head ref and checks GitHub again before publication. A stale or incomplete
review never certifies a PR. Town requests fresh inventory and lets other PRs
continue; pull requests that have never been tried are reviewed before ones
that failed.

There is nothing to retry by hand. A revision gets two attempts; the second
failure closes Town's own pull request and queues the issue for a fresh attempt
from the current branch, which also leaves the stale base behind. A
contributor's pull request goes to the Mayor instead.

Install the complete Town bundle to update its supported bots together. Town
verifies the bot versions against `bundle.json`; there is no per-town bot-version
setting. Building the repositories locally does not publish a release.

## Interrupted workers

Town stops its workers when it exits. Interrupted dispatches retain their target
and revision as an uncertain outcome. Reconcile saved results and GitHub before
authorizing a retry.

Repo Bot resumes repository inventory automatically after an interrupted read.
Older recovery records and interrupted runs that could repair a branch also
resume inventory, while retaining the uncertain repair and holding further
repairs. A successful inventory clears the previous repository error; its log
remains available. Paused repo houses stay paused. After checking the branch and
saved bot result, `bt start --repo OWNER/REPO --role repo` authorizes repairs again.
There is no need to delete state or clear a recovery record to refresh inventory.

For Issue Bot, a matching saved `submitted` or `has_pr` outcome resolves the hold.
Otherwise inspect the issue, its expected branch, open/closed PRs, and saved bot
result. If work did not land, request a targeted retry:

```sh
bt retry --repo OWNER/REPO --task issue:63
```

Retry preserves a paused house's pause; start that house afterwards if needed.
A generic start cannot clear a hold that names an unresolved task. For a discovery
scan with no individual task, inspect recent issues for publications, then use
`bt start --repo OWNER/REPO --role bug` to explicitly authorize another scan.
The uncertainty itself is not proof that no work landed.

## Pushes, merges, and saved repair commits

If a push or merge response is lost, Town checks GitHub rather than assuming
success. Inspect the task and its saved intent, then use **Reconcile and retry** or:

```sh
bt retry --repo OWNER/REPO --task pr:123
```

Retry explicitly permits another attempt after fresh checks. For an uncertain
repair push, it reuses and verifies the saved commit; it does not rerun the agent.
If the PR moved, the saved worktree is retained for inspection. A retry also resets
an exhausted repair budget. Never delete an uncertain intent to force progress.
Retry clears that one task's block; it never starts a paused house. If the house
is paused, the task says so and waits for you to start it.

An uncertain repair is settled by ancestry, not only by an equal revision: once
the commit Town pushed is proven to be in the pull request's history, the push
landed, the intent is closed, and the newer revision goes back for review. Town
never republishes the saved commit over work that built on it.

A worktree is kept only while a saved intent still needs it. Repairs that fail
before anything is pushed release their worktree and its `town-repair-*` branch
immediately, and the issue house collects worktrees whose intent has since been
confirmed, so an unattended town does not accumulate them.

When the release house reports that Release Bot's retry budget is exhausted, fix
the reported failure and ask Town to lift the budget:

```sh
bt retry --repo OWNER/REPO --role release
```

The next release run first calls the bot's `POST /v1/retry` worker API, which
resets the pending release's attempt budget in its own workspace, then resumes
the same release. Unlike a task retry, a release retry also starts the release
house if it was paused. Town never edits the bot's private state. The pinned Release
Bot must advertise the `retry` capability; older pins report that plainly.
Use `bt status --json` to inspect saved details and GitHub to resolve conflicts.
