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

## Status

Architecture foundations are in place. The next implementation layer is the authenticated session/event bridge and concrete environment provisioning, followed by integration tests.
