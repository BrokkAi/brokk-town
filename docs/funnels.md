# Funnels

A funnel brings work into a town from a source other than the repository's own
issues. Town has two providers: **GitHub** and **Slack**. Every funnel normalises
its source into one internal shape so the rest of the pipeline never learns where
an item came from.

Funnels are configured per town in a `--config` file under `funnels`. They are
not exposed through `bt settings`; edit the config file and restart the service.

## The normalised model

Each discovered item is a **work item**:

| Field | Meaning |
| --- | --- |
| `identity` | `{funnel, provider, item}`. The stable key for the item inside its funnel. |
| `title`, `body`, `context` | One-line title, body, and extra context lines. |
| `status` | `queued`, `working`, `blocked`, `needs_human`, `complete`, `released`, `closed`. |
| `eligible` | Whether the item may be dispatched. Terminal work must not stay eligible. |
| `priority` | Explicit `value`, `policy` and reason. Discovery order is never priority. |
| `provenance` | The identity again, plus a URL, provider revision, observation time and external state. |
| `capabilities` | What lifecycle actions the source can enact. |

## Capabilities and transitions

The model-facing action vocabulary is deliberately small:

- `claim` — take the item,
- `report_blocked` — say it cannot proceed,
- `request_human` — hand it to a person,
- `report_complete` — say it is done.

A capability is reported along with a state: `supported`, `unsupported`,
`read_only`, or `requires_mapping`. A non-supported capability must say why, so
the UI and the model can tell an impossible action from one that is merely
missing an operator mapping.

A `transitions` map turns those actions into provider operations:

```json
"transitions": {
  "working":  [{ "name": "reaction", "parameters": { "name": "eyes" } }],
  "complete": [{ "name": "reaction", "parameters": { "name": "white_check_mark" } }]
}
```

An omitted transition has no configured write. An entry with an empty action
list is a deliberate no-op. A `read_only` funnel cannot configure transitions.
`SourceAction` values are ordinary configuration; credentials must never be
placed there.

## Overlap

When several funnels match the same underlying work:

| Policy | Behaviour |
| --- | --- |
| `independent` (default) | Keep one item per funnel. Declaration order is not priority. |
| `deduplicate` | Collapse duplicates across funnels. |
| `reject` | Refuse the overlap instead of guessing. |

## Credentials

A funnel's `credentials` is a `{ name, backend }` reference, never a token.
Town resolves it immediately before each request, so rotation takes effect on the
next request without restarting, and no credential can be serialized into state
or a snapshot.

## FunnelConfig fields

| Field | Meaning |
| --- | --- |
| `id` | Funnel name, unique within the town. |
| `provider` | `github` or `slack`. |
| `location` | Provider-specific source, for example `{"repository": "OWNER/REPO"}` or `{"channel": "C0123456789"}`. Required. |
| `filter` | Provider-specific selector, for example labels. Public keys only. |
| `credentials` | Optional `{ name, backend }` reference. |
| `transitions` | Lifecycle mappings (above). |
| `read_only` | When true, the funnel may not enact any transition. |
| `enabled` | Off by default; a disabled funnel is validated but not polled. |
| `priority_policy` | Required, explicit name for how priority is derived. |
| `overlap` | `independent`, `deduplicate` or `reject`. |

## GitHub provider

The GitHub funnel reads issues through a query, an explicit label filter, or an
explicit list of issue numbers. Selected issue numbers are fetched by identity and
take precedence over the paged query, so a focused run does not depend on search
index freshness.

It maps lifecycle transitions onto idempotent label changes:

```json
{
  "id": "inbox",
  "provider": "github",
  "location": { "repository": "OWNER/REPO" },
  "filter": { "query": "is:issue is:open", "include_labels": "ready" },
  "enabled": true,
  "read_only": true,
  "priority_policy": "first-observed",
  "overlap": "independent"
}
```

With `read_only` false, a transition maps onto labels through actions such as
`update_labels` (with `add` and `remove` parameters) or `add_labels` /
`remove_labels` (with a `labels` parameter):

```json
"transitions": {
  "working": [{ "name": "update_labels", "parameters": { "add": "town-working", "remove": "ready" } }],
  "blocked": [{ "name": "add_labels", "parameters": { "labels": "town-blocked" } }]
}
```

GitHub-specific selectors (`repository`, `query`, `selected_issues`,
`include_labels`, `exclude_labels`, `selected_issues`) are validated before the
funnel is enabled: the repository must be `OWNER/REPO`, the query is one line of
at most 1000 characters, at most 100 issues may be pinned, and a mapping may not
add and remove the same label.

Provider failures are typed — `auth_failure`, `rate_limited`, `incomplete`,
`uncertain_write`, `unsupported`, `revision_conflict` — so a retry decision never
depends on parsing GitHub's error text.

## Slack provider

The Slack funnel speaks the Slack Web API directly through an injected HTTP
client. It reads channel history and thread replies, and, when the caller enables
it, reacts and writes to threads.

Capabilities are explicit local switches, not guesses from scope names:
`history`, `thread_replies`, `reactions`, `thread_writes`. A read-only funnel
needs only history and replies; enabling reactions and thread writes lets the
adapter post a reaction and a thread reply for a lifecycle transition. Slack
remains authoritative for a missing scope, and a refused scope is reported
rather than retried forever.

Every write carries an invisible idempotency marker
(`<!-- brokk-town-slack:… -->`), so a retried reaction or reply is recognised
instead of repeated. Identity mapping from a message back to its funnel is only
needed by the generic adapter methods, not by the low-level history read.

## Safety

- A funnel never stores a credential; it stores a reference.
- Public projections omit credentials entirely, including the lookup name.
- Reads are fully paginated. An incomplete read is reported as incomplete and
  never treated as deletion.
- A write whose outcome is unknown is kept as an unresolved intent and retried
  against the same idempotency marker; it is never assumed to have failed.
- The demo town uses read-only funnels with synthetic items only.
