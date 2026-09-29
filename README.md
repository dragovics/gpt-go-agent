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
4. Persistent session state with crash-safe atomic storage
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

The remote URL is passed unchanged. The application API key and webhook secret are explicitly stripped from the executor environment. The restricted executor key is exposed only as CODEX_API_KEY.

## Status

Architecture foundations and the local executor lifecycle are in place. The production layer now includes webhook-driven reconnect, signed event verification, durable session state, bounded webhook deduplication, exponential lifecycle retries, readiness/metrics endpoints, executor secret isolation, and hardened systemd deployment.


## Production deployment

The daemon exposes /healthz and, when OPENAI_WEBHOOK_SECRET is configured, /webhooks/openai.
The webhook path verifies signatures before enqueueing work. Webhook jobs are journaled with mode 0600 and fsync, recovered at startup, deduplicated with bounded memory, retried with exponential backoff, and dead-lettered after the retry budget is exhausted.

Required application configuration is documented in deploy/env.example. Keep OPENAI_API_KEY outside the executor environment. Use a separate restricted environment key as CODEX_API_KEY for codex exec-server.

A hardened systemd unit is provided at deploy/gpt-go-agent.service. Create a dedicated service account and writable state directory before enabling it.

### Operational checks

1. Install the approved Codex CLI on the execution host.
2. Configure the application API key and webhook signing secret.
3. Configure the restricted executor key only in the executor environment as CODEX_API_KEY.
4. Start the daemon and verify /healthz.
5. Create a self-hosted session and verify the executor reaches environment.connected.
6. Exercise a harmless read-only task before enabling write/network capabilities.
7. Verify journal and session recovery by restarting the daemon while a webhook job is queued or a session is active.
8. Run go test ./..., go test -race ./..., go vet ./..., and go build ./... in CI before release.

The application intentionally does not expose a raw network shell endpoint. The self-hosted executor remains the official OpenAI command bridge; this daemon does not proxy arbitrary shell requests. The executor remains the OpenAI-managed command bridge, while this daemon owns session lifecycle, authentication, queueing, policy, and audit boundaries.
