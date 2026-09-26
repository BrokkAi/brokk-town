# Mjolnir execution

[Back to Brokk Town](../README.md) · [Architecture](ARCHITECTURE.md)

This document records the operator's decisions for
[#151](https://github.com/BrokkAi/brokk-town/issues/151) and guides the implementation
under [#149](https://github.com/BrokkAi/brokk-town/issues/149). Town now caches
Mjolnir's launch options and saves independent town/bot execution selections.
PR review, independent certification, and repairs from verified review feedback
can run through `mj acp`. Each task uses an exact private checkout and a selected
target runtime, and its answer must match retained remote evidence. Other agent
duties remain held when assigned to Mjolnir. Repo Bot continues inventory reads.

## Connect and select

Use `mj api-info` to find the daemon's API base URL and token file. That command
may start Mjolnir. Catalog reads never start it; an explicitly dispatched managed
task checks `mj api-info` before launching the ACP adapter. Set these variables
on the Town service, then start Town normally:

```sh
export BT_MJOLNIR_API_URL=http://127.0.0.1:3765/api/v1
export BT_MJOLNIR_TOKEN_FILE=/path/reported/by/mj/api-info/api-token
bt
```

Both values are required. The listener must be on a loopback address; HTTP and
HTTPS with a normally trusted certificate are supported. Town does not follow
redirects or send this request through an HTTP proxy. The token is read from its
file for each request and never enters Town's state, catalog, logs or clients.
Changing these environment variables requires restarting Town.

Town runs `mj` from PATH by default. `BT_MJOLNIR_COMMAND` can supply a JSON array
such as `["mj", "--instance", "town"]`. The command's API URL and token-file
path must match the configured connection before Town can create a session.
Keep any `MJ_CONFIG_DIR` and `MJ_DATA_DIR` overrides on the Town service as well.

Configure one single-repository bundle in Mjolnir whose primary repository maps
to the town's GitHub repository. Initialize a prompt-free session on the chosen
target/profile and select its known runtime in Town's settings, or with
`bt execution --repo OWNER/REPO --role review --runtime-session SESSION_ID`.
Model and effort are applied and confirmed on each target session before its
first prompt. Unknown runtime identity, a missing bundle, or changed revision
holds the work without starting a local harness.

Town reads `GET /api/v1/options` in a background task, checks API version 1,
and caches only the public profile, target, bundle, host and default fields.
Requests have a five-second timeout and a 1 MiB response limit. The catalog
refreshes every minute and on request. Failed reads retain the last successful
response, mark it stale, and expose a short actionable error. The cache survives
restarts and is scoped to the connection; changing the daemon connection does
not reuse another daemon's catalog. Viewing a town never waits for Mjolnir.

```sh
bt execution                   # current cached options
bt execution --refresh         # queue a background refresh
bt execution --json            # the same catalog the browser reads
bt execution --repo OWNER/REPO --target TARGET_ID --profile PROFILE_ID
bt execution --repo OWNER/REPO --role review --local-execution
bt execution --repo OWNER/REPO --role review --inherit-execution
```

In a town's settings, **Execution location** saves placement separately from the
coding harness. Pickers contain the IDs the daemon reports, including implied
local targets. A single local target/profile adds no picker; a single value in
either picker is implicit. Saved missing targets and daemon failures remain
visible. Unavailable targets retain their reason and may still be selected:
the reported availability is advisory, never permission to start a session.

Config files store a nullable `execution` selection containing only `target_id`
and `profile_id`, and `bot_execution` overrides keyed by bot role. Missing or
null town execution means direct local execution. An absent bot override inherits;
an empty pair explicitly runs locally even when the town default uses Mjolnir.
Both IDs are required for a Mjolnir selection. For example:

```json
{
  "execution": {"target_id": "builder", "profile_id": "codex"},
  "bot_execution": {"review": {"target_id": "", "profile_id": ""}}
}
```

The API accepts `POST /api/execution` with `town`, optional `role`, and a required
`selection` (the ID pair, or `null` to reset/inherit). New pairs must exist in the
cached catalog. Local agent profiles remain independent. Selecting Mjolnir never
runs a local harness as a fallback, consumes retry budget, or authorizes a merge.
Direct local work already in progress keeps the placement captured at dispatch.
Demo mode never reads the Mjolnir token/cache or contacts its daemon.

## Model and effort discovery

The CLI uses the same choices and settings APIs as the browser:

```sh
bt choices --repo OWNER/REPO --role review --json
bt choices --repo OWNER/REPO --role review --model MODEL_ID
bt settings --repo OWNER/REPO --role review --model MODEL_ID --effort EFFORT_ID
```

For a saved Mjolnir selection, **Load available choices** reads
`GET /api/v1/profiles/{profile_id}/config`, passing the selected model when
asking for its effort choices. The request uses the same authentication,
five-second timeout, cancellation and 1 MiB response limit as the launch catalog.
Town does not install or start a local harness for this read; Mjolnir owns
its profile discovery. Missing profiles,
unavailable runtimes, authentication failures and malformed responses produce
an error; they never become an empty successful catalog. Empty advertised lists
mean the profile offers no selectors.

The saved execution profile chooses the Mjolnir harness. The model and effort
fields retain their existing independent town/bot inheritance. While Mjolnir is
selected, settings accept model/effort edits and preserve the saved local
command and registry version; attempts to change a local harness definition
are refused with instructions to choose an execution profile instead. Switching
back to direct local execution restores those local controls and pins.

Discovery is not runtime readiness or a runtime pin. Mjolnir's merged session
API now reports a target-owned runtime receipt and accepts an expected identity
at launch. Town reads, selects and checks those receipts before guarded dispatch.
A mutable profile ID or options revision is never a runtime pin. A changed
identity requires an explicit operator update; Town must not copy Mjolnir's
launch definitions into its local registry.

Initialize a bundle-backed session in Mjolnir on the desired target/profile
without a task prompt, then select its runtime in **Execution location** or:

```sh
bt execution --repo OWNER/REPO --role review --runtime-session SESSION_ID
```

Town reads that session's public receipt and requires matching placement, live
idle readiness and a known runtime identity. This read never starts a local
harness or prompts the discovery session. The operator owns that session's
lifecycle. The browser and CLI both use `POST /api/execution-runtime` with `town`,
optional `role`, and `session_id`. Discovery errors leave the saved pin intact.
Demo refuses the operation without contacting Mjolnir.

Pins are stored by target/profile pair in `execution_runtimes`, including the
source session and public target version evidence. Roles using the same pair
share that pin. A different pair needs its own pin; direct local overrides use
their existing local harness settings. Catalog refreshes cannot update pins.
Explicit replacement affects future work; dispatched configurations and restart
recovery keep their captured receipt. Model/effort remain independent choices.

## Placement

A town has a default execution target. Each bot can override that target, so a
reviewer can run on a build machine while other houses use the town default.
An absent override inherits the town default. The default itself may be unset,
which preserves existing local execution. An explicit local override must remain
distinguishable from inheritance when the town default names a remote target.

Target selection is independent of the agent model and effort: changing a model
must not accidentally discard a target override, and resetting the target must
not discard a bot's agent profile. Store target references, not daemon-owned
connection details or credentials. Freeze the effective target with each dispatch,
alongside the agent profile and exact revision; editing settings affects queued
work, not a running session or its recovery record.

## Capacity

Mjolnir owns execution capacity for all Mjolnir-backed work, including its local
targets. Town shows Mjolnir's reported capacity and capacity waits read-only;
operators change those limits in Mjolnir. Town must not apply its local
`max_workers` setting as a second execution-capacity knob to those sessions.
If capacity information cannot be read, show it as unavailable, not zero or free.

Town still owns which tasks are eligible, each bot's serialized work, pause and
stop controls, quiet hours, and Town's work budgets. Direct local execution
continues to use Town's `max_workers` limit. A mixed town must label which owner
controls each kind of execution, and bounded submission/retry must not create
duplicate sessions while Mjolnir is full or unreachable.

## Implementation dependencies

| Issue | Design constraint |
| --- | --- |
| [#150: target settings](https://github.com/BrokkAi/brokk-town/issues/150) | Town default plus independent per-bot override, explicit inheritance/local distinction, immutable dispatched target, and compatibility for existing local configurations. |
| [#152: options](https://github.com/BrokkAi/brokk-town/issues/152) | Read daemon profiles, targets, defaults and availability in the background; retain the last catalog with a visible stale/error state. Do not block rendering or silently replace an unavailable saved target. |
| [#153: runtime and model choices](https://github.com/BrokkAi/brokk-town/issues/153) | Mjolnir prepares the selected target's runtime. Discover choices there, preserve saved version pins, and report unavailable runtime/authentication without silently switching to local execution. |
| [#154: workspace lifecycle](https://github.com/BrokkAi/brokk-town/issues/154) | Define checkout ownership, exact revision preparation, cancellation, reconnect and retention before dispatching remote work. Save target/session identity durably and reconcile uncertain submissions before retry. Avoid a new persistent workspace or bundle per attempt. |
| [#155: evidence](https://github.com/BrokkAi/brokk-town/issues/155) | Retrieve remote review/repair evidence with the dispatched session and revision identity. Missing or unsupported artifacts are unavailable evidence, never an empty successful review or verified repair. |

The execution contract must keep Town's existing invariants: unchanged tracked
files and HEAD for reviews; private branches, verified nonempty changes and no
history rewriting for repairs; durable write intents and exact GitHub checks
before publication. Decide and test where the checkout and bot run before
treating a remote ACP agent as equivalent to local execution. Keep bots separate
executables communicating through the worker protocol.

Develop this path with fake daemons, GitHub and agents, including interruption,
capacity waits, missing artifacts, and restart recovery. Demo mode must never
contact Mjolnir or launch an agent. A real remote acceptance demonstration is
separate from development tests and must not be claimed from fixture coverage.

## Artifact evidence client (#155, first part)

Town's internal Mjolnir client can read a saved session's identity, an exact-base
JSON diff, a repository-relative file and a page of transcript evidence. It can
also request a Git bundle export. These calls are for background execution work;
they are consumed by the review/repair dispatch path, never by rendering.

The client retains the existing loopback-only connection, per-request token read,
API version check, no proxy/redirect behavior and demo isolation. Artifact reads
have a 30-second deadline; a bundle export has two minutes because it can first
checkpoint the session. Bounds are 1 MiB for session/transcript JSON, 16 MiB for
diff JSON or file bytes, and 32 MiB for a bundle. Oversize, partial, missing or
unsupported responses are errors, with no partial evidence returned. A 409
refusal remains distinct from a missing artifact or daemon failure. Raw daemon
errors are never copied into diagnostics.

Session checks compare the saved session, workspace, bundle, target and profile
IDs. They do not infer runtime identity from a profile or idle status. Diff reads
require a full commit ID and matching returned base. Review-tree checks require
unchanged HEAD and an empty patch; they do not supply a review verdict or replace
a complete independent review receipt. Repair-history checks require a different
HEAD, a nonempty patch and affirmative ancestry. An older worker that omits
ancestry is unsupported evidence, not a successful check.

Transcript pages advance by sequence, including updates to an existing item.
They keep every item sharing a page's boundary sequence even when the daemon
returns more than the requested 200 items, within the byte limit. Missing or
skipped evidence is refused. Only documented item fields are retained; raw
harness-shaped bodies are ignored. Patch and transcript text are private and
excluded from their JSON projections. File and bundle bytes remain private
inputs to validation and eventual durable artifact storage, never snapshots.

The only export operation exposed by this client requests a bundle, not a
branch push. An interrupted export retains an unconfirmed checkpoint outcome
and is never automatically retried. Callers must still import and verify the
bundle in a private checkout, check its exact head and ancestry, run the operator's
verification command, obtain independent review, and use the existing publication
gates. Fake-daemon and disposable-Git tests exercise artifact reads and bundle
verification. The following sections describe the durable lifecycle and its
worker integration; fixture coverage alone does not establish real-target
acceptance.

## Remaining execution contract

The updated audit uses Mjolnir source at
[`adf1304`](https://github.com/BrokkAi/mjolnir/tree/adf1304e533ba5564c2225763c136606d7649ffd).
The former upstream blockers are merged:
[#1164](https://github.com/BrokkAi/mjolnir/pull/1164) implements exact bundle
checkouts, and [#1165](https://github.com/BrokkAi/mjolnir/pull/1165) implements
runtime receipts and expected-identity enforcement. At the September 26 audit,
the latest published Mjolnir release was v2.22.0, which predates these changes.
Subsequent container acceptance found runtime inspection selecting the shell
launcher; upstream [#1167](https://github.com/BrokkAi/mjolnir/pull/1167) fixes that
for v2.23.1. Use a build containing that fix for container execution. Older
daemons that cannot supply the required receipts are refused.

- `mj acp` accepts target, profile, bundle, workspace and an exit policy, but
  does not pass model or effort at creation. The HTTP API supports those
  selectors and configuring them before prompting. Exact checkout now uses
  `checkout: {repository_id, commit, branch}` on HTTP creation or the ACP flags
  `--checkout-repository`, `--checkout-commit`, and `--checkout-branch`. It is
  distinct from `launch_base`, which still only records the diff baseline.
  `--expected-runtime-identity` forwards the saved runtime constraint; Mjolnir
  enforces it before prompting and during worker replacement/recovery. The
  adapter does not allow attaching a session created outside its process.
  Its ACP session ID already equals the Mjolnir session ID.
- Session creation returns a server-assigned ID. Town needs to save a dispatch
  intent before submission and its session receipt before prompting. A lost
  creation receipt must remain uncertain, with no automatic resubmission.
  Reliable recovery needs a caller-supplied identity that the daemon can look up;
  a matching title alone is not proof of ownership.
- Choose Mjolnir-owned target checkouts. Reuse an operator-configured bundle
  for the repository and a stable Town workspace; do not create quick bundles
  from each transient local worktree. Every session still needs its own isolated
  checkout, exact launch commit and Town-owned branch. Ambiguous or missing
  repository mappings must refuse dispatch. Keep interrupted sessions and
  artifacts until reconciliation; only then release their storage.
- `--on-exit suspend` provides a retention policy, but cleanup must be confirmed
  by the daemon. Town's current short local subprocess shutdown must not kill
  the adapter before its potentially long checkpoint completes. Do not use
  `destroy` before evidence has been retrieved and durably recorded.
- Mjolnir's public options report host availability, not numerical execution
  capacity. Town must show capacity as unknown until it is exposed. A missing
  field cannot be interpreted as free capacity.

The evidence mapping below is the required implementation boundary. GitHub
writes remain owned by Town's existing durable intents and exact-head checks.
Bots remain separate executables using their worker protocol; prompts must name
the remote checkout, never a path that only exists on Town's machine.

| Existing check | Required non-local evidence |
| --- | --- |
| Exact review checkout and unchanged HEAD | Bind the artifact to the saved session, repository and launch SHA; require `diff?base=EXPECTED_HEAD&json=true` metadata with that base, `head == EXPECTED_HEAD`, and an empty diff. Missing metadata is a refusal. |
| Reviewed tracked files unchanged | The same diff compares the expected commit to the working tree, including untracked files; reject any changes. Retrieve the structured review receipt through the artifact API and validate its exact base/head and completeness. Zero comments still prove nothing. |
| Nonempty repair and no rewritten history | Require diff metadata with the recorded base, a different head, nonempty diff and `head_descends_from_base == true`; absence of the ancestry field is unsupported evidence. |
| Verified committed repair | Export a Git bundle, validate its advertised head and ancestry against a private Town checkout, and run the configured verify command on that exact imported commit. Require clean tracked files and unchanged HEAD after verification. A patch alone cannot prove a committed repair. |
| Independent review of a repair | Start a separate exact-head review and apply the same read-only evidence checks. Preserve the returned decision and complete receipt before any publication. |
| Recovery and audit trail | Persist session/target/profile/runtime identity, launch and returned revisions, transcript reference and artifact checks before considering the attempt complete. An unavailable file, transcript, export or session remains explicit missing evidence. |
| Safe publication and merge | Push only Town-owned branches without force, then confirm GitHub's exact head. Re-run the existing base/head, review audit, checks and approval requirements before merging. A remote artifact never authorizes a GitHub write by itself. |

These are design constraints, not tests of a running remote integration. The
remaining work includes protocol changes, fake-daemon review/repair integration,
interrupted submission and retention tests, and a separately authorized real
remote acceptance demonstration.

## Launch receipt validation (#153/#154, first part)

`ReadSession` now retains the exact checkout declaration, lifecycle/readiness
fields, enforced runtime constraint and the target's runtime receipt. Runtime
receipts preserve the opaque identity, harness, platform, provenance, readable
component versions/digests, event ordinal and observation time. Unknown component
versions/digests remain null. Unknown runtime provenance remains explicit; raw
unavailability diagnostics and undocumented session/runtime fields are discarded.
The identity is compared as an opaque value, never rebuilt from component
versions or the controller's release. These reads use the existing bounded,
authenticated transport and stay offline in demo mode.

`CheckLaunchReceipt` requires a matching repository/commit/branch declaration,
a live idle session with an affirmative no-error field, and a known runtime
matching both the selected identity and the daemon's saved launch constraint.
Checking the observed identity without that constraint would allow a worker
replacement to change the runtime between lookup and prompting. Missing fields,
unfinished preparation, unavailable runtime provenance and mismatched identities
all refuse readiness. Existing artifact identity reads remain compatible with
older daemons that omit these additive fields.

This check validates launch declarations, not the current repository tree or a
completed review. A recovered session can retain its original checkout intent
while its actual HEAD has changed. Before dispatch, Town must still verify the
bundle/repository mapping, read exact-base diff evidence, save the receipt and
configure model/effort. Each accepted initialization receipt must be retained
with its run, rather than replaced by a later session lookup. Fixture tests cover
the launch guard, changed checkout evidence, retained receipt serialization,
runtime replacement, malformed/partial reads, cancellation and demo isolation.
The review/repair dispatcher consumes these guards before sending its prompt.
Unsupported duties and missing runtime selections remain held by the scheduler.

## Durable checkout lifecycle (#154)

Town's internal run lifecycle now resolves a repository against the daemon's
configured bundles and reuses one stable named workspace. It does not create
quick bundles from local worktrees. Until sibling artifacts can be verified,
the bundle must contain exactly one repository, selected as primary, with a
matching GitHub repository. Missing or ambiguous mappings refuse preparation.

A run freezes the selected target/profile/runtime, exact commit and a new
`town/RUN_ID` private branch. Before session creation it writes and syncs a private
intent, including the daemon connection identity. The session ID is saved before
readiness checks. Preparation requires matching launch receipts and an unchanged
exact-base diff. The same durable boundary supports an ACP-owned session creator;
it never attaches an adapter to a session created over HTTP.

The run ID can be submitted only once. A lost creation reply remains uncertain,
even when no session ID was received. A canceled or failed preparation retains
its intent and any confirmed identity for inspection. Reading records after a
restart never restarts work, guesses ownership from a title, or switches daemons.

Before cleanup, the caller must finish validation and save its complete private
evidence through the run lifecycle. Town syncs that file and its digest before
recording a destroy intent. Destruction is confirmed only when the saved session
returns 404. An interrupted acknowledgement remains uncertain; reconciliation
only reads the saved session and never resubmits destruction. Missing or changed
local evidence prevents cleanup. Bundle and workspace records are retained for
reuse; successful session environments are removed after evidence is saved.
Interrupted sessions are retained for operator reconciliation. No source or
contributor branch is deleted.

Fake-daemon tests cover exact-revision refusals, readiness, lost receipts,
restart recovery, workspace reuse, configuration isolation, evidence retention
and cleanup confirmation. The worker/ACP integration uses this same lifecycle.

## Complete remote evidence (#155)

`Run.Collect` accepts only one finished prompt's bounded answer. It retains a
private transcript, checks that the final agent message matches that answer,
and refuses a changing or incomplete transcript. The session must still report
the original runtime initialization and guarded launch identity. These checks
prove provenance and checkout state; the bot still parses its complete review
receipt and Town independently certifies findings before merging.

| Existing evidence gate | Managed equivalent |
| --- | --- |
| Exact starting HEAD and private worktree | Saved checkout intent, launch receipt and pre-prompt diff at the exact starting commit. |
| Review leaves HEAD and source unchanged | Diff from the dispatched commit must have the same HEAD and no patch, including untracked work; checked again after transcript collection. |
| Complete investigation and independent finding verification | Separate prompts/sessions and the existing strict bot receipts; an empty diff never supplies a verdict. Each ACP answer must match its retained transcript. |
| Repair is committed, nonempty and preserves history | Affirmative remote ancestry, changed HEAD and nonempty patch; a second diff from that HEAD must be empty. |
| Verified repair commit available for publication | Durable export intent, bounded Git bundle, exact advertised commit, local Git bundle verification and ancestry in a Town-owned private checkout. No branch export or remote push is requested from Mjolnir. |
| Operator verification | Run locally against the exact reviewed/imported commit; reject changed HEAD, branch or tracked source afterwards. Repair verification also rejects untracked files. It need not run on the agent's target to verify the same Git commit. |
| Safe publication and merge | Existing ownership, current PR/base/head/discussion, durable write-intent, independent review and GitHub checks remain required. Artifact evidence alone cannot publish or merge. |
| Recovery and cleanup | Retain transcript/diff and repair bundle privately with integrity digests before cleanup. An interrupted export remains `exporting`, since the daemon may have checkpointed; never automatically export again. |

`ImportRepair` fetches objects from the saved bundle without updating a
contributor or remote-tracking branch, then fast-forwards the caller's private
branch. It disables Git hooks during import. Corrupt bundles, unadvertised
commits, rewritten history, empty changes, dirty trees and failed or mutating
verification all refuse publication. Root tests exercise review/repair artifact
collection against fake APIs and repair imports using temporary Git repositories.

## Worker dispatch and recovery

Review Bot advertises the additive `remote-agent-v1` capability. Town supplies a
private mode-0600 callback socket for one dispatched head. The bot sends inline
review context instead of a controller filesystem path, retaining its existing
investigation, independent verification, strict receipts and GitHub write gates.
Each callback creates a separate guarded Mjolnir session. Town also runs its
independent merge certification through this path. Old workers remain compatible
with local execution and are refused before managed work if the capability is
absent. The worker's optional `dry_run` flag preserves all review checks without
publishing a GitHub review or producing merge authority.

Runs and evidence are private files under
`towns/TOWN_KEY/extensions/mjolnir/ROLE/RUN_ID` in Town's state root. Successful
dispatches retain their evidence, then confirm session destruction. An error,
lost response or cancellation keeps the run and blocks further managed work for
that duty. The error identifies the run and any known session. Inspect `run.json`
and the session in Mjolnir before resolving the outcome; restarting Town never
retries creation, prompting, export or cleanup from an uncertain record. There
is no automatic repair of these records or UI action that discards them.
