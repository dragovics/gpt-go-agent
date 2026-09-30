# gpt-go-agent

A self-hosted execution gateway for ChatGPT-compatible MCP clients, with an optional durable webhook execution pipeline.

The MCP path is intentionally small and bounded: workspace file access plus a restricted native-command surface. The optional webhook path can use a Middleman LLM to translate intent into an execution plan, but every native command is still checked by deterministic policy before execution.

## Architecture

    ChatGPT / MCP client
            |
            | HTTP / MCP
            v
      gpt-go-agent
            |
            +-- bearer authentication
            +-- rooted workspace filesystem
            +-- write policy
            +-- deterministic command policy
            +-- bounded output/timeouts
            +-- audit log
            |
            v
     user-controlled VPS/workspace

Optional webhook mode:

    webhook caller
         |
         v
    authenticated queue
         |
         v
    Middleman planner
         |
         +-- native -> deterministic command policy -> executor
         |
         +-- codex  -> read-only sandbox by default
                       workspace-write only when explicitly enabled

The model/reasoning layer is not trusted as the final authorization boundary. Native execution must pass deterministic server-side policy.

## MCP endpoint

The server exposes:

    POST /mcp

Implemented JSON-RPC methods:

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

- File operations use a rooted workspace filesystem. Parent traversal, absolute paths, and symlinks that escape the workspace are rejected by the rooted filesystem API.
- Write access is disabled by default.
- Native command execution is disabled by default.
- Enabling writes or native command execution requires an MCP bearer token, even on loopback.
- Native commands must pass both the configured allowlist and a deterministic restricted policy.
- The restricted policy intentionally rejects shells, interpreters, package managers, build tools, network clients, VCS commands, and arbitrary path/options.
- Native command execution always starts at the workspace root; custom working directories are rejected.
- Command execution uses argument arrays, never a shell command string.
- Command timeout and output size are bounded.
- Remote MCP requests require a bearer token. Tokenless MCP is accepted only from loopback and only when mutation/exec capabilities are disabled.
- Credential-like environment variables are removed from child command environments.
- Tool calls are recorded in a 0600 append-only audit log with UTC timestamps.
- The daemon does not expose an unauthenticated raw network shell.

The systemd hardening is defense in depth; it is not a substitute for the application-level workspace and command policy.

## Configuration

See `deploy/env.example`.

Important MCP settings:

    AGENT_MCP_TOKEN
    AGENT_LISTEN_ADDR
    AGENT_WORKSPACE
    AGENT_ALLOW_WRITE
    AGENT_ALLOW_COMMAND_EXEC
    AGENT_ALLOWED_COMMANDS
    AGENT_AUDIT_PATH

Start the read-only MCP service locally:

    export AGENT_WORKSPACE=/var/lib/gpt-go-agent/workspace
    go run ./cmd/gpt-go-agent

For authenticated restricted command execution:

    export AGENT_MCP_TOKEN='use-a-long-random-secret'
    export AGENT_ALLOW_COMMAND_EXEC=1
    export AGENT_ALLOWED_COMMANDS='echo uptime pwd date uname id ls'

Enable MCP file writes separately:

    export AGENT_ALLOW_WRITE=1

Do not expose the service publicly without authentication and TLS/tunneling.

## Optional webhook execution pipeline

Webhook execution is disabled by default. Enable it deliberately:

    export AGENT_WEBHOOK_ENABLED=1
    export AGENT_WEBHOOK_TOKEN='use-a-separate-long-random-secret'

The webhook path exposes:

    POST /webhook
    GET  /webhook?limit=50&offset=0
    GET  /webhook/{id}

`Idempotency-Key` prevents duplicate delivery from creating duplicate jobs; reusing a key with different request content returns HTTP 409. `X-Request-ID` can provide a correlation ID and is echoed in the response.

The worker persists state transitions with atomic temp-file + fsync + rename writes, keeps a backup store, recovers pending jobs after restart, dead-letters terminal operational failures, retries transient Middleman failures with bounded backoff, and prunes terminal jobs after the configured retention period.

If the primary job store is missing or corrupt after a crash window, startup falls back to the backup store.

### Codex execution

Codex is read-only by default:

    AGENT_CODEX_ALLOW_WRITE=0

To deliberately grant Codex workspace-write sandbox access:

    AGENT_CODEX_ALLOW_WRITE=1

This setting is separate from MCP `AGENT_ALLOW_WRITE`; the two execution surfaces no longer implicitly share mutation permissions.

### Webhook configuration

    AGENT_WEBHOOK_ENABLED
    AGENT_WEBHOOK_TOKEN
    AGENT_WEBHOOK_WORKERS
    AGENT_WEBHOOK_STORE
    AGENT_MIDDLEMAN_URL
    AGENT_MIDDLEMAN_KEY
    AGENT_MIDDLEMAN_MODEL
    AGENT_MIDDLEMAN_TIMEOUT
    AGENT_MIDDLEMAN_MAX_ATTEMPTS
    AGENT_MIDDLEMAN_RETRY_BASE
    AGENT_MIDDLEMAN_CIRCUIT_THRESHOLD
    AGENT_MIDDLEMAN_CIRCUIT_OPEN
    AGENT_CODEX_BIN
    AGENT_CODEX_ALLOW_WRITE
    AGENT_WEBHOOK_RETENTION
    AGENT_WEBHOOK_CLEANUP_INTERVAL
    AGENT_WEBHOOK_MAX_GATEKEEPER_RETRIES
    AGENT_WEBHOOK_RETRY_BASE
    AGENT_WEBHOOK_MAX_OUTPUT_BYTES

`OPENAI_WEBHOOK_SECRET` is accepted only as a backwards-compatible fallback for `AGENT_WEBHOOK_TOKEN`.

## Connecting to ChatGPT

Use the private MCP endpoint through an authenticated/tunneled connection. Keep the execution service bound to loopback unless you have a deliberate remote-access design.

## Operational hardening

The systemd unit uses a dedicated service account and filesystem/kernel restrictions. Keep the workspace, audit log, and job state under service-owned data directories.

Start with read-only MCP, validate connectivity, then enable individual capabilities deliberately. Treat command execution as a privileged capability even though the deterministic policy sharply limits the default surface.

## Tests and CI

CI runs:

    go test ./...
    go test -race ./...
    go vet ./...
    go test -cover ./...
    go build ./...
    staticcheck
    gosec
    govulncheck

Security regression tests cover workspace traversal/symlink escape, restricted native commands, MCP token requirements for mutation/exec, webhook authentication/idempotency, bounded subprocess output, durable job-store recovery, and audit timestamps.

## Release and deployment

Release tags use `vMAJOR.MINOR.PATCH`. The release workflow builds Linux amd64/arm64 artifacts and SHA256 checksums. `scripts/deploy.sh` performs an atomic binary replacement, restarts the service, verifies health/readiness, and restores the previous binary on failed deployment checks.

## Known architectural follow-ups

The current job store is intentionally still a JSON snapshot store. For sustained high-volume webhook traffic, migrate it to SQLite/WAL rather than continuing to extend whole-file persistence.

The MCP wire implementation remains hand-written against protocol version `2025-06-18`. A future compatibility-focused change should migrate it to the official MCP Go SDK as a separate PR, with client compatibility tests, rather than mixing a protocol migration into security hardening.
