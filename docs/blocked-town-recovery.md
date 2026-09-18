# Recovering a blocked town

Check `bt status` first. Worker capacity, queued tasks, per-task errors, and
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
review never certifies a PR. Town requests fresh inventory, retains the task's
bounded retry budget, and lets other PRs continue.

After installing a Review Bot release containing this fix, set its exact pin in
Town's bot settings. Retry blocked reviews individually, for example:

```sh
bt retry --repo BrokkAi/muse-acp --task pr:58
bt retry --repo BrokkAi/muse-acp --task pr:59
```

A new Town binary alone cannot fix the independently pinned Review Bot. No
registry release is created by building these repositories locally.

## Interrupted workers

A detachable worker that remains alive can replay its result when Town reconnects.
If the process is gone and its result cannot be recovered, Town records the exact
unresolved dispatch and holds automatic replacement work. A live but unauthenticated
process keeps its handle; it must not be replaced merely because the socket failed.

For Issue Bot, a matching saved `submitted` or `has_pr` outcome resolves the hold.
Otherwise inspect the issue, its expected branch, open/closed PRs, and saved bot
result. If work did not land, request a targeted retry:

```sh
bt retry --repo BrokkAi/muse-acp --task issue:63
```

Retry preserves a paused house's pause; start that house afterwards if needed.
A generic start cannot clear a hold that names an unresolved task. For a discovery
scan with no individual task, inspect recent issues for publications, then use
`bt start --repo BrokkAi/muse-acp --role bug` to explicitly authorize another scan.
The uncertainty itself is not proof that no work landed.

## Implementation and rollout status, 2026-09-18

Town's local implementation contains scheduling migration, review isolation and
reconciliation, and durable interrupted-dispatch recovery. Review Bot's local
implementation contains exact-base fetching and explicit stale result details.

The inspected live service runs the npm-installed Town v0.4.6 and pins Review Bot
0.2.2. It does not run these local commits. Rollout requires building/installing
Town and publishing/installing a Review Bot version with the fix, then selecting
that exact pin. Registry publication is a separate release action.

At inspection, Issue Bot's saved #63 job was pending with `context canceled`, no
result and no PR URL. Always recheck GitHub immediately before retrying; that
snapshot alone does not prove that nothing landed.
