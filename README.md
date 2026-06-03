# Chief CLI

[![License](https://img.shields.io/badge/license-Apache--2.0-blue)](LICENSE)
[![Go](https://img.shields.io/badge/go-1.26-00ADD8?logo=go&logoColor=white)](go.mod)
[![Release](https://img.shields.io/github/v/release/Storytell-ai/chief-cli?sort=semver)](https://github.com/Storytell-ai/chief-cli/releases)

`chief` is the command-line client for the [Chief](https://chief.bot) public API. It renders styled output in a terminal and machine-readable JSON when piped or run with `--json`, so it works equally well at a prompt, in a script, in CI, and behind an AI coding agent.

```console
$ chief chats create "Summarize the latest uploads in two bullets"
• Two new contracts landed this week; both renew in Q3.
• The Acme MSA adds a data-processing addendum worth a legal review.

chat: chat_5f3c…
```

## Contents

- [Installation](#installation)
- [Quickstart](#quickstart)
- [Authentication & configuration](#authentication--configuration)
- [Commands](#commands)
- [Use with AI agents](#use-with-ai-agents)
- [Output & scripting](#output--scripting)
- [Raw API access](#raw-api-access)
- [Global flags](#global-flags)
- [Local development](#local-development)
- [License](#license)

## Installation

### Go

```bash
go install github.com/Storytell-ai/chief-cli/cmd/chief@latest
```

This installs a `chief` binary into `$(go env GOPATH)/bin`. Make sure that directory is on your `PATH`.

### Prebuilt binaries

Download the archive for your platform from the [releases page](https://github.com/Storytell-ai/chief-cli/releases), extract it, and move `chief` somewhere on your `PATH`. Builds are published for Linux, macOS, and Windows on `amd64` and `arm64`.

### From source

```bash
git clone https://github.com/Storytell-ai/chief-cli.git
cd chief-cli
task install        # builds and installs to GOBIN
# or, without Task:
go build -o bin/chief ./cmd/chief
```

Verify the install:

```bash
chief --version
```

## Quickstart

```bash
# 1. Authenticate (prompts for base URL, API key, and project).
chief login

# 2. Confirm everything resolves and the API is reachable.
chief doctor

# 3. Ask a question.
chief chats create "What changed in my project this week?"
```

Already have an API key and project ID? Skip the prompts:

```bash
chief login --api-key "$CHIEF_API_KEY" --project proj_123
```

## Authentication & configuration

`chief` authenticates with a Chief API key (a personal access token) scoped to a project. Settings resolve with the precedence **flag > environment variable > saved credentials > built-in default**, evaluated independently for each value.

| Setting   | Flag          | Environment variable | Default                     |
| --------- | ------------- | -------------------- | --------------------------- |
| API key   | `--api-key`   | `CHIEF_API_KEY`      | —                           |
| Project   | `--project`   | `CHIEF_PROJECT_ID`   | —                           |
| Base URL  | `--base-url`  | `CHIEF_BASE_URL`     | `https://api.storytell.ai`  |

`chief login` verifies the key against the API and writes it to a credentials file at `$XDG_CONFIG_HOME/chief/credentials.json` (or `~/.config/chief/credentials.json` when `XDG_CONFIG_HOME` is unset). The file holds a secret, so it is created with `0600` permissions inside a `0700` directory.

`chief doctor` reports where each effective setting came from, whether the credentials file is parseable and correctly permissioned, and whether the API is reachable:

```console
$ chief doctor
Credentials: /Users/you/.config/chief/credentials.json
Hosts: 1 configured
CHIEF_API_KEY: unset
Base URL: https://api.storytell.ai (file)
API key: ••••a1b2 (file)
Project: proj_123 (file)
✓ reachable — 3 projects
```

## Commands

Run `chief help` or `chief <command> --help` for full, generated usage at any time.

```
chief
├── login                 Authenticate to a host and save credentials
├── doctor                Diagnose local configuration and connectivity
├── project               Manage the default project
│   ├── list
│   └── switch
├── chats                 Start and continue conversations
│   ├── create
│   ├── send
│   ├── get
│   ├── list
│   ├── update
│   └── delete
├── assets                Upload and manage files
│   ├── upload
│   ├── list
│   ├── get
│   ├── update
│   └── delete
├── actions               Scheduled and triggered automations
│   ├── create / list / get / update / delete
│   └── enable / disable
├── labels                Organize assets with labels
│   ├── create / list / get / update / delete
│   └── attach / detach
├── skills                Reusable instructions for chat turns
│   ├── create / list / get / update / delete
│   └── enable / disable
├── memories              Persistent context the assistant recalls
│   └── create / list / get / update / delete
├── sessions              Inspect and manage sessions
│   └── list / get / update / delete
├── mcp                   Generate MCP server config for coding agents
│   └── config
└── api                   Make a raw authenticated API request
```

### Chats

Start a conversation, send follow-ups, and read transcripts. By default `create` and `send` wait for the assistant's response and render it as Markdown; pass `--no-wait` to return as soon as the turn is accepted.

```bash
# Start a chat and wait for the answer.
chief chats create "Draft a release note from the latest commits"

# Continue an existing chat.
chief chats send chat_5f3c "Make it half as long"

# Tune the turn.
chief chats create "Research our top three competitors" \
  --intelligence research \
  --provider anthropic \
  --skill competitor-analysis \
  --public-data \
  --label-id label_market

# Print the full transcript, or just enqueue and poll later.
chief chats get chat_5f3c
chief chats create "Long-running analysis" --no-wait
```

Turn-tuning flags (shared by `create` and `send`):

| Flag             | Description                                                      |
| ---------------- | --------------------------------------------------------------- |
| `--intelligence` | Mode preset: `auto`, `fast`, `expert`, or `research`            |
| `--provider`     | Provider bias: `automatic`, `anthropic`, `openai`, or `google`  |
| `--skill`        | Enable a skill for the turn (repeatable)                        |
| `--public-data`  | Allow public-web search (otherwise follows the mode preset)     |
| `--label-id`     | Scope to a label (repeatable)                                   |
| `--asset-id`     | Scope to an asset (repeatable)                                  |
| `--chat-id`      | Include a past chat as context (repeatable)                     |
| `--project-id`   | Scope to a project (repeatable)                                 |
| `--concept-id`   | Scope to a concept (repeatable)                                 |
| `--no-wait`      | Return once the turn is accepted instead of waiting             |
| `--timeout`      | How long to wait for the response (default `5m`)                |

### Assets

Upload a single file or a whole directory. Uploads run in parallel, content-identical files are skipped server-side, and by default the CLI waits for each file to finish ingesting.

```bash
chief assets upload ./report.pdf
chief assets upload ./docs --recursive --concurrency 8
chief assets upload ./big.csv --no-wait        # don't block on ingest

chief assets list --limit 20
chief assets get asset_abc
chief assets update asset_abc --name "Q3 report" --description "Final"
chief assets delete asset_abc
```

| Flag            | Default | Description                                |
| --------------- | ------- | ------------------------------------------ |
| `--recursive`   | `true`  | Descend into subdirectories                |
| `--concurrency` | `4`     | Number of parallel uploads                 |
| `--no-wait`     | `false` | Return after upload without waiting for ingest |
| `--timeout`     | `15m`   | Per-file wait timeout                      |

### Actions

Actions are automations that run a prompt on a schedule or in response to events, optionally emailing the result. `update` replaces an action wholesale — any schedule, trigger, scope, or email not passed as a flag is cleared.

```bash
# Every weekday at 07:00 UTC, summarize new assets and email the team.
chief actions create "Daily digest" \
  --prompt "Summarize assets added in the last 24 hours" \
  --hour 7 --weekday 1-5 --timezone UTC \
  --email team@example.com --email-subject "Daily digest"

# Run whenever a new asset lands in a label.
chief actions create "Triage incoming" \
  --prompt "Tag and route this document" \
  --trigger new --label-id label_inbox

chief actions list --limit 20
chief actions disable action_123
chief actions enable action_123
chief actions delete action_123
```

| Flag              | Description                                          |
| ----------------- | --------------------------------------------------- |
| `--prompt`        | Instruction the action runs (required on `create`)  |
| `--description`   | Human-readable description                          |
| `--hour`          | Cron hour field (unset positions default to `*`)    |
| `--weekday`       | Cron weekday field                                  |
| `--month-day`     | Cron day-of-month field                             |
| `--timezone`      | IANA timezone for the schedule (default `UTC`)      |
| `--trigger`       | Event trigger: `new` or `all`                       |
| `--email`         | Email recipient (repeatable)                        |
| `--email-subject` | Subject line for the email outcome                  |
| `--label-id`      | Scope the action to a label (repeatable)            |
| `--asset-id`      | Scope the action to an asset (repeatable)           |

### Labels

```bash
chief labels create "Contracts" --color "#6b7280" --icon file
chief labels list --limit 20
chief labels attach asset_abc Contracts      # attach by name
chief labels detach asset_abc label_xyz      # detach by ID
chief labels delete label_xyz
```

### Sessions

```bash
chief sessions list --limit 20
chief sessions get session_abc
chief sessions update session_abc --name "Onboarding" --description "Notes"
chief sessions delete session_abc
```

## Use with AI agents

`chief` is built to put Chief's projects, assets, and conversations within reach of coding agents and autonomous workflows. There are four building blocks.

### MCP server

`chief mcp config <harness>` prints a ready-to-paste [Model Context Protocol](https://modelcontextprotocol.io) server snippet, prefilled with your current credentials, that points a coding agent at the `chief-mcp` server. Supported harnesses: `claude`, `cursor`, and `codex`.

```bash
chief mcp config claude
```

```json
{
  "mcpServers": {
    "chief": {
      "command": "chief-mcp",
      "args": ["stdio"],
      "env": {
        "CHIEF_API_KEY": "your-api-key",
        "CHIEF_PROJECT_ID": "proj_123"
      }
    }
  }
}
```

The hint printed to stderr tells you where each harness expects the snippet:

- **Claude Code** — `.mcp.json` (project) or `~/.claude.json` (user), or run `claude mcp add chief -- chief-mcp stdio`
- **Cursor** — `.cursor/mcp.json` (project) or `~/.cursor/mcp.json` (global)
- **Codex** — `~/.codex/config.toml` (the snippet is emitted as TOML for this harness)

If credentials aren't configured yet, the snippet uses placeholders you can fill in, or pass `--api-key` and `--project` to bake them in.

### Skills

Skills are reusable instructions (a prompt fragment, a persona, a procedure) that you enable per chat turn with `chief chats ... --skill <name>`.

```bash
chief skills create competitor-analysis \
  --content "When asked about competitors, produce a SWOT table and cite sources." \
  --description "Structured competitor breakdowns" \
  --scope project --category skill

chief skills list
chief skills disable competitor-analysis
```

| Flag             | Description                            |
| ---------------- | -------------------------------------- |
| `--content`      | Skill body (required on `create`)      |
| `--display-name` | Human-readable name                    |
| `--description`  | What the skill does                    |
| `--scope`        | `project` or `user`                    |
| `--category`     | `skill` or `persona`                   |
| `--icon`         | Icon name                              |

### Memories

Memories are durable facts the assistant carries across conversations — identity, preferences, standing instructions.

```bash
chief memories create \
  --content "Always answer in metric units." \
  --category instruction --importance 8

chief memories list
chief memories update mem_abc --content "Answer in metric units, 24h time."
```

Categories: `identity`, `preference`, `fact`, `context`, `instruction`. Scope is empty (global) or `project`.

### Actions

For unattended automation — scheduled digests, event-triggered triage — see [Actions](#actions) above. Combined with skills and memories, an action becomes a recurring agent task with persistent context.

## Output & scripting

Every command renders styled, human-readable output on a terminal and switches to JSON when you add `--json` or pipe the output. JSON is pretty-printed on a TTY and compact when piped, so it composes cleanly with tools like `jq`.

```bash
# Pretty table on a terminal.
chief assets list

# Stable JSON for scripts.
chief assets list --json | jq -r '.data[] | select(.status=="failed") | .asset_id'

# Capture the new chat's ID without waiting for the answer.
chief chats create "Kick off analysis" --no-wait --json | jq -r '.chat_id'
```

Commands exit non-zero on failure. Delete commands prompt for confirmation on an interactive terminal; pass `--force` (or `-f`) to skip the prompt, which is also the default when output is piped or `--json` is set. Set `ACCESSIBLE=1` to degrade interactive prompts to plain text for screen readers.

## Raw API access

`chief api` sends an authenticated request to any endpoint and prints the JSON response. Authentication and content-type headers are applied for you. Useful for endpoints the CLI doesn't yet wrap, or for debugging.

```bash
chief api GET /v1/projects
chief api POST /v1/labels --body '{"name":"Urgent","color":"#e24b4a"}'
chief api POST /v1/labels --body @label.json    # read the body from a file
```

Add `--debug` to any command to dump the full HTTP request and response.

## Global flags

These persistent flags apply to every command:

| Flag          | Description                                                          |
| ------------- | ------------------------------------------------------------------- |
| `--api-key`   | Chief API key (env `CHIEF_API_KEY`)                                 |
| `--project`   | Project ID (env `CHIEF_PROJECT_ID`)                                 |
| `--base-url`  | API base URL (env `CHIEF_BASE_URL`; default `https://api.storytell.ai`) |
| `--insecure`  | Skip TLS certificate verification (local dev only)                  |
| `--json`      | Emit machine-readable JSON                                          |
| `--debug`     | Dump HTTP requests and responses                                    |
| `--no-color`  | Disable colored output                                              |

## Local development

The repo uses [Task](https://taskfile.dev) for common workflows:

```bash
task build            # build ./bin/chief
task run -- chats list   # go run with arguments
task install          # build and install to GOBIN
task test             # run the test suite
task test:race        # tests with the race detector and coverage
task lint             # golangci-lint
task fmt              # format with golangci-lint
task release:snapshot # local goreleaser snapshot, no publish
```

Requires Go 1.26+. Without Task, the equivalent `go` commands work directly (`go build ./cmd/chief`, `go test ./...`).

## License

[Apache 2.0](LICENSE). Copyright 2026 Signal from Noise.
