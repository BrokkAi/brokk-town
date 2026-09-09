# Licensing and third-party notices

Brokk Town uses [Apache-2.0](../LICENSE). [NOTICE](../NOTICE) identifies the project
and adapted code. [THIRD_PARTY_NOTICES.txt](THIRD_PARTY_NOTICES.txt) includes the
complete selected Go dependency graph, plus Go runtime and Unicode data notices.
The four bot modules, acp-go, uniseg, x/sys and x/term are pinned in `policy.json`.

Run `python3 scripts/licenses.py` to verify the module inventory, exact versions,
legal file hashes, Go version, and generated report. For a reviewed dependency
change, update the policy deliberately and run `python3 scripts/licenses.py --write`.
The checker rejects unreviewed modules, version changes, replacements, altered
legal texts, and stale reports. It does not classify or approve new licenses.

Preserve LICENSE, NOTICE, and this directory in source and binary distributions.
No native release or npm distribution is published by this initial implementation.
Separately installed coding agents and interpreters have their own terms.
Original generated artwork is documented in [ARTWORK.md](../docs/ARTWORK.md).
