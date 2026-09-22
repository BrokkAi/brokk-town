# Brokk Town implementation plan

## Standalone projects and explicit lifecycle (complete)

- Import eight standalone bot projects under bots/, preserving independent modules,
  CLIs, tests, packaging and source provenance. No shared bot module or cross imports.
- Build complete Town bundles from bundle.json; release projects with separate suffix
  tag workflows, independent bot versions and existing npm package identities.
- Delete TUI and Town/bot upgrade code and its dedicated tests. No migrations,
  compatibility shims or tests asserting deleted features are absent.
- Foreground service by default; -d starts a simple background process. Clients do
  not auto-start services. Remove login registration and automatic replacement.
- Start eight persistent workers per town, even when paused; schedule work separately.
  Stop workers and descendants with Town, including unexpected parent death.
- Replace Issue Bot imports with private protocol job-summary and retry operations.
  Preserve exact revisions, durable write intent and uncertain-outcome recovery.
- Validate nine modules with race tests/vet, browser checks, standalone launcher,
  packaging/license checks and isolated lifecycle integration. Commit on current
  branch; no publication, live automation, or original checkout changes.

## Validation and delivery

- Imported all eight clean source checkouts with original commit provenance.
- Removed Town's Issue Bot Go dependency, TUI, login registration, runtime version
  selection, update polling/installers and their tests. No compatibility migration.
- Added complete native/npm bundles, independent suffix release workflows and
  version validation. External publishing connections await a release request.
- Persistent processes start for every house. Sequential-job, overlap rejection,
  cancellation and parent-pipe tests pass across worker implementations. Issue Bot
  summaries/retry are covered by a real local protocol test preserving evidence.
- Copied 16 open source issues and 6 attributed discussion comments into Town
  (#96–111), with mapping in docs/imported-bot-issues.md. Originals unchanged.
- Nine-module Go race/vet validation, Python packaging/installer/license and npm
  launcher checks completed (Feature Bot's changed shutdown expectation corrected
  and its worker tests rerun). Browser: 39 tests and syntax pass. Demo foreground/
  daemon smoke and all eight real worker initialization/parent-loss checks pass;
  no real jobs, agents or GitHub automation were used for development tests.
- Built and validated all four native bundles and five npm packages from commit
  67e7262 using check-only mode; nothing uploaded. Workflow lint and final root/
  Issue Bot race tests and vet passed. Implementation committed on master.
