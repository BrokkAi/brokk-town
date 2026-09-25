# Source funnels

[Back to Brokk Town](../README.md)

Optional `funnels` make issue intake source-neutral. Each named funnel declares a
provider, validated location/filter, a local credential reference, explicit
priority policy, overlap policy, and allowlisted lifecycle mappings. Credential
references are resolved by adapters at request time; secret values never enter
Town state, model prompts, events, logs, or public snapshots. Public state shows
the safe funnel configuration and each normalized item's source identity, URL,
revision, external state, eligibility, capabilities, priority policy, last sync,
and typed outcome.

GitHub funnels support query, `selected_issues`, and comma-separated
`include_labels`/`exclude_labels`. Focused selections are read by issue identity
instead of relying on search indexing. `working` and `blocked` mappings translate
the normalized lifecycle into confirmed label changes. Slack is the second real
adapter: it reads a configured channel through paginated Web API calls and maps
configured lifecycle actions to reactions plus bounded thread replies. Slack
tokens are obtained from a private resolver immediately before each request.
Read-only funnels and empty per-transition mappings stay visibly unsupported.

Funnels also recognize provider-native done signals on inbound reads. A Slack
message with a present `:white_check_mark:` or `:heavy_check_mark:` reaction is
normalized as **Done**; a GitHub issue whose state is `closed` is normalized as
**Closed**. Both remain in the durable inventory with their source provenance,
but become ineligible and leave the worker queue. A GitHub selector must include
closed issues (for example `is:issue`, not `is:issue is:open`) if Town is to
observe that transition. Done is based only on an explicit item state or reaction;
an incomplete or empty inventory never closes missing work by implication.

Lifecycle mutations use a durable intent before the provider call. A lost
response remains `uncertain`; reconciliation is read-only and absence of a
receipt never permits a duplicate label, reaction, comment, or thread reply.
Incomplete reads, partial discovery, authentication failures, rate limits,
unsupported actions, and uncertain writes remain distinct outcomes. Funnel
declaration order is never scheduling priority. Overlapping source identities
are retained, deduplicated, or rejected only according to the explicit overlap
policy. Demo and tests use synthetic or fake providers and perform no live source
or agent automation. See [the configuration example](config.example.json) for GitHub and Slack shapes.
