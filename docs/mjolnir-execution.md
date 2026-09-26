# Mjolnir execution

[Back to Brokk Town](../README.md) · [Architecture](ARCHITECTURE.md)

This document records the operator's decisions for
[#151](https://github.com/BrokkAi/brokk-town/issues/151) and guides the implementation
under [#149](https://github.com/BrokkAi/brokk-town/issues/149). Town now caches
Mjolnir's launch options and saves independent town/bot execution selections.
Running agents through Mjolnir remains planned: selecting a Mjolnir target holds
that bot's agent work until remote checkout and evidence support are available.
Repo Bot continues its inventory reads without starting a repair agent.

## Connect and select

Use `mj api-info` to find the daemon's API base URL and token file. That command
may start Mjolnir; Town itself never starts it. Set these environment variables
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

Discovery is not runtime readiness or a runtime pin. Mjolnir's public profile
and session responses currently omit the resolved harness version. Town must
not silently treat a mutable profile ID or the options projection revision as
an immutable runtime identity. Before enabling dispatch, the contract needs a
daemon-owned runtime identity which can be selected, checked at launch and
recorded with the session. A changed identity must require an explicit operator
update; Town must not copy Mjolnir's launch definitions into its local registry.

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
they are not connected to rendering or dispatch yet. Selecting Mjolnir still
holds agent work while the remaining launch and recovery contracts are developed.

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
verification. Durable session/artifact receipts, bot integration and complete
remote review/repair dispatch remain work under #154/#155; this client does not
complete the real-target acceptance in #149.

## Remaining execution contract

The following audit uses Mjolnir source at
[`4d6c0ce`](https://github.com/BrokkAi/mjolnir/tree/4d6c0ce9c58efe76cdaa098de1b735eded5e871b).
It records requirements for #153–155; it does not claim those issues or the
real-target acceptance in #149 are complete.

- `mj acp` accepts target, profile, bundle, workspace and an exit policy, but
  does not pass launch base/branch, model or effort. The HTTP API supports those
  selectors, but `launch_base` only sets the diff baseline for a fresh bundle
  checkout; it does not move HEAD to that commit. A new exact-checkout contract
  and supported ACP launch path are tracked in Mjolnir #1162. The adapter does
  not currently allow attaching a session created outside its process. Its ACP
  session ID already equals the Mjolnir session ID. Runtime identity and expected
  identity enforcement remain upstream work in Mjolnir #1163.
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
