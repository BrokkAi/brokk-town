# Brokk Simplifier Bot

This standalone project now lives in [`BrokkAi/brokk-town/bots/simplifier-bot`](https://github.com/BrokkAi/brokk-town/tree/master/bots/simplifier-bot). Build and test from this directory; see [RELEASING.md](RELEASING.md) for its independent suffix-tag releases.

`bsb` is the complexity and value reviewer for Brokk Town. It is modeled on the
released bug-bot worker architecture and uses the shared
[ACP runner](https://github.com/BrokkAi/acp-go).

It has two Town-selected modes:

- **suggest** — attach a bounded admission/decline recommendation to a Mayoral
  decision. The Mayor remains the decision maker.
- **auto** — let Town admit routine work, decline low-value complex work, and
  close low-value complex issues without a separate Mayoral decision.

Every incoming Town issue and pull request is assessed before normal Issue Bot
or Review Bot work. The bot also performs repository scans and files simplifier
issues proposing removal or replacement of subsystems that add disproportionate
complexity or provide little value. Issues created by simplifier-bot carry a
hidden marker and are not recursively routed back through this bot.

The implementation is deliberately conservative: it must not recommend removing
security, privacy, correctness, accessibility, durability, observability, or
legally required behavior, and uncertain cases are admitted for human review.

## Worker protocol

```sh
bsb worker --socket PATH
```

The service speaks Brokk Town Worker Protocol v1 over a mode-0600 Unix socket.

- `GET /v1/initialize` identifies `simplifier-bot`.
- `POST /v1/runs` accepts a strict task with either an issue or PR number and
  `mode`, or neither for a repository scan.
- Item runs return `result.simplification`; discovery runs create marked GitHub
  issues and return no typed result.
- The stream is bounded newline-delimited JSON with progress, terminal result,
  and completion events.

Assessment worktrees are detached and exact-revision checked. Tracked edits and
revision movement fail the run. GitHub issue creation uses a random durable
request marker saved before publication; an unknown outcome is reconciled by
marker and is never blindly reposted.

## Development

Go 1.27.1 or newer is required.

```sh
make check
```

Town currently pins this package as `@brokkai/simplifier-bot`. Local development
can override the executable with the simplifier bot command setting.

No release has been published from this initial implementation.

## License

Licensed under [MIT](LICENSE). Release packages include third-party notices
for bundled dependencies, which retain their own license terms.
