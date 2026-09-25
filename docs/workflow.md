# How work moves

[Back to Brokk Town](../README.md)

| House | Work and handoff |
| --- | --- |
| Bug greenhouse | Runs bug-bot's investigation and verification; observed filed issues travel to issue-bot. |
| Feature study | Runs feature-bot to discover useful new capabilities, independently review their value and feasibility, and compare existing requests; confirmed feature issues travel to issue-bot. |
| Simplifier clarifier | Reviews every incoming issue/PR for disproportionate complexity or low value, advises the Mayor, and discovers removal/replacement proposals. |
| Town Hall (Mayor Bot) | Runs mayor-bot to judge every arrival awaiting a Mayoral decision and to write the town bulletin: the feed of features gained and bugs fixed, written for users. Also displays Repo Bot reports. Paused by default; decisions wait for you until it is started. |
| Issue workshop | Runs issue-bot on eligible issues, opens implementation-ready PRs, and repairs Town-owned PR branches from review feedback. |
| Review observatory | Runs review-bot, independently checks the full change and every retained finding, then returns fixes or waits for merge requirements. |
| Release depot | Runs release-bot's batching and publishing policy. Confirmed merged/direct commits accumulate here; a published stable release ships only commits proven to be its ancestors. |
| Repo watchtower | Polls GitHub, reconciles arrivals and revisions, summarizes new commit titles, reports queue growth and blocked work. |

The first complete inventory establishes existing work without a burst of arrival
animations. Subsequent deliveries follow durable, sequenced state changes.
Animation never initiates a GitHub write. Reconnecting does not replay previously
seen deliveries. External PRs receive reviews but their branches are left to their
authors; only locally recorded Town-created branches enter automatic repair.

Every new issue and PR first waits at Simplifier Bot (including work filed by
Bug Bot and Feature Bot and implementation PRs created by Issue Bot). In the
default **suggest** mode, its bounded assessment is attached to the resulting
durable Mayoral decision: **Admit** sends the work to Issue Bot or Review Bot;
**Decline** keeps Town from acting on it. In **auto** mode, Simplifier Bot can
admit routine work and decline low-value complex work itself; a declined issue is
closed through Repo-bot, while a contributor's declined PR is ignored rather than
closed. Town's own declined implementation PR, by either the Mayor or Simplifier
Bot, is closed like one that failed its second review, and its issue starts over.
Simplifier Bot's own marked proposals do not recursively pass through intake.

**Mayor Bot** lives in Town Hall. Start it like any other house and it judges
every arrival that waits for a Mayoral decision: one worker run per arrival,
in a detached worktree at the exact revision, from the town's description of
the item (with Simplifier Bot advice or the Town review attached) and the live
GitHub source. It answers admit, decline, or, for a bot update, delay, with a
reason kept on the task; decisions go through the same path as your own clicks
and are recorded under the Mayor Bot name. A judgment that fails is retried
twice, fifteen minutes apart, then left for you. The same house writes the
**town bulletin**, shown in Town Hall under "What changed": after work merges,
at most every `bulletin_seconds` (default 21600), it summarizes the merged pull
requests for the people who use the software as features, fixes and
improvements, each citing its pull requests.

A review is bound to the exact base, head, PR description, and discussion snapshot. A suppressed
duplicate comment is still a finding to check. Complete coverage, explicit
resolution of every concern, and validation evidence are required for a clean
result. New commits invalidate review readiness.

Every pull request Town works on ends merged or closed, and nothing waits on a
person to press retry. The first review sends every finding back to Issue Bot
for one fix round. If the second review still finds work at or above the town's
`review_close_severity` (default `P2`, so P1 and P2 close), Town closes the pull
request, deletes its branch, leaves the findings on the issue, and queues the
issue for a fresh attempt from the current branch. Findings below the threshold
are filed as follow-up issues and the pull request merges. A revision gets at
most two attempts at any step: a reviewer or repair failure earns one more
attempt after a short delay, and a second failure retires the pull request the
same way. A contributor's pull request is never closed by Town; it goes to the
Mayor instead. Review Bot picks pull requests that have never been tried before
ones that failed, so one bad pull request cannot hold the queue.

The default merge policy is `bot`: auto-merge eligible Town-created PRs. `manual`
leaves every merge to the operator; because the current Release Bot protocol cannot
limit its authority to publish while forbidding its release-preparation PR merges,
Town pauses Release Bot and rejects attempts to start or retry it under this policy.
`all` also permits eligible external PRs. Town-managed merges require current clean Town evidence, GitHub mergeability, required checks
and approvals. Town uses an expected-head squash merge, without admin bypass.
Change this at any time under **Town Settings → External contributions**, or
with `./bin/bt settings --repo BrokkAi/my-project --merge-policy manual`.
Repositories that require a merge queue or prohibit squash merging need manual
merges for now. GitHub is the final authority at write time.

## Feature discovery

Feature-bot uses its own effective ACP harness, model, and effort, plus the town's
optional verifier, with a private workspace and durable publication state.
It proposes scoped features with user value, repository evidence and acceptance
criteria. A separate review rejects duplicate, already implemented, rejected or
uncertain proposals before filing. Its verifier receives `FEATURE_COMMIT` and
`FEATURE_FINDING`; shared verification commands must support the selected bot's
environment. New towns keep feature discovery paused until started.

## Automation outcomes

Town Hall's **Automation outcomes** report separates worker attempts, filed
findings, submitted implementation PRs, repository-confirmed merges, repair
rounds, blocked or abandoned work, and verified releases for a selectable
period. A submitted PR is an artifact rather than an accepted fix. Operators
can mark each filed finding useful or a false positive only by adding an
explanation. Records retain task and revision provenance and show elapsed time,
token usage, and cost as unknown when the worker does not provide them. The CSV
export includes the repository on every row for comparisons across runs and
towns.
