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
