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

## Released recovery versions

The recovery fixes ship in Town v0.4.7 and Review Bot v0.2.3. The Review Bot
release also handles both npm 11 and npm 12 package metadata during packaging.
A Town update alone does not change an existing town's independent Review Bot
pin: select 0.2.3 or a later fixed release in that bot's settings.

For the muse-acp rollout on 2026-09-18, Town was installed through its existing
npm channel and restarted at v0.4.7. Review Bot was pinned to 0.2.3. Repo,
Simplifier, Issue and Review were explicitly resumed; Bug, Feature and Release
remained paused. Failed attempts for PRs #57–60 were reset. Issue #63 was retried
after confirming it was open, unlocked, and had no PR on its expected branch.
Its retry does not jump ahead of existing PR repairs.

Feature's interrupted scan remains recorded as uncertain and paused. It was not
restarted as part of the requested backlog recovery.
