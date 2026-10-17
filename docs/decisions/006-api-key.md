# 006 — Optional API key

**Status:** accepted · 2026-10-11

## Problem
Anyone who can reach the port can create, pause or delete jobs. An http job
makes the scheduler send requests from inside your network, so an open API
on a shared network is a way in. Another app (for example MockFlow) now
talks to the scheduler over the network, so the API needs a way to say
"only callers that know the secret".

## Options
1. No auth, rely on the network (bind to localhost, firewall). Simple, but one
   wrong `-p 8080:8080` exposes everything.
2. An API key in a header, set by an environment variable.
3. Users and tokens stored in the database. More than a single-tenant
   scheduler needs.

## Decision
Option 2, optional:

- `API_KEY` set → every `/jobs` route needs `Authorization: Bearer <API_KEY>`.
  A missing or wrong key gets `401` with code `unauthorized` and a
  `WWW-Authenticate` header.
- `API_KEY` not set → open, same as before, so `docker run` still works with
  no configuration. Startup logs that the API is open.
- `/health` and `/openapi.yaml` stay public: health checks (Docker,
  load balancers) have no key, and the API description is not a secret.
- The check compares SHA-256 hashes of the header and the expected value with
  `subtle.ConstantTimeCompare`, so response time doesn't reveal how much of
  the key was right, or its length.
- The standard `Authorization: Bearer` header, so every HTTP client and the
  OpenAPI spec (`securitySchemes`) support it without anything custom.

## Consequences
- One shared key: no per-client keys and no rotation without a restart.
  Fine for one app talking to its own scheduler.
- The key travels in a header, so use HTTPS (a reverse proxy) when the
  scheduler is reached over an untrusted network.
- Shell jobs are still opt-in (`003`); with a key, only key holders can create them.
