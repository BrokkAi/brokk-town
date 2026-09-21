# Changelog

## 0.5.0 — 2026-09-21

- Give Town Hall its own Mayor Bot to judge arrivals awaiting a Mayoral
  decision. Start or pause it like the other bot houses; it starts paused.
- Show Mayor Bot's bulletin of merged work in Town Hall and the TUI, with
  a configurable bulletin interval (six hours by default).
- Add `townsim` to model bot scheduling, review churn, and queue throughput
  without running agents or writing to GitHub.

## 0.4.9 — 2026-09-18

- Check npm for new stable bot releases every fifteen minutes instead of every
  six hours, so a town with automatic bot updates picks a release up within
  the quarter hour.

## 0.4.8 — 2026-09-18

- Every pull request now ends merged or closed. The first review sends all
  findings to Issue Bot for one fix round; a second review that still finds work
  at or above the town's `review_close_severity` (default P2) closes the pull
  request, deletes its branch, explains why on the PR and the issue, and queues
  the issue for a fresh attempt from the current branch. Findings below the
  threshold become follow-up issues and the pull request merges.
- A revision gets two attempts at any step. The second failed reviewer or repair
  attempt retires the pull request the same way instead of parking it as
  blocked; a contributor's pull request goes to the Mayor instead.
- Review Bot picks pull requests that have never been tried before ones that
  failed, so a failing pull request no longer holds the queue.
- `review_close_severity` is a town setting in the browser, `bt settings
  --review-close-severity`, and config files. Requires Review Bot with the
  `finding-severity` capability and Issue Bot with the `requeue` capability.
- Default bot pins move to the current releases: Bug Bot 0.3.5, Feature Bot
  0.1.2, Issue Bot 0.5.4, Review Bot 0.2.4, Release Bot 0.6.1, Simplifier Bot
  0.1.1. Towns with explicit pins keep them.

## 0.4.7 — 2026-09-18

- Recover persisted Simplifier intake delays on restart without clearing failures.
- Refresh incomplete reviews and keep unrelated PRs moving after a review fails.
- Preserve interrupted worker targets and hold replacement work until recovery;
  resolve issue work from saved publication evidence and expose recovery guidance.

## 0.4.6 — 2026-09-18

- Keep Simplifier intake moving on the normal polling interval and show its
  backlog explicitly on the board instead of labeling it Unknown stage.
- Show compact active / waiting / blocked item counts inside each bot panel,
  with hover explanations, and restore Town Hall's pending-decision count.
- Put assigned work first in the inspector, preserve queue scrolling across
  updates, and keep large backlogs accessible. Active tasks show their running
  agent profile; waiting tasks show the next-run configuration.

## 0.4.5 — 2026-09-18

- Complete the Repo Bot split: repository inventory remains read-only and
  agentless, while branch health repair runs through the released Repo Bot
  worker with a configured, independently selectable agent profile.
- Add Repo Bot to the browser profile selector and inspector so operators can
  configure, save, reset and understand the repair profile directly in the UI.

## 0.4.4 — 2026-09-18

- Stop trusting an installer's exit code during an in-place upgrade. bt ships
  in an optional npm dependency, and npm exits 0 when it skips one, so a
  "successful" upgrade could leave the executable stale or absent; the service
  then restarted into nothing and took bt on PATH with it. Both install
  channels now run the binary a restart will exec and require the version just
  installed, an upgrade is only offered once this platform's payload is
  actually fetchable, and a failed upgrade reports why in the service log and
  the page instead of a tooltip.

## 0.4.3 — 2026-09-18

- Put Simplifier Bot on the map: its cottage had artwork but no house, so the
  label, status dot, queue count, profile chips and click target were missing
  and journal, inbox and courier clicks headed for it opened Town Hall. Key 8
  now visits it, and its worker walks on its own phase.

## 0.4.2 — 2026-09-18

- Launch Muse ACP against a derived config directory.

## 0.4.1 — 2026-09-18

- Draw Simplifier Bot's Clarifier cottage.

## 0.4.0 — 2026-09-18

- Route every newly observed issue and pull request through Simplifier Bot, a
  sixth paused automation house: new intake passes a durable simplifying task
  before Issue Bot, Review Bot, or Town Hall. A town-level simplifier mode
  either attaches the bot's bounded admit/decline assessment to a Mayoral
  decision or applies admissions, ignores declined PRs, and closes declined
  issues automatically.
- Show which harness, model and effort each bot runs without opening anything:
  the browser's village labels, board, compact view and town cards carry the
  profile as chips (effort shaded by level, outlined when a house overrides the
  town default), and the terminal panel's house table gains an AGENT column with
  the selected house's profile written out in full. Trailing slashes in profile
  labels are tolerated.
- Harden worker supervision and repair: coalesced worker log and phase commits,
  release ancestry settled with one comparison per poll, serialized repository
  reconciliation, repair intents settled by ancestry, unneeded repair worktrees
  released, the observed default branch tracked separately from the configured
  one, paused houses kept paused on retry, clean shutdown on SIGHUP, bounded
  persisted subprocess failure text, and the newest worker phase kept when
  progress bursts.
