# gpt-go-agent

A self-hosted execution gateway for ChatGPT-compatible MCP clients, with an optional durable webhook-to-planner execution pipeline.

The core daemon does not contain a reasoning model. MCP clients call a small local tool surface against a user-controlled workspace. The optional webhook path can ask an OpenAI-compatible Middleman service to translate an intent into a typed execution decision, but deterministic local policy remains the final execution gate.

## Architecture

    ChatGPT / MCP client
            |
            | POST /mcp
            v
      gpt-go-agent
            |
            +-- bearer authentication
            +-- capability-rooted workspace
            +-- explicit write policy
            +-- strict/trusted command policy
            +-- audit log
            |
            v
     user-controlled workspace

Optional webhook path:

    authenticated caller
            |
            | POST /webhook
            v
       durable queue
            |
            v
       Middleman planner
            |
            v
     deterministic policy
        |          |
      native      Codex
        |          |
     strict     read-only by default
      gate      workspace-write opt-in

## MCP endpoint

The server exposes POST /mcp and implements the JSON-RPC methods currently required by the supported MCP client flow:

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

Filesystem access is rooted with Go os.Root. Relative-path validation is not the only boundary: read, list, write, and command working-directory resolution reject symlink traversal outside the configured workspace.

Write access is disabled by default.

Command execution is disabled by default and has two modes:

- strict: the default. Only a small deterministic diagnostic surface is permitted: echo, uptime, pwd, date, uname, id, and ls, with constrained arguments.
- trusted: requires both AGENT_ALLOW_COMMAND_EXEC=1 and an explicit AGENT_ALLOWED_COMMANDS list. Trusted mode intentionally grants an allowlisted executable the authority of the gpt-go-agent service account. An executable-name allowlist is not a sandbox.

When MCP write or command execution is enabled, AGENT_MCP_TOKEN is mandatory. A non-loopback listener also requires AGENT_MCP_TOKEN.

Child process output and execution time are bounded. Strict native commands receive a minimal environment. Trusted subprocesses and Codex receive a credential-sanitized environment.

The optional Codex executor is disabled by default. When enabled it uses a read-only sandbox unless AGENT_CODEX_ALLOW_WRITE=1 is explicitly set.

The systemd unit provides defense in depth with a dedicated account, NoNewPrivileges, filesystem protection, empty capability sets, namespace restrictions, and other hardening. Application-level policy remains the primary boundary.

## Core configuration

    AGENT_LISTEN_ADDR=127.0.0.1:8787
    AGENT_WORKSPACE=/var/lib/gpt-go-agent/workspace
    AGENT_MCP_TOKEN=

    AGENT_ALLOW_WRITE=0
    AGENT_ALLOW_COMMAND_EXEC=0
    AGENT_COMMAND_EXEC_MODE=strict
    AGENT_ALLOWED_COMMANDS=

    AGENT_COMMAND_TIMEOUT=60s
    AGENT_MCP_MAX_OUTPUT_BYTES=16384
    AGENT_AUDIT_PATH=/var/lib/gpt-go-agent/agent-audit.jsonl

Read-only local start:

    export AGENT_WORKSPACE=/var/lib/gpt-go-agent/workspace
    go run ./cmd/gpt-go-agent

Enable writes:

    export AGENT_MCP_TOKEN='use-a-long-random-secret'
    export AGENT_ALLOW_WRITE=1

Strict command execution:

    export AGENT_MCP_TOKEN='use-a-long-random-secret'
    export AGENT_ALLOW_COMMAND_EXEC=1
    export AGENT_COMMAND_EXEC_MODE=strict

Trusted command execution is an explicit authority escalation:

    export AGENT_MCP_TOKEN='use-a-long-random-secret'
    export AGENT_ALLOW_COMMAND_EXEC=1
    export AGENT_COMMAND_EXEC_MODE=trusted
    export AGENT_ALLOWED_COMMANDS='git go'

In trusted mode, tools such as interpreters, build systems, package managers, and VCS clients may provide general code execution under the service account. Enable only when that is the intended trust model.

## Optional webhook pipeline

Webhook support is disabled unless AGENT_WEBHOOK_ENABLED=1 is set. Pure MCP deployments do not need a webhook token, Middleman service, durable job store, or Codex installation.

