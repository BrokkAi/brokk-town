# Changelog

## 0.1.2 — pending

- Add a cross-town "Needs you" inbox to the browser: pending Mayoral decisions
  and stuck work from every town, longest wait first, with a jump to the exact
  Town Hall or house and in-place admit/decline. Sidebar towns and overview
  cards now show how many decisions each town is waiting on.
- Offer pinned Brokk Town upgrades through the browser and terminal interfaces.
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
