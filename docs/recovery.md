# Recovery

Brokk Town assumes that any write it starts might have landed before the
process stopped. Recovery is built around that: an interrupted action becomes an
explicit, inspectable hold rather than a silent retry that could duplicate work.

## Interrupted runs

Before a bot starts work, Town records the dispatch identity — bot,
revision, base and head — in the worker's `run`. If the process stops while a
run is open, the next startup turns that `run` into a **recovery hold**:

```
Interrupted issue work on pr:42 has an uncertain outcome.
Check GitHub and the saved bot result; then retry pr:42 in the browser if unfinished.
Automatic replacement work is held.
```

A hold does not stop repository reads. Repo Bot keeps inventorying, so the
Town can observe what actually landed. A hold does stop replacement work for
that bot until an operator resolves it, because only an operator can decide
whether the interrupted write succeeded.

The hold records the exact revision and phase it stopped at, plus the execution
placement and pinned runtime when one was in use. Cancellation records the cause
(operator stop, service shutdown, worker detail, or unknown) and the last known
phase in the durable log and outcome history.

## Runs on a bot over the network

Every bot runs in its own process, so Town and a bot can stop independently.
The rule is the same as for any interrupted run: a run the bot may have acted
on, whose result Town did not receive whole, becomes a recovery hold.

- **Town stops or restarts** (SIGTERM, a rollout, a pod rescheduled). Town
  cancels its runs: it asks each bot to stop the run, waits up to 30 seconds
  for the result the bot completed (its transcripts are recorded), then
  records the run as interrupted, which is a recovery hold. If Town dies
  without stopping, its connection to the bot closes, which also stops the run
  on the bot; the open `run` in `state.json` becomes a hold at the next
  startup. A restarted Town never reattaches to a run it lost.
- **A bot stops, restarts or loses its connection mid-run.** A bot that shuts
  down (a rollout, a pod rescheduled) stops its runs and reports each as
  cut short, with what it completed; a bot that dies leaves the stream without
  a result, or silent for a minute. Either way the run becomes a hold.
- **A bot answers something the contract forbids** — frames out of order, an
  oversized event, more after the result, the wrong bot's identity. The run
  becomes a hold.
- **A bot cannot be reached, or refuses the request** (wrong token, a request it
  rejects). Town learns this before the bot acts: the dispatch fails with the
  reason, the bot backs off, and no hold is recorded. Before each dispatch Town
  asks the bot for its identity, so an absent bot is caught there.
- **A transcript removal whose answer is lost** reports `uncertain`; refresh the
  Storage inventory to see what was removed.

## Resolving a hold

1. Check GitHub and the bot's saved state for evidence of the write.
2. If it landed, the normal inventory reconciles it and some holds clear
   themselves — an issue whose implementation pull request is recorded resolves
   its hold without operator action.
3. If it did not land, use Reconcile and retry on the task in the browser.
4. If no specific task is known, start the bot again from the browser.

Repo Bot's hold is special: a repair may have pushed a commit, so inventory
continues but repairs stay held until an operator inspects the branch. Losing an
inventory read is not uncertain, so an interrupted inventory never blocks later
reads.

## Retries and backoff

- A failed bot waits a short backoff before trying again.
- A task that produced no decision gets one more attempt after a short delay; a
  second incomplete attempt on the same revision retires the pull request.
- Release Bot retries a failed release after its own delay and preserves local
  work and evidence. After its attempt budget is exhausted the job stays pending;
  Retry release in the browser resets the budget.
- An agent setup failure — an unknown model, an unsupported effort — exits before
  any work and does not consume an attempt. Correct the setting and restart.

## Uncertain writes

The most consequential write is a merge. Town records a merge intent as
`uncertain` *before* calling GitHub, then confirms it. If the call fails, the task
is blocked with "Merge outcome is uncertain" and the next inventory reconciles the
real state instead of merging again.

Funnel writes use the same pattern: a stable idempotency marker on the source, an
intent in the repository's state, and a retry that is recognised as the same
write.

## Startup recovery

Opening the store repairs what is safely repairable and reports the rest:

- Open runs become recovery holds, including runs on a bot in another process
  or container.
- An Assistant turn that was gathering or answering is marked `interrupted`
  and can be re-asked.
- A known obsolete startup failure — a default-branch error written by an older
  release — is retired so the first inventory can proceed.
- State that says it is demo state, written by a release that had a demo mode,
  is refused; Town never opens or discards it.

## Reopening archived tasks

A terminal task is archived to History after thirty days but keeps its
identity. If the underlying issue or pull request reopens, the task is restored
before revision checks and returns with its saved decisions. Damaged history
objects fail to read rather than falling back to a fresh task. See
[storage.md](storage.md).
