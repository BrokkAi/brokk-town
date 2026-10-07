# Release Bot

Watch unreleased commits and publish a verified release.

`release-bot` is a Go package of the
[SlopCop Squad](https://github.com/BrokkAi/brokk-town) module. Release Bot staffs
the **Release** unit (role key `release`), and the Squad runs it in process, so
there is no standalone binary, server or release tag to install. See
[../../docs/bots.md](../../docs/bots.md) and
[../../docs/workflow.md](../../docs/workflow.md) for the cadence in context.

## What it does

A release is four steps, run by the agent under the bot's gates:

1. **prepare** — build the change set for the unreleased commits, using the
   repository's own release procedure. The prepared commit and tag are binding.
2. **preflight** — prove publishability without publishing: build and package for
   every destination, confirm the version is available, and confirm the actual
   publishing identity has permission. A failed check stops the release.
3. **publish** — run the documented publication procedure and reconcile any
   partial result from an earlier attempt rather than republishing blindly.
4. **verify** — independently check the published tag, required workflow runs,
   release assets and registry artifacts before recording success.

### Cadence

- a **daily** deadline after which unreleased commits are released;
- a **burst** of commits inside a window may trigger an early release, provided
  the **minimum gap** since the last release has passed and the branch has been
  **quiet** long enough;
- between those rules, a short read-only **triage** session decides whether the
  unreleased range contains anything users need promptly. Triage never blocks the
  daily deadline.

A failed attempt preserves local work and its failure evidence for the retry
delay, and the attempt budget bounds repeated failures. `release_trigger_ignore`
lets a repository list paths that should not, by themselves, start a release.

## What it never does

It does not weaken checks, force-push, rewrite or move a published tag, delete a
published release, or change the prepared commit during publication. A source,
test, build or workflow repair returns to preparation rather than being made in
the publication step.

## Configuration

The Squad fills this configuration from the precinct's agent profile and the
Release unit's policy in the browser. The package's own fields are:

| Field | Meaning |
| --- | --- |
| `remote`, `branch`, `directory`, `state_directory` | Private checkout and state. |
| `instruction_files` | Repository instruction files handed to the agent. |
| `initial_ref` | Explicit released commit when the repository has no release to discover. |
| `agent` | ACP command, environment, model and effort. |
| `github.host`, `github.workflows`, `github.assets` | Required workflow names and asset patterns. |
| `poll`, `daily`, `minimum_gap`, `quiet`, `burst`, `burst_window` | The cadence above. |
| `triage`, `triage_timeout` | Whether and how long the read-only triage session may run. |
| `timeout`, `verification_timeout`, `retry_delay`, `attempts` | Attempt budgets. |
| `preflight`, `verify` | Operator commands for publishability and verification. |
| `release_trigger_ignore` | Repository-relative paths that do not by themselves start a release. |
| `notify`, `notify_timeout` | Optional local command run on a verified release or an exhausted budget. The Squad does not pass this. |

The Squad's policy mapping is `attempts`, `verify` and the release fields
(`daily_seconds`, `minimum_gap_seconds`, `quiet_seconds`, `burst`,
`burst_window_seconds`, `triage`, `preflight`, `verification_timeout_seconds`,
`workflows`, `assets`). Defaults: poll every 5 minutes, daily 24h, minimum gap
2h, quiet 15m, burst 5 in 2h, a 2-hour attempt budget, a 30-minute verification
timeout, a 15-minute retry delay, 3 attempts, and triage on with a 10-minute
bound.

The embedded skills the agent follows are in [skills/](skills/).

## Development

```sh
go test -race ./bots/release-bot/...
go vet ./...
```

Tests use temporary repositories and simulated registry and GitHub outcomes. They
never publish a real release or package.

## License

Apache-2.0. See [LICENSE](../../LICENSE) and [NOTICE](../../NOTICE).
