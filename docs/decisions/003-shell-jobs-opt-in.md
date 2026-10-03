# 003 — Shell jobs are disabled by default

**Status:** accepted · 2026-10-03

## Problem
A shell job runs any command on the machine. If the API is reachable by
someone else (open port, shared network), creating a shell job means they
can run commands on the server: remote code execution.

## Options
1. Always allow shell jobs: easiest, but unsafe by default.
2. Never allow shell jobs: safe, but loses a useful feature.
3. Allow them only when the operator turns them on: `ALLOW_SHELL_JOBS=true`.

## Decision
Option 3. Without the setting, creating a shell job returns
`403 shell_disabled` with a message explaining how to enable it.
HTTP jobs, the main feature, always work.

## Consequences
- Safe by default; one environment variable to opt in.
- Later (roadmap Step 7) an API key adds protection for the whole API.
