# Storage and retention

Town keeps two kinds of local data: the committed snapshot, which is small and
must be kept, and per-run artifacts — checkouts, worktrees, bot state and
transcripts — which accumulate. `bt storage` inventories the artifacts and can
remove the ones that are provably safe to remove.

## Inventory

```sh
bt storage --repo OWNER/REPO
bt storage --repo OWNER/REPO --older-than-hours 24 --json
```

The inventory reports, per house, bytes, files and artifact count, plus one entry
per artifact: a stable opaque ID, its town-relative path, its kind, its role, any
owning task, size, file count, age, and whether it is eligible for removal with
the reason why.

The default minimum age is 168 hours (seven days). It is a minimum, not a
guarantee: an artifact must also pass the rules below.

An inventory can be **incomplete** — a timeout or an unreadable path. An
incomplete inventory never marks anything eligible.

## What is eligible

- **Transcripts** are eligible only when a completed-run receipt exists, the
  owning task is terminal, the receipt is older than the threshold, and the
  transcript still matches its recorded SHA-256 and size. Changed, unknown or
  unmapped transcripts are retained.
- **Repair worktrees** are eligible only when they reconcile with a confirmed
  repair at its exact saved commit and the working tree is clean, including
  tracked, untracked and ignored files.

Everything else — worker-owned data, active state, unmapped evidence — is
retained with a stated reason.

## Cleanup

```sh
bt storage --repo OWNER/REPO --cleanup ID1,ID2
```

Cleanup takes the opaque IDs from a fresh inventory, never a path. It will not
start while a **cleanup hold** applies:

- any worker is enabled, running, or has an open run or agent;
- any worker has a recovery hold;
- any merge or write intent is unresolved;
- any funnel write intent is unresolved;
- an issue request is queued or uncertain;
- the demo town (cleanup is disabled).

While cleanup runs the town takes a reservation so new work cannot start against
an artifact being removed. Each artifact is re-measured and re-verified under
that reservation; a changed artifact is retained instead of removed. Task,
ownership, intent and worker identities are never removed.

Removals are recorded as receipts: `removed`, or `partial` when the directory was
removed but, for example, its branch could not be deleted. A write whose outcome
cannot be confirmed is reported as uncertain and its identity is kept.

## History

Archived tasks are separate from artifact cleanup and are never deleted by it.
Town archives closed, merged and shipped tasks after thirty days, at most 500 per
inventory, into immutable objects with a digest index:

- `towns/<key>/history/` holds the objects.
- Active, blocked, pending-decision, recovery, follow-up and source-cursor tasks
  stay hot.
- Reopened tasks are restored before revision checks and keep their previous
  decisions and identities.
- A damaged object fails to read rather than silently becoming a fresh task.

`bt history --repo OWNER/REPO` lists the archive; `--task ID` reads one task,
`--after` paginates, and `--limit` (1–100) sets the page size.

## Backups

Copy the whole state directory to back up a town. It contains the snapshot, the
history index and objects, per-house bot state, and the checkouts. Restoring is
the reverse: stop the service, put the directory back, start the service. The
single-writer lock and the per-file permissions make a partially copied directory
detectable rather than silently corrupt.
