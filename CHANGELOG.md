# Changelog

## 0.3.1 — pending

- Show which harness, model and effort each bot runs without opening anything:
  the browser's village labels, board, compact view and town cards carry the
  profile as chips (effort shaded by level, outlined when a house overrides the
  town default), and the terminal panel's house table gains an AGENT column with
  the selected house's profile written out in full.
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
