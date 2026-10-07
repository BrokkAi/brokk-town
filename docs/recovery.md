# Recovery

SlopCop Squad assumes that any write it starts might have landed before the
process stopped. Recovery is built around that: an interrupted action becomes an
explicit, inspectable hold rather than a silent retry that could duplicate work.

## Interrupted runs

Before a unit starts work, the Squad records the dispatch identity — bot,
revision, base and head — in the worker's `run`. If the process stops while a
run is open, the next startup turns that `run` into a **recovery hold**:

```
Interrupted issue work on pr:42 has an uncertain outcome.
Check GitHub and the saved bot result; then retry pr:42 in the browser if unfinished.
Automatic replacement work is held.
```

A hold does not stop repository reads. Repo Bot keeps inventorying, so the
Squad can observe what actually landed. A hold does stop replacement work for
that unit until an operator resolves it, because only an operator can decide
whether the interrupted write succeeded.

The hold records the exact revision and phase it stopped at, plus the execution
placement and pinned runtime when one was in use. Cancellation records the cause
(operator stop, service shutdown, worker detail, or unknown) and the last known
phase in the durable log and outcome history.

## Resolving a hold

1. Check GitHub and the bot's saved state for evidence of the write.
2. If it landed, the normal inventory reconciles it and some holds clear
   themselves — an issue whose implementation pull request is recorded resolves
   its hold without operator action.
3. If it did not land, use Reconcile and retry on the case in the browser.
4. If no specific case is known, deploy the unit again from the browser.

Repo Bot's hold is special: a repair may have pushed a commit, so inventory
continues but repairs stay held until an operator inspects the branch. Losing an
inventory read is not uncertain, so an interrupted inventory never blocks later
reads.

## Retries and backoff

- A failed unit waits a short backoff before trying again.
- A case that produced no decision gets one more attempt after a short delay; a
  second incomplete attempt on the same revision retires the pull request.
- Release Bot retries a failed release after its own delay and preserves local
  work and evidence. After its attempt budget is exhausted the job stays pending;
  Retry release in the browser resets the budget.
- An agent setup failure — an unknown model, an unsupported effort — exits before
  any work and does not consume an attempt. Correct the setting and restart.

## Uncertain writes

The most consequential write is a merge. The Squad records a merge intent as
`uncertain` *before* calling GitHub, then confirms it. If the call fails, the case
is blocked with "Merge outcome is uncertain" and the next inventory reconciles the
real state instead of merging again.

Funnel writes use the same pattern: a stable idempotency marker on the source, an
intent in the precinct's state, and a retry that is recognised as the same
write.

## Startup recovery

Opening the store repairs what is safely repairable and reports the rest:

- Open runs become recovery holds.
- A Desk Sergeant turn that was gathering or answering is marked `interrupted`
  and can be re-asked.
- A known obsolete startup failure — a default-branch error written by an older
  release — is retired so the first inventory can proceed.
- Demo state written by an older release is set aside with every byte kept, and
  the demo seeds itself again. Real state is never discarded this way.

## Reopening archived cases

A terminal case is archived to the Case archive after thirty days but keeps its
identity. If the underlying issue or pull request reopens, the case is restored
before revision checks and returns with its saved decisions. Damaged history
objects fail to read rather than falling back to a fresh case. See
[storage.md](storage.md).
