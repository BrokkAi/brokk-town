# Agent harnesses

A harness is an ACP agent executable that Town drives: it receives a prompt, a
workspace and a session, and does the actual coding or reasoning work. Every
house has a harness; so does Town Guide.

## Where harnesses come from

Town merges three sources into one catalog:

1. **The official ACP registry** — `https://cdn.agentclientprotocol.com/registry/v1/latest/registry.json`.
   Town keeps a bundled copy and a cached copy under the state directory, and
   refreshes the catalog on request or in the background. A refresh failure keeps
   the cached catalog.
2. **Bundled supplements** — `anvil`, `muse-acp` and `draupnir`. These are listed
   as `additional` entries and are not part of the registry's version scheme.
3. **Custom** — any ACP v1 stdio executable you name yourself.

List what is available:

```sh
bt harnesses
bt harnesses --refresh
```

The listing shows each agent's source (`registry` or `additional`), whether it
can run on this platform, and how it is launched. See
[tools and the browser](../README.md) for selecting one in the settings UI.

## Selecting a harness

```sh
bt add --repo OWNER/REPO --harness codex-acp
bt settings --repo OWNER/REPO --harness claude-acp --model claude-opus-5 --effort high
bt settings --repo OWNER/REPO --role review --harness codex-acp
bt settings --repo OWNER/REPO --role review --inherit
```

The default harness is `codex-acp`. When you select a different harness, Town
clears the stored model, effort and session mode, because those belong to the
harness that advertised them. A house override stays independent of the town
default until you `--inherit`.

### Custom harnesses

```sh
bt settings --repo OWNER/REPO --harness custom \
  --agent-command '["your-acp-agent","--stdio"]'
```

`--agent-command` is a JSON argument array interpreted literally, with no shell.
It is only accepted for the `custom` harness. You can add environment variables
and an advertised auth method or session mode in a `--config` file under the
town's `agent` object.

## Model and effort

Town does not guess a model catalogue. It asks the harness:

```sh
bt choices --repo OWNER/REPO
bt choices --repo OWNER/REPO --role review --model MODEL
```

The probe starts the harness, initializes an ACP session with the same session
config capability a real run advertises, and reads the choices. It offers no
workspace tools. For a managed (Mjolnir) selection, the choices come from the
daemon's profile API instead.

An empty model or effort uses the harness's own default. An unknown value is
refused with the choices the harness actually reported.

## How a harness is launched

`internal/harness` prepares a command; it never starts the process. Depending on
the entry:

- **Custom command** — the executable is resolved on `PATH`; missing means a
  clear setup error.
- **`npx` / `uvx` package** — Town runs `npx --yes -- PACKAGE [args]` (or `uvx`),
  requiring the launcher on `PATH`. This is how `anvil`, `muse-acp` and Mjolnir
  are launched by default.
- **Registry binary** — Town downloads the platform archive into
  `<state-dir>/harnesses/installed/<id>-<platform>`, verifies the archive
  checksum when the registry supplies one, unpacks it inside the cache, marks the
  installation, chmods the command `0700`, and installs it with an atomic rename
  under a file lock. A concurrent dispatch downloads once.

Redirects are restricted to HTTPS and the archive size is bounded. Nothing is
run through a shell.

## Harness-specific notes

- **codex-acp** — the default. If the `codex-acp` executable is not installed,
  the bots can fall back to `npx --yes @agentclientprotocol/codex-acp`; Node.js
  must be on the service `PATH`.
- **muse-acp** — launched through `npx`. Muse composes a session permission
  profile from your own `permissions.default_profile` setting, and a profile its
  stdio host cannot reach refuses every session. Town derives an ACP-only config
  directory that omits that one key and symlinks everything else back to your real
  directory, so credentials and trust decisions stay shared and live. Set
  `BROKK_TOWN_MUSE_PERMISSIONS=keep` to launch against the unmodified config.
- **draupnir** — a `PATH` supplement. Install it using its own release
  instructions and configure its model provider first.

## Setup errors

A missing executable, an unauthenticated harness, an unsupported model and an
unsupported effort are all reported as setup errors before any prompt. They do
not consume a work attempt and do not count against a task's retry budget;
correct the setting and restart. `bt doctor --repo OWNER/REPO` reports
availability without running an agent, and the result is saved in town state.

Harnesses run with the service account's permissions. They are not sandboxed by
Town, so run Town as a dedicated account, or in a container, when that matters.
