# Mjolnir execution

Mjolnir is an optional daemon that runs agent work on a target you own — another
machine, a container, a specific runtime — instead of on the machine running
SlopCop Squad. The Squad treats it as a placement for *supported* duties.
Everything else keeps running locally, and a duty that cannot be placed is held
rather than quietly run somewhere else.

## Connecting

The Squad reads two environment variables when the service starts:

| Variable | Meaning |
| --- | --- |
| `SLOPCOP_SQUAD_MJOLNIR_API_URL` | Mjolnir's API base URL. Must end in `/api/v1` and use a loopback address. |
| `SLOPCOP_SQUAD_MJOLNIR_TOKEN_FILE` | Path to the file holding the API token. |

Both are required to enable Mjolnir. Their names from before the rename,
`BT_MJOLNIR_API_URL` and `BT_MJOLNIR_TOKEN_FILE`, are still read when the new
ones are unset. `mj api-info` prints the values for a local
daemon. The connection is deliberately restricted to loopback, rejects redirects
and proxies, and never falls back to the public internet. Demo mode ignores the
connection entirely.

```sh
export SLOPCOP_SQUAD_MJOLNIR_API_URL=http://127.0.0.1:PORT/api/v1
export SLOPCOP_SQUAD_MJOLNIR_TOKEN_FILE=$HOME/.local/state/mjolnir/token
scs -d
```

## Catalog

The Squad caches Mjolnir's launch options under the state directory and
refreshes them on request or on a short interval. The catalog lists:

- **targets** — where a run can happen, with availability and an unavailable
  reason;
- **profiles** — the agent profile a run uses;
- **bundles** — the repositories a workspace needs.

Execution location, in a precinct's Settings, shows the cached catalog and can
ask the daemon for a fresh one. The cache is scoped to the connection that
produced it, so switching daemons never reuses another daemon's options.

## Selecting execution

A selection is a `{ target_id, profile_id }` pair. An empty pair is direct local
execution. Use Execution location in the browser's Settings to set a precinct
default, override one unit, force local execution, or restore inheritance.

Saving a selection only stores catalog references; availability stays advisory.
Selecting a target does not start it.

## Runtime pinning

Before a unit can be dispatched to a managed target, the Squad pins the exact
runtime from an initialized session on that target. Execution location in
Settings selects that session and records the runtime identity and where it
came from.
The pin is validated on load. A unit with a managed selection and no valid pin
shows a hold ("Select a known Mjolnir runtime before dispatching this bot")
instead of dispatching. This prevents a dispatch from silently landing on a
different runtime than the one you reviewed.

## What can run remotely

| Duty | Managed support |
| --- | --- |
| Review of a pull request | Supported. |
| Issue repair (fixing a branch the Squad owns from review feedback) | Supported. |
| Other agent duties | Held with an explicit message; select direct local execution. |

When a managed duty starts, the Squad builds a run plan that requires the exact
commit, a private `town/<id>` branch, a resolved placement and a pinned runtime,
plus the selected model and effort. Loose references are refused.

## Evidence and cleanup

A managed run is not trusted on its word. The Squad reads back:

- a bounded session identity and state,
- the exact diff from the run's base,
- repository-relative files it needs,
- transcript pages up to completion,
- an exported Git bundle for repair work.

Evidence is size-bounded and validated against the expected session, commit and
ancestry before it is used. A repair bundle is imported and checked against the
base and head before the Squad treats the fix as delivered. Private evidence is
saved with a digest so a later read can prove it is unchanged.

Run records are durable. A run that is interrupted mid-flight is retained with
its session identity and becomes `uncertain` rather than being replayed;
cleanup reconciles the real session before destroying it.

## Troubleshooting

- **Nothing is listed.** Confirm both environment variables, that the URL ends in
  `/api/v1` and is loopback, and that the service was restarted after you set
  them.
- **A unit is held.** Either the duty has no managed support, or no runtime is
  pinned. Check the hold text on the unit in the browser.
- **Choices are empty.** For a managed selection, model and effort come from the
  daemon's profile API; refresh the catalog and re-select the profile.
