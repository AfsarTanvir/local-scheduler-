# local-scheduler — Roadmap

A containerized job scheduler built step by step, published as `afsartanvir/local-scheduler` on Docker Hub.

## Design principles — usable from any platform, simple but good

**Anyone can use it, from any language**
- The only interface is a plain **HTTP + JSON REST API**. Anything that can send an HTTP request can use it (curl, Python, Node, Java, .NET, PHP, Go, Postman, ...).
- Publish an **OpenAPI spec** (`/openapi.json`) so people can generate a client in their language.
- The main job type is **`http`** (call a URL), so the scheduler can trigger a service written in any language.
- Results can be sent back to the caller with an optional **webhook** (`onSuccess` / `onFailure` URL).

**Runs anywhere**
- Docker image for **linux/amd64 and linux/arm64** (normal servers, Apple Silicon Macs, Raspberry Pi, AWS Graviton).
- Standalone binaries for **Linux, macOS and Windows**, so people without Docker can use it too.
- `shell` jobs use `sh` on Linux/macOS and `cmd` / PowerShell on Windows; document this clearly.

**Simple by default, more power when needed**
- **Zero required dependencies:** one binary or container, with SQLite built in. `docker run` and it works.
- PostgreSQL and Redis are **optional**, enabled only by setting `DATABASE_URL` / `REDIS_URL`.
- All configuration via **environment variables**, with sensible defaults.
- Times are stored in UTC; each job can set its own `timezone`.
- Optional API key (`API_KEY` env var); with no key set, it runs open for local use.

**Language recommendation:** **Go**. It compiles to a single small binary for every OS/CPU, needs no runtime, makes tiny Docker images, and is what Ofelia, Supercronic, Docker and Kubernetes are written in. Alternative: .NET with self-contained single-file publish (works, but bigger binaries).

**Rules for every step**

1. Write the research questions first.
2. See how existing tools answer them (Ofelia, Supercronic, Quartz.NET, Cronicle).
3. Write a short decision note in `docs/decisions/NNN-topic.md` (problem → options → choice → why).
4. Build it, and add a test that proves it.
5. Commit, and tag the release (`v0.1`, `v0.2`, ...).

Don't start a step until the previous step's "Done when" is true.

---

## Step 0 — Setup (1 day)

- [ ] Choose a language: **Go** (recommended, see above) or **.NET** (self-contained publish)
- [ ] Install Go: https://go.dev/doc/install, then do the "Tour of Go" (https://go.dev/tour)
- [ ] `git init`, add `.gitignore` and `README.md`
- [ ] Create a GitHub repo `local-scheduler` and push
- [ ] Create a Docker Hub account (`afsartanvir`)
- [ ] Create the project skeleton with a `GET /health` endpoint
- [ ] Write a simple `Dockerfile`
- [ ] Create `docs/decisions/001-language-choice.md`

**Done when:** `docker build -t local-scheduler . && docker run -p 8080:8080 local-scheduler` serves `/health`.

---

## Step 1 — In-memory MVP (1–2 weeks) → `v0.1`

