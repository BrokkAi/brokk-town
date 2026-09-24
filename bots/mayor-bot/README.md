# Brokk Mayor Bot

This standalone project now lives in [`BrokkAi/brokk-town/bots/mayor-bot`](https://github.com/BrokkAi/brokk-town/tree/master/bots/mayor-bot). Build and test from this directory; see [RELEASING.md](RELEASING.md) for its independent suffix-tag releases.

`bmb` is the Mayor of a Brokk Town. It is modeled on the released
simplifier-bot worker and uses the shared [ACP runner](https://github.com/BrokkAi/acp-go).

The Mayor has two duties, both one-shot runs Town asks for over the worker
protocol:

- **judge** — decide one arrival waiting at Town Hall: an issue, a pull request,
  or a bot update. The Mayor reads the town's description of the arrival (with
  any Simplifier Bot advice or Town review attached), the live issue or pull
  request and its discussion from GitHub, and the repository itself in a
  detached worktree at the exact revision, then answers admit, decline, or, for
  a bot update, delay, with a reason a person can audit.
- **bulletin** — summarize the pull requests merged into the branch in one
  window for the people who use the software: features gained and bugs fixed,
  in plain language, each item citing the pull requests it came from. Town
  shows the bulletins as its work-completed feed.

The Mayor never writes to GitHub and never edits the repository. Tracked edits
and revision movement fail the run. Every judgment is a decision the town's
human Mayor could have clicked; Town applies it through the same path.

## Run standalone

```sh
bmb /path/to/your-repo                    # a bulletin every poll interval (default 24h)
bmb once /path/to/your-repo               # one bulletin since the last one, then exit
bmb bulletin /path/to/your-repo --since 72h   # print one bulletin as JSON
bmb judge /path/to/your-repo --issue 42   # print a decision as JSON
bmb judge /path/to/your-repo --pr 7
bmb status /path/to/your-repo
```

With no configuration file, the bot discovers the repository from a checkout
(or a subdirectory), a bare repository, or a Git URL, watches the remote's
default branch, and keeps its own checkout and state under
`$XDG_STATE_HOME/mayor-bot` (default `~/.local/state/mayor-bot`). It uses an installed
`codex-acp`, or provisions it with `npx` when absent.

Every bot shares these options: `--config`, `--branch`, `--agent`,
`--agent-arg` (repeatable), `--model`, `--effort`, `--poll`, `--timeout`,
`--once`, `--json` (structured logs) and `--plain` (scrolling console output).
`status` prints the saved state; `version` prints the release.

Bot options: `--max-items`, and `--since` for `bulletin`. The watching bulletin
starts where the last recorded bulletin ended. Standalone, the Mayor still never
writes to GitHub.

## Terminal dashboard

Interactive runs show a live dashboard by default. It fits the current terminal
or tmux pane and adjusts when the pane is resized.

```sh
bmb /path/to/repo           # live dashboard in an interactive terminal
bmb /path/to/repo --plain   # scrolling console output and agent transcript
bmb /path/to/repo --json    # structured logs for tools and log collectors
```

The overview shows the repository and branch, stage, active tool, uptime, and
the countdown to the next bulletin; larger panes add the model, reasoning effort
and bulletin interval. **Recorded** counts every bulletin in the workspace state,
its items and the pull requests it covered. The bulletin browser shows each
window's summary and classified items with the pull requests and issues they
cite. On exit, bulletins written during the run stay in the terminal.

- `1`, `2`, `3` or `Tab`: switch between overview, bulletins, and activity.
- `↑` / `↓` or `k` / `j`: browse bulletins or scroll activity.
- `Enter`: inspect the selected bulletin. `Esc` returns; `Page Up` / `Page Down` scroll.
- `g` / `G`: jump to the start/end; `G` resumes following live activity.
- `q` or `Ctrl+C`: stop the bot and its active agent, then restore the terminal.

Piped input, redirected stderr, and `TERM=dumb` use scrolling output
automatically. `--plain` and `--json` disable the dashboard and are mutually
exclusive. `NO_COLOR` disables dashboard colors. `status`, `judge`, `bulletin`, `version` and help never open the
dashboard.

## Worker protocol

```sh
bmb worker --socket PATH
```

The service speaks Brokk Town Worker Protocol v1 over a mode-0600 Unix socket.

- `GET /v1/initialize` identifies `mayor-bot` with the `mayor-judgment` and
  `mayor-bulletin` capabilities.
- `POST /v1/runs` with `mode: "judge"` takes the arrival's `issue` or `pr`
  number (neither for a bot update), the exact `head_sha` for a pull request,
  and `arrival`, the town's JSON description of the item. It returns
  `result.judgment` with `decision` and `reason`.
- `POST /v1/runs` with `mode: "bulletin"` takes `since` and `until`. It returns
  `result.bulletin` with a title, a summary, classified items (`feature`,
  `fix`, `improvement`, `other`) and the pull request numbers it covered, so
  Town can advance its cursor without trusting the prose. A window with nothing
  merged returns an empty bulletin without starting an agent.
- The stream is bounded newline-delimited JSON with progress, terminal result,
  and completion events.

Judgment worktrees are detached and exact-revision checked. A bulletin item may
cite only pull requests from its window; anything else fails the run.

## Development

Go 1.27.1 or newer is required.

```sh
make check
```

Town pins this package as `@brokkai/mayor-bot`. Local development can point Town
at a built binary with the `BROKK_TOWN_HALL_BOT` environment variable.

No release has been published from this initial implementation.

## License

Licensed under [MIT](LICENSE). Release packages include third-party notices
generated at release time; see [RELEASING.md](RELEASING.md).
