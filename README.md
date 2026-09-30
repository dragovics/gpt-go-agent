# gpt-go-agent

A self-hosted execution gateway that gives ChatGPT-compatible MCP clients a small, auditable interface to a workspace you control.

It can run in two independent modes:

1. **MCP gateway** — the default. Exposes workspace file tools and an optional restricted native-command tool.
2. **Webhook worker** — optional. Accepts authenticated jobs, asks a Middleman LLM to translate intent into an execution plan, then applies deterministic execution policy before running native commands or Codex.

The project is designed for people who want an AI client to work against a private VPS, development box, or controlled repository **without exposing a general-purpose unauthenticated shell**.

---

## Table of contents

- [Purpose](#purpose)
- [What this project is and is not](#what-this-project-is-and-is-not)
- [Architecture](#architecture)
- [Feature overview](#feature-overview)
- [Security model](#security-model)
- [Requirements](#requirements)
- [Quick start: MCP-only](#quick-start-mcp-only)
- [MCP tools](#mcp-tools)
- [Testing the MCP endpoint manually](#testing-the-mcp-endpoint-manually)
- [Enabling writes](#enabling-writes)
- [Enabling restricted command execution](#enabling-restricted-command-execution)
- [Connecting a remote MCP client](#connecting-a-remote-mcp-client)
- [Webhook worker mode](#webhook-worker-mode)
- [Codex execution](#codex-execution)
- [Health, readiness, and metrics](#health-readiness-and-metrics)
- [Audit logging](#audit-logging)
- [Configuration reference](#configuration-reference)
- [Systemd deployment](#systemd-deployment)
- [Release and deployment workflow](#release-and-deployment-workflow)
- [Testing and CI](#testing-and-ci)
- [Troubleshooting](#troubleshooting)
- [Operational recommendations](#operational-recommendations)
- [Known architectural follow-ups](#known-architectural-follow-ups)

---

## Purpose

The main goal of `gpt-go-agent` is to provide a **small execution boundary** between an AI client and a machine you control.

A typical use case looks like this:

```text
ChatGPT / MCP client
        |
        | authenticated MCP requests
        v
  gpt-go-agent
        |
        +-- rooted workspace access
        +-- optional file writes
        +-- optional restricted native commands
        +-- bounded execution
        +-- audit log
        |
        v
 private VPS / repository / workspace
```

The daemon is useful when you want an AI client to:

- inspect files in a private repository;
- create or update files inside one controlled workspace;
- run a small set of diagnostic native commands;
- keep an audit trail of tool calls;
- avoid exposing SSH or a raw shell directly to the model;
- optionally accept durable asynchronous jobs through an HTTP webhook API;
- optionally delegate coding jobs to Codex with a read-only or workspace-write sandbox.

The default deployment is intentionally conservative:

- loopback listener;
- file writes disabled;
- native command execution disabled;
- webhook execution disabled;
- Codex writes disabled.

You explicitly enable additional capabilities.

---

## What this project is and is not

### It is

- an MCP HTTP server;
- a workspace-scoped file gateway;
- a restricted command executor;
- an audit boundary;
- an optional durable asynchronous job runner;
- an optional Middleman-to-Codex/native execution bridge.

### It is not

- a replacement for SSH;
- a general-purpose remote shell;
- a container runtime;
- a complete OS sandbox;
- an LLM embedded inside the MCP server;
- a guarantee that arbitrary binaries become safe because their executable name is allowlisted.

The distinction around command execution is important.

`AGENT_ALLOWED_COMMANDS` is only the **first gate**. A command must also pass the deterministic restricted-command policy implemented by the server.

For example, adding `python3` to `AGENT_ALLOWED_COMMANDS` does **not** make `python3 -c ...` executable through the restricted MCP command tool.

---

## Architecture

### MCP path

```text
ChatGPT / MCP client
        |
        | POST /mcp
        v
+-------------------------+
|      gpt-go-agent       |
|-------------------------|
| Bearer authentication   |
| Rooted workspace        |
| Read/write capability   |
| Restricted cmd policy   |
| Timeout/output limits   |
| Environment sanitizing  |
| Audit logging           |
+------------+------------+
             |
             v
       controlled host
```

MCP file operations are performed through Go's rooted filesystem API. This prevents a path or symlink inside the workspace from transparently resolving outside the configured root.

### Optional webhook path

```text
Webhook caller
      |
      | Bearer token
      | Idempotency-Key
      v
+----------------------+
| durable job queue    |
+----------+-----------+
           |
           v
+----------------------+
| Middleman planner    |
| OpenAI-compatible API|
+----------+-----------+
           |
           v
    execution decision
       /          \
      /            \
 native           codex
   |                |
   v                v
deterministic     Codex sandbox
command policy    read-only by default
   |                |
   +-------+--------+
           |
           v
      workspace
```

The Middleman is **not** the final security authority. Its output is treated as an execution proposal.

Native commands must still pass deterministic server-side policy before a child process is created.

---

## Feature overview

| Feature | Default | Notes |
|---|---|---|
| MCP HTTP endpoint | enabled | `POST /mcp` |
| Workspace file listing | enabled | rooted to `AGENT_WORKSPACE` |
| Workspace file reading | enabled | rooted and output bounded |
| Workspace file writing | disabled | enable with `AGENT_ALLOW_WRITE=1` |
| MCP native execution | disabled | enable with `AGENT_ALLOW_COMMAND_EXEC=1` |
| MCP bearer token | optional for loopback read-only | mandatory when write/exec is enabled |
| Restricted native policy | always enforced | independent of executable allowlist |
| Audit log | enabled | JSON Lines, mode 0600 |
| Webhook worker | disabled | `AGENT_WEBHOOK_ENABLED=1` |
| Webhook authentication | required when enabled | separate bearer token |
| Durable webhook jobs | enabled with webhook | JSON snapshot store + backup |
| Idempotency | enabled with webhook | `Idempotency-Key` |
| Middleman retries | configurable | bounded retry + circuit breaker |
| Codex execution | available in webhook mode | requires Codex CLI |
| Codex workspace writes | disabled | explicit `AGENT_CODEX_ALLOW_WRITE=1` |
| Health endpoint | enabled | `GET /healthz` |
| Readiness endpoint | enabled | `GET /readyz` |
| Metrics endpoint | enabled | `GET /metrics` |

---

## Security model

The security model is based on **multiple independent gates** instead of trusting a single model decision.

### 1. Network exposure

The default listener is:

```text
127.0.0.1:8787
```

Keep it on loopback unless you intentionally configure authentication and a secure transport/tunnel.

### 2. MCP authentication

Remote MCP requests require a bearer token.

When no MCP token is configured, tokenless requests are accepted only from loopback and only while the server is read-only/non-executing.

If either of these is enabled:

```text
AGENT_ALLOW_WRITE=1
AGENT_ALLOW_COMMAND_EXEC=1
```

then `AGENT_MCP_TOKEN` becomes mandatory at startup.

Bearer secrets are compared using constant-time comparison.

### 3. Rooted workspace

All file operations are scoped to:

```text
AGENT_WORKSPACE
```

The server rejects:

- absolute paths;
- parent traversal outside the workspace;
- symlink traversal that escapes the workspace.

Examples that are not valid workspace access:

```text
/etc/passwd
../../etc/passwd
workspace-link -> /etc
```

### 4. Writes are opt-in

```text
AGENT_ALLOW_WRITE=0
```

is the default.

This controls MCP file writes.

Codex writes are controlled separately by `AGENT_CODEX_ALLOW_WRITE`.

### 5. Restricted native execution

Native command execution requires all of the following:

1. `AGENT_ALLOW_COMMAND_EXEC=1`;
2. an MCP token;
3. the executable appears in `AGENT_ALLOWED_COMMANDS`;
4. the executable and arguments pass deterministic restricted policy.

The current restricted policy allows:

```text
echo
uptime
pwd
date
uname
id
ls
```

The policy deliberately rejects general execution primitives such as:

```text
sh
bash
python
python3
node
npm
git
go
make
curl
wget
```

Adding one of those names to `AGENT_ALLOWED_COMMANDS` does not bypass deterministic policy.

### 6. No shell command strings

Native execution uses argument arrays with `os/exec`. It does not concatenate request text into `sh -c`.

### 7. Fixed native working directory

Restricted native commands start from the configured workspace root.

Custom `cwd` values are rejected.

### 8. Child environment sanitizing

Credential-like environment variables are removed from native child processes.

Codex uses its own child environment path because it may legitimately require authentication/configuration.

### 9. Bounded execution

Native/Codex output is bounded.

MCP command execution also has a configurable timeout.

### 10. Systemd hardening

The included service unit adds defense in depth with controls such as:

- `NoNewPrivileges=true`;
- `ProtectSystem=strict`;
- `ProtectHome=true`;
- empty capability bounding set;
- namespace restrictions;
- device restrictions;
- kernel/proc restrictions.

Systemd hardening is additional protection. It does not replace application-level path and execution policy.

---

## Requirements

### Build

- Go 1.24 or compatible project toolchain;
- Linux is the primary deployment target.

### Optional webhook/Codex mode

Depending on the configuration:

- an OpenAI-compatible Middleman endpoint;
- a Middleman model/API key if required by that endpoint;
- Codex CLI if Middleman decisions can dispatch to Codex.

The basic MCP-only mode does **not** require a Middleman, Codex, or webhook token.

---

## Quick start: MCP-only

Clone and build:

```bash
git clone https://github.com/dragovics/gpt-go-agent.git
cd gpt-go-agent

go build -o ./gpt-go-agent ./cmd/gpt-go-agent
```

Create a workspace:

```bash
mkdir -p /tmp/gpt-go-agent-workspace
printf 'hello from workspace\n' > /tmp/gpt-go-agent-workspace/hello.txt
```

Run the daemon in read-only MCP mode:

```bash
AGENT_WORKSPACE=/tmp/gpt-go-agent-workspace \
AGENT_LISTEN_ADDR=127.0.0.1:8787 \
./gpt-go-agent
```

Check health:

```bash
curl -sS http://127.0.0.1:8787/healthz
```

Example response:

```json
{"ok":true,"version":"0.2.1-dev"}
```

Check readiness:

```bash
curl -sS http://127.0.0.1:8787/readyz
```

Example:

```json
{"ready":true}
```

---

## MCP tools

### `list_dir`

Lists entries under a workspace-relative directory.

Input:

```json
{
  "path": "."
}
```

### `read_file`

Reads a UTF-8 text file under the workspace.

Input:

```json
{
  "path": "README.md"
}
```

Large output is truncated to the configured MCP output limit.

### `write_file`

Writes a UTF-8 file under the workspace.

Requires:

```text
AGENT_ALLOW_WRITE=1
AGENT_MCP_TOKEN=<token>
```

Input:

```json
{
  "path": "notes/result.txt",
  "content": "finished\n"
}
```

Parent directories are created inside the rooted workspace as needed.

### `exec_command`

Runs a restricted native command.

Requires:

```text
AGENT_ALLOW_COMMAND_EXEC=1
AGENT_MCP_TOKEN=<token>
AGENT_ALLOWED_COMMANDS=<allowlist>
```

Example input:

```json
{
  "command": "uptime",
  "args": []
}
```

The executable must be present in the configured server allowlist **and** accepted by deterministic policy.

Custom working directories are not supported; commands execute from the workspace root.

---

## Testing the MCP endpoint manually

The current hand-written MCP endpoint implements protocol version:

```text
2025-06-18
```

### Initialize

Without a token in loopback read-only mode:

```bash
curl -sS http://127.0.0.1:8787/mcp \
  -H 'Content-Type: application/json' \
  --data '{
    "jsonrpc":"2.0",
    "id":1,
    "method":"initialize",
    "params":{}
  }'
```

With authentication:

```bash
curl -sS http://127.0.0.1:8787/mcp \
  -H 'Authorization: Bearer YOUR_MCP_TOKEN' \
  -H 'Content-Type: application/json' \
  --data '{
    "jsonrpc":"2.0",
    "id":1,
    "method":"initialize",
    "params":{}
  }'
```

### List tools

```bash
curl -sS http://127.0.0.1:8787/mcp \
  -H 'Authorization: Bearer YOUR_MCP_TOKEN' \
  -H 'Content-Type: application/json' \
  --data '{
    "jsonrpc":"2.0",
    "id":2,
    "method":"tools/list",
    "params":{}
  }'
```

### Read a file

```bash
curl -sS http://127.0.0.1:8787/mcp \
  -H 'Authorization: Bearer YOUR_MCP_TOKEN' \
  -H 'Content-Type: application/json' \
  --data '{
    "jsonrpc":"2.0",
    "id":3,
    "method":"tools/call",
    "params":{
      "name":"read_file",
      "arguments":{"path":"hello.txt"}
    }
  }'
```

### Run a restricted command

```bash
curl -sS http://127.0.0.1:8787/mcp \
  -H 'Authorization: Bearer YOUR_MCP_TOKEN' \
  -H 'Content-Type: application/json' \
  --data '{
    "jsonrpc":"2.0",
    "id":4,
    "method":"tools/call",
    "params":{
      "name":"exec_command",
      "arguments":{"command":"uptime","args":[]}
    }
  }'
```

---

## Enabling writes

Set a strong MCP token and explicitly enable writes:

```bash
export AGENT_MCP_TOKEN='replace-with-a-long-random-secret'
export AGENT_ALLOW_WRITE=1
export AGENT_WORKSPACE=/var/lib/gpt-go-agent/workspace

./gpt-go-agent
```

The server refuses to start with writes enabled and an empty MCP token.

A simple secret can be generated with a local tool such as:

```bash
openssl rand -hex 32
```

Treat the token as a password.

---

## Enabling restricted command execution

Example:

```bash
export AGENT_MCP_TOKEN='replace-with-a-long-random-secret'
export AGENT_ALLOW_COMMAND_EXEC=1
export AGENT_ALLOWED_COMMANDS='echo uptime pwd date uname id ls'

./gpt-go-agent
```

The allowlist and deterministic policy are intersected.

For example:

```bash
AGENT_ALLOWED_COMMANDS='python3 uptime'
```

does not make Python executable through MCP. `uptime` can pass; `python3` is rejected by deterministic policy.

If your actual product requirement is arbitrary remote code execution, do not weaken this restricted tool silently. Introduce a separately named privileged execution mode with explicit trust and deployment assumptions.

---

## Connecting a remote MCP client

The default server binds to loopback.

Recommended topology:

```text
ChatGPT / remote MCP client
        |
        | secure tunnel / private network
        v
127.0.0.1:8787 on your VPS
        |
        v
gpt-go-agent
```

For any non-loopback deployment:

- configure `AGENT_MCP_TOKEN`;
- use TLS or a trusted secure tunnel;
- do not expose an unauthenticated HTTP endpoint directly to the internet;
- keep the workspace limited to files the agent genuinely needs.

Exact MCP-client setup differs between clients. The endpoint to configure is:

```text
POST /mcp
```

with:

```http
Authorization: Bearer <AGENT_MCP_TOKEN>
```

when authentication is enabled.

---

## Webhook worker mode

Webhook mode is independent from normal MCP operation and is disabled by default.

Enable it with:

```bash
export AGENT_WEBHOOK_ENABLED=1
export AGENT_WEBHOOK_TOKEN='replace-with-a-separate-random-secret'

export AGENT_MIDDLEMAN_URL='http://127.0.0.1:20128/v1'
export AGENT_MIDDLEMAN_MODEL='glm-5.3'
export AGENT_MIDDLEMAN_KEY='...'

./gpt-go-agent
```

When enabled, these endpoints are mounted:

```text
POST /webhook
GET  /webhook?limit=50&offset=0
GET  /webhook/{job-id}
```

When webhook mode is disabled, these routes are not mounted.

### Create a job

```bash
curl -sS -X POST http://127.0.0.1:8787/webhook \
  -H 'Authorization: Bearer YOUR_WEBHOOK_TOKEN' \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: example-job-001' \
  -H 'X-Request-ID: local-test-001' \
  --data '{
    "intent":"check server uptime",
    "context":"diagnostic request"
  }'
```

A new job normally returns HTTP `202 Accepted`.

Example response:

```json
{
  "job_id": "job-...",
  "status": "pending"
}
```

### Poll a job

```bash
curl -sS http://127.0.0.1:8787/webhook/JOB_ID \
  -H 'Authorization: Bearer YOUR_WEBHOOK_TOKEN'
```

### List jobs

```bash
curl -sS 'http://127.0.0.1:8787/webhook?limit=50&offset=0' \
  -H 'Authorization: Bearer YOUR_WEBHOOK_TOKEN'
```

Pagination constraints:

- default limit: 50;
- maximum limit: 200;
- offset must be non-negative.

### Idempotency

If the same `Idempotency-Key` is reused with the same request content, the existing job is returned instead of creating another job.

If the same key is reused with different content, the server returns HTTP `409 Conflict`.

This makes retries from webhook callers safer.

### Correlation IDs

Send:

```http
X-Request-ID: your-correlation-id
```

The ID is stored with the job and included in audit records.

If no request ID is provided, the worker generates one.

### Job states

Jobs can move through these states:

```text
pending
evaluating
rejected
running
completed
failed
```

Typical successful lifecycle:

```text
pending
   |
   v
evaluating
   |
   v
running
   |
   v
completed
```

Policy rejection:

```text
pending -> evaluating -> rejected
```

Operational failure:

```text
pending -> evaluating/running -> failed
```

Retryable Middleman failures can move a job back to `pending` before another bounded attempt.

### Durable store

Default path:

```text
/var/lib/gpt-go-agent/jobs/store.json
```

The store uses:

1. a temporary file;
2. file `fsync`;
3. primary-to-backup rename;
4. temp-to-primary rename;
5. directory `fsync`.

At startup:

- a valid primary is preferred;
- a missing/corrupt primary falls back to `.bak`;
- if both are absent, the store starts empty;
- if both exist but are invalid, startup fails rather than silently inventing state.

This store is appropriate for modest workloads. See [Known architectural follow-ups](#known-architectural-follow-ups) for the high-volume storage direction.

---

## Codex execution

Webhook decisions may dispatch a coding task to Codex.

Codex is **read-only by default**:

```text
AGENT_CODEX_ALLOW_WRITE=0
```

The effective Codex sandbox is:

```text
read-only
```

To deliberately grant workspace mutation:

```bash
export AGENT_CODEX_ALLOW_WRITE=1
```

The effective sandbox then becomes:

```text
workspace-write
```

Codex write permission is intentionally separate from MCP `AGENT_ALLOW_WRITE`.

That means:

```text
AGENT_ALLOW_WRITE=0
AGENT_CODEX_ALLOW_WRITE=1
```

is a valid configuration: MCP cannot call `write_file`, while the Codex webhook executor may still modify its workspace.

Similarly:

```text
AGENT_ALLOW_WRITE=1
AGENT_CODEX_ALLOW_WRITE=0
```

allows authenticated MCP file writes while Codex remains read-only.

---

## Health, readiness, and metrics

### `GET /healthz`

Returns process health and version:

```bash
curl -sS http://127.0.0.1:8787/healthz
```

Example:

```json
{"ok":true,"version":"0.2.1-dev"}
```

Release builds override the development version through linker flags.

### `GET /readyz`

MCP-only mode is ready after startup.

Webhook mode additionally checks that the required Middleman configuration is present.

```bash
curl -i http://127.0.0.1:8787/readyz
```

A non-ready service returns HTTP `503`.

### `GET /metrics`

Prometheus-text-style metrics are exposed at:

```bash
curl -sS http://127.0.0.1:8787/metrics
```

In MCP-only mode, the endpoint may be empty because worker metrics are not registered.

Webhook mode can expose metrics such as:

```text
gpt_go_agent_jobs_total
gpt_go_agent_queue_depth
gpt_go_agent_jobs_pending
gpt_go_agent_jobs_evaluating
gpt_go_agent_jobs_running
gpt_go_agent_jobs_completed
gpt_go_agent_jobs_failed
gpt_go_agent_jobs_rejected
gpt_go_agent_jobs_retried
gpt_go_agent_jobs_dead_lettered
```

Middleman metrics are merged into the same output when available.

---

## Audit logging

Default audit path:

```text
agent-audit.jsonl
```

Production deployments usually set:

```text
AGENT_AUDIT_PATH=/var/lib/gpt-go-agent/agent-audit.jsonl
```

The file is opened with mode `0600`.

Each line is a JSON object.

Representative event:

```json
{
  "time":"2026-10-01T00:00:00Z",
  "action":"read_file",
  "target":"README.md",
  "allowed":true
}
```

Webhook events can also contain:

- correlation ID;
- decision details;
- duration;
- failure information.

Audit timestamps are normalized to UTC.

---

## Configuration reference

### Core / MCP

| Variable | Default | Purpose |
|---|---|---|
| `AGENT_LISTEN_ADDR` | `127.0.0.1:8787` | HTTP listen address |
| `AGENT_WORKSPACE` | `.` | Root directory exposed to MCP/executors |
| `AGENT_MCP_TOKEN` | empty | MCP bearer token |
| `AGENT_ALLOW_WRITE` | `0` | Enable MCP `write_file` |
| `AGENT_ALLOW_COMMAND_EXEC` | `0` | Enable restricted MCP native execution |
| `AGENT_ALLOWED_COMMANDS` | empty unless configured | Server-side executable allowlist |
| `AGENT_AUDIT_PATH` | `agent-audit.jsonl` | Audit JSONL path |

Recommended restricted allowlist:

```text
echo,uptime,pwd,date,uname,id,ls
```

### Webhook

| Variable | Default | Purpose |
|---|---|---|
| `AGENT_WEBHOOK_ENABLED` | `0` | Mount/start webhook worker |
| `AGENT_WEBHOOK_TOKEN` | empty | Webhook bearer token |
| `AGENT_WEBHOOK_WORKERS` | `4` | Background worker count |
| `AGENT_WEBHOOK_STORE` | `/var/lib/gpt-go-agent/jobs/store.json` | Durable job store |
| `AGENT_WEBHOOK_RETENTION` | `168h` | Terminal job retention |
| `AGENT_WEBHOOK_CLEANUP_INTERVAL` | `1h` | Retention cleanup interval |
| `AGENT_WEBHOOK_MAX_GATEKEEPER_RETRIES` | `2` | Worker-level Middleman retries |
| `AGENT_WEBHOOK_RETRY_BASE` | `500ms` | Worker retry base delay |
| `AGENT_WEBHOOK_MAX_OUTPUT_BYTES` | `65536` | Native/Codex output cap |

`OPENAI_WEBHOOK_SECRET` is accepted as a backwards-compatible fallback for `AGENT_WEBHOOK_TOKEN`, but new deployments should use `AGENT_WEBHOOK_TOKEN`.

### Middleman

| Variable | Default | Purpose |
|---|---|---|
| `AGENT_MIDDLEMAN_URL` | `http://127.0.0.1:20128/v1` | OpenAI-compatible API base |
| `AGENT_MIDDLEMAN_KEY` | empty | Middleman API key |
| `AGENT_MIDDLEMAN_MODEL` | `glm-5.3` | Model identifier |
| `AGENT_MIDDLEMAN_TIMEOUT` | `30s` | Middleman request timeout |
| `AGENT_MIDDLEMAN_MAX_ATTEMPTS` | `3` | Transport-level attempts |
| `AGENT_MIDDLEMAN_RETRY_BASE` | `250ms` | Retry base delay |
| `AGENT_MIDDLEMAN_CIRCUIT_THRESHOLD` | `3` | Failures before breaker opens |
| `AGENT_MIDDLEMAN_CIRCUIT_OPEN` | `10s` | Circuit open duration |

### Codex

| Variable | Default | Purpose |
|---|---|---|
| `AGENT_CODEX_BIN` | `codex` | Codex executable |
| `AGENT_CODEX_ALLOW_WRITE` | `0` | Switch sandbox from read-only to workspace-write |

The complete example is in:

```text
deploy/env.example
```

---

## Systemd deployment

The repository includes:

```text
deploy/gpt-go-agent.service
```

The service expects:

```text
User=gptagent
Group=gptagent
WorkingDirectory=/var/lib/gpt-go-agent
EnvironmentFile=/etc/gpt-go-agent/env
ExecStart=/usr/local/bin/gpt-go-agent
```

A typical host preparation looks like:

```bash
sudo useradd --system --home /var/lib/gpt-go-agent --shell /usr/sbin/nologin gptagent

sudo install -d -o gptagent -g gptagent -m 0700 \
  /var/lib/gpt-go-agent \
  /var/lib/gpt-go-agent/workspace \
  /var/lib/gpt-go-agent/jobs

sudo install -d -o root -g root -m 0755 /etc/gpt-go-agent
sudo install -o root -g root -m 0600 deploy/env.example /etc/gpt-go-agent/env

sudo install -o root -g root -m 0644 \
  deploy/gpt-go-agent.service \
  /etc/systemd/system/gpt-go-agent.service
```

Edit the production environment:

```bash
sudo editor /etc/gpt-go-agent/env
```

Then:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now gpt-go-agent
sudo systemctl status gpt-go-agent
```

Logs:

```bash
journalctl -u gpt-go-agent -f
```

Health from the host:

```bash
curl -fsS http://127.0.0.1:8787/healthz
curl -fsS http://127.0.0.1:8787/readyz
```

---

## Release and deployment workflow

Release tags follow:

```text
vMAJOR.MINOR.PATCH
```

The GitHub release workflow builds:

- Linux amd64;
- Linux arm64;
- SHA256 checksums.

The deploy helper:

```text
scripts/deploy.sh
```

performs:

1. executable validation;
2. staged install;
3. SHA256 output;
4. backup of the previous binary;
5. systemd stop/start;
6. `/healthz` and `/readyz` checks;
7. rollback to the previous binary if deployment health fails.

Usage:

```bash
sudo ./scripts/deploy.sh /path/to/gpt-go-agent
```

---

## Testing and CI

Local baseline:

```bash
go test ./...
go test -race ./...
go vet ./...
go test -cover ./...
go build ./...
```

CI additionally runs:

- Staticcheck;
- Gosec;
- Govulncheck.

The application itself targets Go 1.24 in CI. Govulncheck uses a newer supported Go toolchain for the current vulnerability-scanner release; this does not change the application's build target.

Security regression coverage includes:

- parent traversal rejection;
- symlink workspace escape;
- mutation/exec token requirements;
- restricted native commands;
- webhook authentication;
- webhook idempotency;
- bounded subprocess output;
- durable-store backup recovery;
- audit timestamp generation.

### Smoke test

Run:

```bash
./scripts/smoke.sh
```

By default it checks:

- `/healthz`;
- `/readyz`;
- `/metrics`.

To additionally verify that webhook authentication rejects unauthenticated requests:

```bash
CHECK_WEBHOOK_AUTH=1 ./scripts/smoke.sh
```

Use that option only when webhook mode is enabled.

---

## Troubleshooting

### Server refuses to start: MCP token required

Symptom:

```text
MCP token is required when writes or command execution are enabled
```

Cause:

You enabled:

```text
AGENT_ALLOW_WRITE=1
```

or:

```text
AGENT_ALLOW_COMMAND_EXEC=1
```

without configuring `AGENT_MCP_TOKEN`.

Fix:

```bash
export AGENT_MCP_TOKEN="$(openssl rand -hex 32)"
```

Then restart the service.

---

### Remote MCP request returns 401

Check:

1. `AGENT_MCP_TOKEN` is set on the daemon;
2. the client sends the same token;
3. the header format is exactly:

```http
Authorization: Bearer YOUR_TOKEN
```

Also check the service environment actually loaded:

```bash
sudo systemctl show gpt-go-agent --property=Environment
sudo journalctl -u gpt-go-agent -n 100
```

For secrets stored through `EnvironmentFile`, inspect that file directly with appropriate root permissions instead of printing secrets into shared logs.

---

### MCP works locally but not remotely

The default listener is loopback:

```text
127.0.0.1:8787
```

This is intentional.

Prefer a secure tunnel/private network rather than changing the daemon to a public bind.

If you deliberately change `AGENT_LISTEN_ADDR`, make sure:

- bearer authentication is configured;
- firewall rules are correct;
- TLS/tunneling is present;
- you understand the exposure.

Check listening sockets:

```bash
ss -ltnp | grep 8787
```

---

### `write_file` says writes are disabled

Enable:

```text
AGENT_ALLOW_WRITE=1
```

and configure:

```text
AGENT_MCP_TOKEN
```

Restart after changing environment values.

For systemd:

```bash
sudo systemctl restart gpt-go-agent
```

---

### Path rejected even though it looks inside the workspace

The server intentionally rejects path resolution that escapes through `..`, absolute paths, or symlinks.

Inspect the path:

```bash
readlink -f /var/lib/gpt-go-agent/workspace/path/to/item
```

If it resolves outside `AGENT_WORKSPACE`, the rejection is expected.

Move/copy the required data into the workspace instead of weakening the root boundary.

---

### `exec_command` says command is not allowlisted

Add the executable name to:

```text
AGENT_ALLOWED_COMMANDS
```

but remember that this is only gate one.

Example:

```text
AGENT_ALLOWED_COMMANDS=uptime,pwd,date
```

Restart the service.

---

### Command is allowlisted but still says it is not permitted

That means deterministic restricted policy rejected it.

This is expected for general execution primitives such as:

```text
python3
node
bash
git
go
make
curl
```

Do not treat `AGENT_ALLOWED_COMMANDS` as a way to bypass policy.

If a new native capability is genuinely needed, implement a narrow validator for that command/subcommand in the deterministic policy and add tests.

---

### Custom `cwd` is rejected

Restricted MCP native execution intentionally runs from the workspace root.

Use workspace-relative operations through the file tools instead.

If a future command needs a subdirectory, add a narrowly validated server-side operation rather than reopening arbitrary `cwd` path handling.

---

### `/webhook` returns 404

Webhook mode is probably disabled.

Set:

```text
AGENT_WEBHOOK_ENABLED=1
AGENT_WEBHOOK_TOKEN=<secret>
```

and restart.

When webhook mode is disabled, webhook routes are intentionally not mounted.

---

### Webhook returns 401

Verify:

```text
AGENT_WEBHOOK_TOKEN
```

and send:

```http
Authorization: Bearer YOUR_WEBHOOK_TOKEN
```

The MCP token and webhook token are separate credentials.

---

### Webhook POST returns 409

You reused an `Idempotency-Key` with different intent/context content.

Use either:

- the original request body for that key; or
- a new idempotency key.

---

### Webhook returns 429

The request limiter has been exceeded.

Respect the `Retry-After` header and retry later.

If sustained traffic is expected, first evaluate worker capacity and storage scalability instead of simply raising the request limit.

---

### Webhook returns 503 or jobs fail during Middleman evaluation

Check:

```text
AGENT_MIDDLEMAN_URL
AGENT_MIDDLEMAN_KEY
AGENT_MIDDLEMAN_MODEL
```

Verify connectivity from the service host.

Check logs:

```bash
journalctl -u gpt-go-agent -n 200
```

Also inspect `/metrics` for Middleman retry/circuit information when available.

---

### Codex job fails: executable not found

Check:

```text
AGENT_CODEX_BIN
```

Verify from the service environment:

```bash
command -v codex
```

For systemd deployments, remember that the service's `PATH` may differ from your interactive shell.

Use an explicit absolute executable path in `AGENT_CODEX_BIN` when necessary.

---

### Codex can read but cannot modify files

That is the default.

Set:

```text
AGENT_CODEX_ALLOW_WRITE=1
```

only if workspace mutation is intentionally required.

Restart afterward.

---

### Job store fails to load

Default location:

```text
/var/lib/gpt-go-agent/jobs/store.json
```

Check:

```bash
ls -la /var/lib/gpt-go-agent/jobs/
```

The service account must be able to write the directory.

If the primary is corrupt/missing, the loader automatically tries:

```text
store.json.bak
```

If both primary and backup exist but are invalid, startup fails intentionally so corrupted state is not silently discarded.

---

### Permission denied under systemd

The service runs as:

```text
gptagent:gptagent
```

and systemd only permits writes under:

```text
/var/lib/gpt-go-agent
```

Check ownership:

```bash
sudo chown -R gptagent:gptagent /var/lib/gpt-go-agent
sudo chmod 0700 /var/lib/gpt-go-agent
```

Do not broadly relax `ProtectSystem` or filesystem permissions just to make one path work. Prefer moving writable agent state under the intended service directory.

---

### `/readyz` returns 503

In MCP-only mode this should normally be ready after startup.

In webhook mode, verify Middleman URL/model configuration.

Inspect:

```bash
curl -i http://127.0.0.1:8787/readyz
journalctl -u gpt-go-agent -n 100
```

---

### `/metrics` is empty

This is normal in MCP-only mode.

Worker metrics are registered only when webhook mode is enabled.

---

### Audit log is missing

Check:

```text
AGENT_AUDIT_PATH
```

and parent-directory permissions.

Production example:

```text
AGENT_AUDIT_PATH=/var/lib/gpt-go-agent/agent-audit.jsonl
```

The service account must be able to create/write the file.

---

### Deployment script rolls back

`scripts/deploy.sh` rolls back if:

- systemd fails to become active;
- `/healthz` fails;
- `/readyz` fails.

Inspect:

```bash
systemctl status gpt-go-agent
journalctl -u gpt-go-agent -n 200
```

Fix readiness/configuration before retrying deployment.

---

## Operational recommendations

For a small private deployment:

1. keep `AGENT_LISTEN_ADDR=127.0.0.1:8787`;
2. use a secure tunnel/private network for remote MCP access;
3. configure a long random MCP token before enabling mutation or execution;
4. keep `AGENT_ALLOW_WRITE=0` unless needed;
5. keep `AGENT_ALLOW_COMMAND_EXEC=0` unless needed;
6. use the minimal native allowlist required;
7. keep webhook mode disabled unless you actually use it;
8. use a separate webhook token from the MCP token;
9. keep Codex read-only unless coding jobs must modify files;
10. keep the workspace narrowly scoped;
11. review the audit log;
12. monitor `/readyz`, queue depth, failures, and disk usage;
13. back up any job/audit state you care about.

For production exposure, also add:

- TLS or a trusted secure tunnel;
- firewall rules;
- secret rotation;
- log retention;
- monitoring/alerts;
- regular dependency and OS updates.

---

## Known architectural follow-ups

### Job persistence

The current webhook store is a durable JSON snapshot store. It rewrites the retained job map on persistence updates.

This is intentionally simple and operationally lightweight, but it is not the right long-term backend for sustained high-volume job traffic.

The recommended future direction is:

```text
SQLite
+ WAL
+ indexed pagination
+ UNIQUE idempotency keys
+ transactional state transitions
+ efficient retention deletes
```

### MCP protocol implementation

The MCP wire layer is currently hand-written and pinned to:

```text
2025-06-18
```

A future compatibility-focused change should migrate the transport/protocol layer to the official MCP Go SDK with explicit client compatibility tests.

That migration should remain separate from execution-policy changes so protocol behavior and security behavior can be reviewed independently.

### Execution capabilities

The restricted command surface is intentionally small.

If additional capabilities are needed, prefer adding **narrow typed tools or narrowly validated subcommands** rather than expanding toward arbitrary process execution.

---

## License / ownership

This repository is currently maintained as a private project. Add an explicit license before redistributing it publicly.
