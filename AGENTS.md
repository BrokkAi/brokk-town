# Brokk Town

Brokk Town is a local Go service with browser and CLI clients. Keep work and
network I/O off input/render loops. Both clients consume the same state and
commands. Animation follows committed events; it never triggers GitHub writes.

Keep each bot an independent project. Communicate with bot executables over the
worker protocol; never import bot Go packages. Town launches the latest stable
npm bot releases through npx, resolving an exact version for each worker lifetime;
do not package bot binaries with bt. Bot releases must retain all previously
supported worker API versions and capabilities; add breaking APIs alongside them.
Use released acp-go dependencies. Keep private worktrees, bounded
subprocess output, cancellation, exact revision checks, and durable write intents.
Never infer a clean review from zero new comments. Preserve uncertain outcomes.
Do not modify contributor branches or bypass GitHub checks and approvals.

Run go test -race ./..., go vet ./..., frontend syntax/tests, and relevant local
integration checks. Use fake GitHub and agents for write tests. Demo mode must
never invoke real agents or GitHub. Do not run live repository automation as a
development test. Keep credentials out of snapshots, logs and source control.

Keep the implementation plan in .agents/plans/town.md current. Commit coherent,
validated changes on the current branch. Do not publish a release without a
release request. Keep the application local; no hosted deployment is required.
