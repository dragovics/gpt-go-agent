# gpt-go-agent

A lightweight execution agent designed for ChatGPT/Codex-controlled remote machines.

## Goal

Keep reasoning in ChatGPT/Codex while this agent provides a controlled execution surface on the user's machine:

`ChatGPT/Codex → agent → terminal/files/processes → results → ChatGPT/Codex`

The agent does not contain its own LLM.

## Status

Initial skeleton. The next milestone is a secure local/remote transport and an explicit tool protocol for terminal, filesystem, process, SSH, and Docker operations.

## Security principles

- Explicitly bind to localhost by default.
- Require authentication before remote access.
- Keep an auditable execution log.
- Separate read-only and side-effecting operations.
- Add command/time/output limits.
- Never store credentials in source control.
