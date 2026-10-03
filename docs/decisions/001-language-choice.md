# 001 — Use Go

**Status:** accepted · 2026-10-03

## Problem
The scheduler must be usable on any platform: Linux, macOS, Windows,
Intel and ARM, with or without Docker. It should be simple to install.

## Options
| Option | Pros | Cons |
|---|---|---|
| Go | One static binary per OS/CPU, no runtime needed, ~15 MB Docker image, standard for infra tools (Docker, Kubernetes, Ofelia, Supercronic) | New language for me |
| .NET | I know it already, good libraries | Self-contained binaries are much bigger; runtime needed otherwise |
| Node/TypeScript | I know it already | Needs Node installed; single-binary builds are awkward |

## Decision
Go. Cross-compiling is one command (`GOOS=windows GOARCH=amd64 go build`),
and the standard library already has a good HTTP server, JSON, timers,
contexts and process execution, so very few dependencies are needed.

## Consequences
- Dependencies are kept minimal: only a cron expression parser for now.
- I need to learn Go basics (Tour of Go).