- Strip unearned verification from the release publisher: the tag workflow
  builds, smokes, uploads missing draft assets, publishes missing npm versions,
  and finalizes, with upload exit codes gating each step and a re-run filling
  in whatever is still missing.

## 0.3.3 — 2026-09-16

- Remove the release read-back verification tail that kept failing releases:
  staged-version waits, dist-tag reconciliation, provenance checks, and double
  asset downloads are gone, so rolling a new release over a broken one just
  publishes.
- Wait out npm staged-version conflicts instead of failing: a visible version
  record whose tarball has not propagated yet reports incomplete publication so
  submission retries instead of resubmitting an existing version.

## 0.3.2 — 2026-09-16

- Switch to tag-based releases: pushing a `v*` tag runs the shared CI checks,
  then builds the native archives and npm packages, stages a draft GitHub
  release, publishes npm platform packages before the launcher, and finalizes.
  The preflight/dispatch machinery is deleted.

## 0.3.1 — 2026-09-16

- Move the repo house out of the service and into the released `repo-bot`
  worker. Town no longer reads repository inventory from GitHub itself: the
  worker reports one complete observation over Worker Protocol v1, and Town
  applies it with the reconciliation it already had.
- Give Repo Bot the branch-health duty. When the checks on the branch a town
  covers are failing, it repairs the branch with an agent in a private worktree
  at the exact failing revision and publishes what passes the operator's
  verification command. Attempts are budgeted per revision, and the house holds
  an agent slot only while it is repairing.

- Harden release publication against npm registry visibility lag: a visible
  version record whose tarball has not propagated yet reports incomplete
  publication so submission and verification retry together instead of failing
  or resubmitting an existing version.

## 0.3.0 — 2026-09-15

- Make worker startup and write authority explicit across the browser, TUI, and
  CLI. Manual merge policy pauses Release Bot, rejects direct release starts and
  retries, excludes it from town-wide wake, and safely adopts persisted workers
  that stop while Town reconnects.
- Close declined, town-authored Feature Bot proposals at their GitHub source;
  declined outside work is still ignored rather than modified.
- Keep Start unavailable for workers that are already running or waiting out a
  scheduled poll, while retaining early retry for a failed worker.
- Keep the browser connect dialog pointed at `bt web` and preserve inspector
  scrolling while live snapshots arrive, with Mayoral actions remaining above
  asynchronously loaded task details.
- Add Cobra-style `bt` help for commands and service verbs without adding a CLI
  dependency.
- Persist deduplicated automation outcome records with task and revision
  provenance, explicit finding judgments, optional usage and cost data, and
  selectable Town Hall reports plus JSON and CSV exports. Submitted PRs remain
  distinct from confirmed merges, absent metrics remain unknown, and CSV formula
  text is neutralized.

## 0.2.0 — 2026-09-15

- Certify a completed publication once every destination and provenance check
  passes; the final `published` check no longer fails with a placeholder error.
- Lift Release Bot's exhausted attempt budget from Town through its worker API
  with `bt retry --role release` instead of editing bot state by hand.

## 0.1.2 — 2026-09-14

- Add a cross-town "Needs you" inbox to the browser: pending Mayoral decisions
  and stuck work from every town, longest wait first, with a jump to the exact
  Town Hall or house and in-place admit/decline. Sidebar towns and overview
  cards now show how many decisions each town is waiting on.
- Start the town service on demand from any `bt` command and register it with
  the login session (launchd or systemd --user) so it survives crashes and
  reboots; `bt service` inspects, stops, restarts, or unregisters it.
- Restart in place after an upgrade through the original install channel, roll
  a stale service forward when `bt` is newer, keep the browser address stable
  across restarts, and reload the page when the service version changes.
- Keep external bots running across a Town service restart. Bot processes are
  detached with durable run handles, the next service reconnects to them before
  scheduling, `detach`-capable workers replay missed events, and only operator
  stop, town deletion, or the dispatch deadline kills a bot.
- Offer pinned Brokk Town upgrades through the browser and terminal interfaces.
- Offer newer stable bot releases as Mayoral decisions with upgrade, delay-a-day
  and decline outcomes, checked from npm every six hours, and add a per-town
  setting that pins new bot releases automatically.
- Make the external pull-request ownership policy editable in Town settings.
- Harden partial-release recovery when native archives were produced by a
  different compressor but contain identical files and executable modes.
- Fail closed when npm `latest`/`next` pointers do not match an already-present
  package version, and verify package provenance and dist-tags after publication.
- Keep completed exact-commit build evidence usable when the publisher gate
  fails, while never treating that failure as authorization evidence.

## 0.1.1

- Start pinned external bot workers through `npx`.

## 0.1.0

- Local multi-repository agent town with browser, terminal and CLI clients.
- Versioned private Unix-socket worker protocol replaces compiled-in bot libraries and CLI state coupling.
- Durable bot supervision, revision-bound review and merge gates, and isolated demo mode.
- Linux and macOS binaries for amd64 and arm64, plus the npm launcher and platform packages.
- Apache-2.0 licensing with reviewed dependency notices in every package.
- Release preparation validates all deliverables before uploads and preserves partial outcomes.

The historical npm-only `0.1.0-rc.1` bootstrap remains unchanged.