**Research questions**
- How does the cron format work? (`man 5 crontab`, https://crontab.guru)
- How does a scheduler loop work: sleep until the next due time, or tick every second?
- How should time zones work? UTC or local time?
- What does graceful shutdown mean (SIGTERM, letting running jobs finish)?

**Build**
- [ ] Job model: `id`, `name`, `schedule`, `timezone`, `type` (`http` | `shell`), `target`, `enabled`
- [ ] HTTP job options: `method`, `headers`, `body`, expected status codes
- [ ] Consistent JSON errors: `{ "error": { "code": "...", "message": "..." } }`
- [ ] `GET /openapi.json` describing the whole API
- [ ] `POST /jobs`, `GET /jobs`, `GET /jobs/{id}`, `DELETE /jobs/{id}`
- [ ] `POST /jobs/{id}/run` (run now), `POST /jobs/{id}/pause`, `POST /jobs/{id}/resume`
- [ ] Scheduler loop that triggers due jobs
- [ ] HTTP executor and shell executor
- [ ] Per-job timeout
- [ ] Graceful shutdown

**Done when:** a job runs every minute, and `docker stop` waits for a running job to finish.

---

## Step 2 — Persistence (1–2 weeks) → `v0.2`

**Research questions**
- What tables do I need? (`jobs`, `executions`)
- How do I handle DB migrations?
- At-least-once vs at-most-once: which one do I want?

**Build**
- [ ] SQLite storage first (file in `/app/data`)
- [ ] `executions` table: `job_id`, `started_at`, `finished_at`, `status`, `output`, `error`
- [ ] `GET /jobs/{id}/executions` (history)
- [ ] Load jobs from DB on startup
- [ ] Add PostgreSQL as an **option** (behind the same storage interface), used only when `DATABASE_URL` is set
- [ ] SQLite stays the default, so a plain `docker run` still needs nothing else

**Done when:** restarting the container keeps all jobs and history, and scheduling continues, with both SQLite and PostgreSQL.

---

## Step 3 — Reliability (2 weeks) → `v0.3`

**Research questions**
- Retries: how many, how long between them? (exponential backoff + jitter)
- Missed jobs: if the scheduler is down at 02:00, run it on startup or skip it? (read Quartz.NET "misfire instructions")
- Overlap: if the 02:00 run is still going at 02:05, skip, queue, or run in parallel?
- Crash mid-run: is the job idempotent? Should it be re-run?

**Build**
- [ ] Per-job settings: `retries`, `backoff`, `timeoutSeconds`, `misfirePolicy`, `allowConcurrent`
- [ ] Mark executions stuck in `running` after a crash as `failed` / `abandoned`
- [ ] Decision notes: `misfire-policy.md`, `crash-recovery.md`

**Done when:** a test kills the scheduler mid-run, and the behavior matches the written policy.

---

## Step 4 — Publish to Docker Hub (2–3 days) → `v1.0`

- [ ] Multi-stage `Dockerfile` (small image, non-root user)
- [ ] `HEALTHCHECK` using `/health`
- [ ] Configuration via environment variables (port, DB connection, log level)
- [ ] `docker-compose.yml` example in the README
- [ ] GitHub Actions: build + test on push (Linux, macOS, Windows runners)
- [ ] Multi-arch image (`linux/amd64`, `linux/arm64`) with `docker buildx`, pushed to Docker Hub on git tag
- [ ] Standalone binaries for Linux / macOS / Windows attached to GitHub Releases (GoReleaser does this for Go)
- [ ] Good README: what it is, quick start (Docker **and** binary), API examples in curl, Python and JavaScript

**Done when:** `docker run -p 8080:8080 afsartanvir/local-scheduler` works on an Intel PC and an ARM machine, and the Windows `.exe` runs without installing anything.

---

## Step 5 — Redis + separate workers (2–3 weeks) → `v1.1`

**Research questions**
- Redis lists vs streams for queues
- `SET key value NX PX` locks: why are they tricky?
- How does a worker acknowledge a job? What happens if it dies mid-job?

**Build**
- [ ] Redis is **optional** (`REDIS_URL`); without it, everything still runs in one process
- [ ] Scheduler only enqueues; workers pull and execute
- [ ] Same image, different mode: `MODE=scheduler` / `MODE=worker`
- [ ] Worker heartbeats; re-queue jobs from dead workers

**Done when:** with 3 workers running, each execution happens exactly once.

---

## Step 6 — Multiple scheduler instances (2–3 weeks) → `v2.0`

**Research questions**
- Leader election vs job claiming
- PostgreSQL `SELECT ... FOR UPDATE SKIP LOCKED` and advisory locks
- Fencing tokens: read "How to do distributed locking" (Martin Kleppmann)
- Clock skew between machines

**Build**
- [ ] 2+ scheduler instances, no duplicate runs
- [ ] Failover when the leader dies

**Done when:** killing the leader causes no missed and no duplicated runs.

---

## Step 7 — Optional extras

- [ ] Web dashboard (jobs, history, run now)
- [ ] Prometheus metrics (`/metrics`)
- [ ] Structured JSON logs
- [ ] Webhooks on job success / failure
- [ ] Small CLI client (`lsched jobs list`, `lsched run <id>`)
- [ ] Job definitions from a YAML file
- [ ] Example client snippets for popular languages in `examples/`

---

## Reading list

1. `man 5 crontab` and https://crontab.guru
2. Quartz.NET docs — triggers and misfire instructions
3. Ofelia and Supercronic source code (GitHub)
4. *Designing Data-Intensive Applications* — chapters 8–9
5. Martin Kleppmann — "How to do distributed locking"

## Problem log

Write down hard problems you hit here (great for interviews):

| Date | Problem | What I learned |
|------|---------|----------------|
|      |         |                |
