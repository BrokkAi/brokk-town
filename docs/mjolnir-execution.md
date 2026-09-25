# Planned Mjolnir execution

[Back to Brokk Town](../README.md) · [Architecture](ARCHITECTURE.md)

This document records the operator's decisions for
[#151](https://github.com/BrokkAi/brokk-town/issues/151) and guides the implementation
under [#149](https://github.com/BrokkAi/brokk-town/issues/149). These settings are
not implemented yet; current Town dispatches use local bot workers and agents.

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