Enable webhook mode:

    AGENT_WEBHOOK_ENABLED=1
    AGENT_WEBHOOK_TOKEN=use-a-separate-long-random-secret
    AGENT_MIDDLEMAN_URL=http://127.0.0.1:20128/v1
    AGENT_MIDDLEMAN_MODEL=glm-5.3
    AGENT_MIDDLEMAN_COMPAT_PROFILE=

For compatibility, OPENAI_WEBHOOK_SECRET is accepted as a fallback token, but AGENT_WEBHOOK_TOKEN is the preferred name because this endpoint uses bearer authentication rather than vendor webhook-signature verification.

Endpoints:

    POST /webhook
    GET  /webhook?limit=50&offset=0
    GET  /webhook/{id}

Webhook POST supports Idempotency-Key and X-Request-ID. Reusing an idempotency key with different request content returns HTTP 409.

Job state is persisted before execution-side effects are allowed. Store health contributes to /readyz. Pending jobs are recovered after restart; jobs that were evaluating or running at restart are terminally failed for manual review.

The JSON durable store is intentionally bounded with AGENT_WEBHOOK_MAX_JOBS. It is suitable for a small single-node gateway, not an unbounded high-throughput queue.

Webhook tuning:

    AGENT_WEBHOOK_WORKERS=4
    AGENT_WEBHOOK_QUEUE_SIZE=100
    AGENT_WEBHOOK_MAX_JOBS=10000
    AGENT_WEBHOOK_RATE_LIMIT_PER_MINUTE=120
    AGENT_WEBHOOK_JOB_TIMEOUT=3m
    AGENT_WEBHOOK_RETENTION=168h
    AGENT_WEBHOOK_CLEANUP_INTERVAL=1h
    AGENT_WEBHOOK_MAX_OUTPUT_BYTES=65536

The default Middleman User-Agent identifies gpt-go-agent. Set AGENT_MIDDLEMAN_COMPAT_PROFILE=opencode only for an upstream router that explicitly requires the legacy OpenCode compatibility headers.

Middleman transport resilience:

    AGENT_MIDDLEMAN_TIMEOUT=30s
    AGENT_MIDDLEMAN_MAX_ATTEMPTS=3
    AGENT_MIDDLEMAN_RETRY_BASE=250ms
    AGENT_MIDDLEMAN_CIRCUIT_THRESHOLD=3
    AGENT_MIDDLEMAN_CIRCUIT_OPEN=10s

Worker-level Gatekeeper retries default to zero because the Middleman transport already retries transient failures. If enabled, circuit-open errors carry a retry delay so the worker does not immediately retry into an open breaker.

## Optional Codex execution

Codex is independent from MCP write permission:

    AGENT_CODEX_ENABLED=0
    AGENT_CODEX_ALLOW_WRITE=0
    AGENT_CODEX_BIN=codex

AGENT_CODEX_ENABLED=1 allows approved webhook plans to invoke Codex. The default sandbox remains read-only. Set AGENT_CODEX_ALLOW_WRITE=1 only when workspace mutation by Codex is intended.

## Observability

The daemon exposes:

    GET /healthz
    GET /readyz
    GET /metrics

Audit events are JSONL with a real UTC timestamp, action, target, decision, correlation ID where available, and duration. The audit file is created with mode 0600.

Webhook metrics include queue/capacity, retained job counts by state, storage health, worker count, retry/dead-letter counts, maximum retained job duration, and Middleman retry/circuit metrics.

## Tests and CI

The project requires Go 1.25+ and pins the production/CI toolchain to Go 1.25.14 or newer patched releases.

CI runs:

    gofmt check
    go test ./...
    go test -race ./...
    go vet ./...
    go test -cover ./...
    go build ./...
    Staticcheck
    Gosec
    govulncheck

Security regression tests include workspace traversal and symlink-escape cases, strict/trusted execution policy, bearer authentication, webhook idempotency, output bounds, retry behavior, durable-store backup recovery, and restart recovery.

## Deployment

Release tags use vMAJOR.MINOR.PATCH. The release workflow builds Linux amd64 and arm64 binaries with checksums.

scripts/deploy.sh performs an atomic binary replacement, restarts the service, checks /healthz and /readyz, and restores the previous binary if deployment health checks fail.

Keep the service private whenever possible. Prefer a private tunnel or trusted reverse proxy rather than directly exposing the execution service to the public internet.
