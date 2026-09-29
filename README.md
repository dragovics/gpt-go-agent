# gpt-go-agent

A lightweight execution boundary for ChatGPT/Codex-controlled remote development.

## Architecture

```
ChatGPT/Codex
     |
     | authenticated/official transport
     v
gpt-go-agent
     |
     +-- policy / audit / task lifecycle
     +-- session / transport abstractions
     +-- Codex executor integration
     v
user-controlled environment
```

The agent does not contain an LLM. It owns connection/session/policy concerns; Codex owns reasoning and the tool loop.

## Implemented foundations

1. Agent lifecycle and versioning
2. Codex executor boundary
3. Remote transport abstraction
4. Persistent-in-process session state
5. Task lifecycle state
6. Explicit tool contract
7. Policy checks for writes/network
8. JSONL audit logging
9. Local health server, bound to `127.0.0.1:8787` by default

## Security

- Localhost binding by default.
- No public unauthenticated shell endpoint.
- Writes and network access are policy-controlled.
- Audit events are append-only JSONL with restrictive file permissions.
- Credentials are not stored in source.
- The Codex executor is an integration boundary; the agent does not implement a second shell protocol.

## Codex integration

OpenAI's current self-hosted environment model uses `codex exec-server` as the executor. The executor connects outbound using a restricted environment key; application credentials stay outside the environment.

The App Server is a separate JSON-RPC integration and is currently documented as experimental. Keep that integration isolated so the project can adopt protocol changes without coupling the execution boundary to it.

## Run the executor

The environment key is intentionally supplied only through the process environment.

```bash
export CODEX_API_KEY='...restricted environment key...'
export CODEX_REMOTE_URL='...session environment remote_url...'
export CODEX_ENVIRONMENT_ID='...session environment id...'

go run ./cmd/gpt-go-agent
```

The CLI starts:

```text
codex exec-server --remote "$CODEX_REMOTE_URL" --environment-id "$CODEX_ENVIRONMENT_ID"
```

The remote URL is passed unchanged. The application API key is not passed to the executor.

## Status

Architecture foundations and the local executor lifecycle are in place. The next production layer is webhook-driven provisioning/reconnect, signed event verification, durable task storage, observability, and end-to-end tests.
