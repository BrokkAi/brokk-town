# Mjolnir execution

Mjolnir is an optional daemon that runs agent work on a target you own — another
machine, a container, a specific runtime — instead of on the Town host. Town
treats it as a placement for *supported* duties. Everything else keeps running
locally, and a duty that cannot be placed is held rather than quietly run
somewhere else.

## Connecting

Town reads two environment variables when the service starts:

| Variable | Meaning |
| --- | --- |
| `BT_MJOLNIR_API_URL` | Mjolnir's API base URL. Must end in `/api/v1` and use a loopback address. |
| `BT_MJOLNIR_TOKEN_FILE` | Path to the file holding the API token. |

Both are required to enable Mjolnir. `mj api-info` prints the values for a local
daemon. The connection is deliberately restricted to loopback, rejects redirects
and proxies, and never falls back to the public internet. Demo mode ignores the
connection entirely.

```sh
export BT_MJOLNIR_API_URL=http://127.0.0.1:PORT/api/v1
export BT_MJOLNIR_TOKEN_FILE=$HOME/.local/state/mjolnir/token
bt -d
```

## Catalog

Town caches Mjolnir's launch options under the state directory and refreshes them
on request or on a short interval. The catalog lists:

- **targets** — where a run can happen, with availability and an unavailable
  reason;
- **profiles** — the agent profile a run uses;
- **bundles** — the repositories a workspace needs.

```sh
bt execution                # cached catalog
bt execution --refresh      # ask the daemon for a fresh catalog
```

The cache is scoped to the connection that produced it, so switching daemons
never reuses another daemon's options.

## Selecting execution

A selection is a `{ target_id, profile_id }` pair. An empty pair is direct local
execution.

```sh
bt execution --repo OWNER/REPO --target TARGET --profile PROFILE          # town default
bt execution --repo OWNER/REPO --role review --target T --profile P      # one house
bt execution --repo OWNER/REPO --role issue --local-execution            # force local
bt execution --repo OWNER/REPO --role issue --inherit-execution          # drop override
```

Saving a selection only stores catalog references; availability stays advisory.
Selecting a target does not start it.

## Runtime pinning

Before a house can be dispatched to a managed target, Town pins the exact runtime
from an initialized session on that target:

```sh
bt execution --repo OWNER/REPO --role review --target T --profile P --runtime-session SESSION
```

The pin records the runtime identity and the session it came from, and it is
validated on load. A house with a managed selection and no valid pin shows a hold
("Select a known Mjolnir runtime before dispatching this bot") instead of
dispatching. This prevents a dispatch from silently landing on a different
runtime than the one you reviewed.

## What can run remotely

| Duty | Managed support |
| --- | --- |
| Review of a pull request | Supported. |
| Issue repair (fixing a Town-owned branch from review feedback) | Supported. |
| Other agent duties | Held with an explicit message; select direct local execution. |

When a managed duty starts, Town builds a run plan that requires the exact
commit, a private `town/<id>` branch, a resolved placement and a pinned runtime,
plus the selected model and effort. Loose references are refused.

## Evidence and cleanup

A managed run is not trusted on its word. Town reads back:

- a bounded session identity and state,
- the exact diff from the run's base,
- repository-relative files it needs,
- transcript pages up to completion,
- an exported Git bundle for repair work.

Evidence is size-bounded and validated against the expected session, commit and
ancestry before it is used. A repair bundle is imported and checked against the
base and head before Town treats the fix as delivered. Private evidence is saved
with a digest so a later read can prove it is unchanged.

Run records are durable. A run that is interrupted mid-flight is retained with
its session identity and becomes `uncertain` rather than being replayed;
cleanup reconciles the real session before destroying it.

## Troubleshooting

- **Nothing is listed.** Confirm both environment variables, that the URL ends in
  `/api/v1` and is loopback, and that the service was restarted after you set
  them.
- **A house is held.** Either the duty has no managed support, or no runtime is
  pinned. Check the hold text on the board.
- **Choices are empty.** For a managed selection, model and effort come from the
  daemon's profile API; refresh the catalog and re-select the profile.
