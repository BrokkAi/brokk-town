# AGENTS.md

Mayor Bot is a local Go/ACP worker for Brokk Town: it judges Town Hall arrivals and writes the bulletin of user-facing changes. Keep repository and
GitHub I/O off render loops. Use exact revisions, private worktrees, bounded
output, cancellation, and durable write intents. Never infer a successful GitHub
write from process exit or a zero-length response.

Run `go test -race ./...`, `go vet ./...`, `node --test npm/bmb.test.cjs`, and
`python3 -m unittest discover -s scripts -p '*_test.py'`. Use fake agents and
GitHub responses for tests; never run live repository automation as a development
test. Demo or fixture code must not invoke real agents or GitHub.

Keep credentials out of logs, snapshots, and source control. Commit coherent,
validated changes. Do not publish a release unless explicitly requested.
