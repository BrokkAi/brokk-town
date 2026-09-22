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
  version validation. All 45 external publishing connections were repaired and
  verified on 2026-09-22 as part of the authorized release.
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

- Published the consolidation to Brokk Town master, verified all eight destination
  directories, updated source README notices/descriptions/homepage links, and
  archived all eight original repositories at the user’s explicit request.

## Town 0.6.0 release (complete)

- User authorized a Town release and local installation update, plus repair of
  publishing trust for all eight bots. Publish only through GitHub Actions.
- Fixed Feature Bot shutdown using a separate bounded drain context after job
  cancellation; worker race tests passed 20 repetitions and vet passed.
- Advanced all eight bundled bot patch versions for their changed lifecycle code.
  Corrected the npm launcher description to browser and CLI clients.
- Town race/vet, 27 packaging tests, browser syntax and 39 browser tests pass.
- Replaced and read back all 45 npm publisher connections for Town and eight bot
  families, preserving environments and staging permissions. Each now trusts its
  release workflow in BrokkAi/brokk-town with direct publishing enabled.
- All 19 preparation CI checks passed. Tag v0.6.0-town at fe09bb7 triggered
  GitHub Actions run 35703157496; all 20 jobs succeeded. Four native bundles and
  five npm packages were submitted by Actions; npm reported processing pending.
- Installed the checksum-verified native v0.6.0 bundle in ~/.local/bin while npm
  processed its packages, and removed the superseded npm 0.5.0 installation.
  bt reports v0.6.0; all eight installed bots initialized at manifest versions
  and exited on parent loss with no jobs dispatched. Town remains stopped.

## Local startup repair after 0.6.0

- Plain installed bt failed with "read town state: invalid task kind": six obsolete
  upgrade tasks remained in three deleted local towns. Installation verification
  had missed loading the existing state.
- Backed up the local state with private permissions and removed only those six
  entries under the service lock. No product migration or upgrade code was added.
- Verified the installed binary starts using a temporary copy of the repaired
  state with all workers paused, serves authenticated HTTP 200, and shuts down
  cleanly. Actual worker settings and other saved work remain unchanged.

## Actionable attention inbox

- Replaced raw failure cards with a plain-language explanation and next step.
  Missing agent executables link directly to the affected bot’s agent settings.
- Release retry exhaustion links the previous failed GitHub workflow and offers
  the existing worker-protocol retry operation, with confirmation that it resumes
  work and may publish. Manual policy and recovery holds suppress the retry.
- Kept full errors in expandable technical details, explicit task/bot-log links,
  stable expanded state, keyboard focus, and visible request errors.
- Go race tests/vet and frontend syntax checks pass; 40 browser tests cover
  settings navigation, workflow links, canceled/accepted/rejected retry, policy
  restrictions, and uncertain-outcome guidance. No live actions used for tests.

## Town 0.6.1 release (complete)

- Released immutable tag v0.6.1-town at a733129 through GitHub Actions run
  35710266271. All 19 checks and the publishing job passed.
- Recovered an npm Linux ARM64 404 after verifying its correct publisher trust,
  then a duplicate draft caused by GitHub omitting drafts from by-tag lookup.
  Backed up and checksum-verified unpublished assets before recreating drafts;
  the release tag and source commit were unchanged.
- Fixed future draft lookup in Town and all eight standalone bot publishers,
  with mocked pagination, missing-draft, duplicate-draft, and existing-release
  tests. Root packaging tests (30) and all eight bot publisher test suites pass.
- Installed the checksum-verified native 0.6.1 bundle locally. Verified startup
  with a paused copy of current saved state, HTTP state and new inbox assets,
  and clean shutdown without dispatching repository jobs or requests.
  bt reports v0.6.1; the actual Town service is stopped and ready for the user.
