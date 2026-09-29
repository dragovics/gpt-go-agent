# gpt-go-agent

A self-hosted execution gateway for ChatGPT-compatible MCP clients.

The daemon does not run an LLM and does not depend on Codex quotas. It exposes a small, authenticated MCP tool surface that a connected ChatGPT/MCP client can call against a user-controlled workspace.

## Architecture

    ChatGPT / MCP client
            |
            | Streamable HTTP / MCP
            v
      gpt-go-agent
            |
            +-- authentication
            +-- workspace boundary
            +-- write policy
            +-- command allowlist
            +-- audit log
            |
            v
     user-controlled VPS/workspace

The model/reasoning layer remains outside this process. gpt-go-agent is the execution boundary.

## MCP endpoint

The server exposes:

    POST /mcp

It implements the MCP JSON-RPC methods needed for tool discovery and invocation:

    initialize
    notifications/initialized
    tools/list
    tools/call

Current tools:

- list_dir
- read_file
- write_file
- exec_command

## Security model

- Workspace paths must remain inside the configured workspace.
- Absolute paths are rejected.
- Parent traversal outside the workspace is rejected.
- Write access is disabled by default.
- Command execution is disabled by default.
- Executables must be explicitly allowlisted.
- Command execution uses argument arrays, not a shell command string.
- Command timeout and output size are bounded.
- Remote MCP requests require a bearer token.
- Without a token, MCP is accepted only from loopback.
- The MCP token and OpenAI credentials are not inherited by child commands.
- Tool calls are recorded in the append-only 0600 audit log.
- The daemon does not expose an unauthenticated raw network shell.

## Configuration

See deploy/env.example.

Important settings:

    AGENT_MCP_TOKEN
    AGENT_LISTEN_ADDR
    AGENT_WORKSPACE
    AGENT_ALLOW_WRITE
    AGENT_ALLOW_COMMAND_EXEC
    AGENT_ALLOWED_COMMANDS
    AGENT_AUDIT_PATH

Start locally:

    export AGENT_WORKSPACE=/var/lib/gpt-go-agent/workspace
    export AGENT_MCP_TOKEN='use-a-long-random-secret'
    go run ./cmd/gpt-go-agent

For command execution:

    export AGENT_ALLOW_COMMAND_EXEC=1
    export AGENT_ALLOWED_COMMANDS='git go python3 npm node make ls pwd cat grep find'

Enable writes separately:

    export AGENT_ALLOW_WRITE=1

Do not expose the service publicly without authentication and TLS/tunneling.

## Connecting to ChatGPT

OpenAI documents custom MCP servers as a way for ChatGPT-compatible products to call tools on infrastructure you operate. ChatGPT connects to remote MCP servers; private/local servers can be reached through Secure MCP Tunnel rather than being exposed directly to the public internet.

The exact availability of write-capable custom MCP apps depends on the ChatGPT product/workspace and its current developer-mode/app permissions.

Recommended deployment path:

    VPS
      |
      +-- gpt-go-agent
      |
      +-- private MCP endpoint
              |
              +-- Secure MCP Tunnel
              |
              +-- ChatGPT

This keeps the execution service independent from Codex.

## Operational hardening

The systemd unit uses a dedicated service account and filesystem restrictions. Keep the workspace and audit/state directories under the service-owned data directory.

Before enabling command execution, start with read-only tools and validate the MCP connection. Enable writes and command execution deliberately.

## Tests

CI runs:

    go test ./...
    go test -race ./...
    go vet ./...
    go build ./...

The goal is that all execution happens through the bounded MCP tool surface rather than through a second agent runtime embedded in this daemon.
