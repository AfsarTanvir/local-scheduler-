# local-scheduler

A small, self-hosted job scheduler with an HTTP API.
Tell it **when** (a cron schedule or a one-time date) and **what** (call a URL or run a command), and it does it.

- Works with **any language**: everything goes through a plain HTTP + JSON API.
- **One binary, no dependencies**: the database (SQLite) is built in; no server to install.
- Runs on **Linux, macOS and Windows**, with or without Docker.

> Status: early (roadmap Step 2 done). Jobs and run history are saved in SQLite. See [ROADMAP.md](ROADMAP.md).

## How it works

```text
 your app / curl / Postman
          │  POST /jobs  {"schedule": "0 2 * * *", "http": {"url": ...}}
          ▼
 ┌──────────────────┐     ┌─────────────┐
 │   HTTP API       │────▶│    Store    │  SQLite: jobs + run history
 └──────────────────┘     └──────┬──────┘
                                 │ every second: "which jobs are due?"
                          ┌──────┴──────┐
                          │  Scheduler  │
                          └──────┬──────┘
                                 │ runs each due job in a goroutine
                          ┌──────┴──────┐
                          │   Runner    │──▶ HTTP request  or  shell command
                          └─────────────┘
```

1. You create a job with a `schedule` (repeats) or a `runAt` (runs once).
2. The scheduler computes `nextRunAt`.
3. Every second it starts jobs whose `nextRunAt` has passed, and moves `nextRunAt` forward.
4. Each run is recorded in the history; the latest result is also shown as `lastRun` on the job.

## Quick start

**With Docker**

```bash
docker build -t local-scheduler .
docker run -p 8080:8080 -v scheduler-data:/app/data local-scheduler
```

The volume keeps jobs and history when the container is removed. With Docker Compose:

```yaml
services:
  scheduler:
    build: .
    ports:
      - "8080:8080"
    volumes:
      - scheduler-data:/app/data
    # environment:
    #   ALLOW_SHELL_JOBS: "true"

volumes:
  scheduler-data:
```

**With Go** (1.26 or newer)

```bash
go run ./cmd/local-scheduler     # database file: data/scheduler.db
```

Check it is up:

```bash
curl localhost:8080/health
# {"status":"ok"}
```

## Examples

**Call a URL every day at 02:00 (Dhaka time)**

```bash
curl -X POST localhost:8080/jobs -d '{
  "name": "backup-database",
  "schedule": "0 2 * * *",
  "timezone": "Asia/Dhaka",
  "type": "http",
  "http": {
    "url": "http://my-app:5000/backup",
    "method": "POST",
    "headers": {"Authorization": "Bearer my-token"},
    "body": "{\"db\": \"main\"}"
  }
}'
```

**Call a URL once, at a given time**

```bash
curl -X POST localhost:8080/jobs -d '{
  "name": "send-reminder",
  "runAt": "2026-10-04T09:00:00Z",
  "type": "http",
  "http": {"url": "http://my-app:5000/remind", "method": "POST"}
}'
```

**Run a command every 5 minutes** (start the server with `ALLOW_SHELL_JOBS=true`)

```bash
curl -X POST localhost:8080/jobs -d '{
  "name": "cleanup",
  "schedule": "*/5 * * * *",
  "type": "shell",
  "shell": {"command": "echo cleaning up"}
}'
```

**See the result**

```bash
curl localhost:8080/jobs/<id>          # the job, with its latest run
curl localhost:8080/jobs/<id>/runs     # run history, newest first
```

```json
{
  "id": "e3d8909bd7dce324",
  "name": "send-reminder",
  "enabled": true,
  "running": false,
  "lastRun": {
    "startedAt": "2026-10-04T09:00:00Z",
    "finishedAt": "2026-10-04T09:00:00Z",
    "status": "success",
    "output": "200 OK\nreminder sent"
  }
}
```

**From Python** (any language works the same way)

```python
import requests

job = requests.post("http://localhost:8080/jobs", json={
    "name": "ping",
    "schedule": "@every 10m",
    "type": "http",
    "http": {"url": "https://example.com/health"},
}).json()
print(job["id"], job["nextRunAt"])
```

> **Docker tip:** inside the container, `localhost` is the container itself.
> To call an app on your machine, use `http://host.docker.internal:<port>`
> (on Linux add `--add-host=host.docker.internal:host-gateway` to `docker run`).

## API

| Method | Path | What it does |
|---|---|---|
| `GET` | `/health` | Service is up |
| `GET` | `/openapi.yaml` | Full API description (OpenAPI 3) |
| `POST` | `/jobs` | Create a job |
| `GET` | `/jobs` | List jobs |
| `GET` | `/jobs/{id}` | Get a job and its last run |
| `GET` | `/jobs/{id}/runs?limit=20` | Run history, newest first (100 kept per job) |
| `DELETE` | `/jobs/{id}` | Delete a job |
| `POST` | `/jobs/{id}/run` | Run now (in the background) |
| `POST` | `/jobs/{id}/pause` | Pause |
| `POST` | `/jobs/{id}/resume` | Resume |

Errors always have the same shape:

```json
{"error": {"code": "invalid_job", "message": "either schedule or runAt is required"}}
```

The full description is in [internal/api/openapi.yaml](internal/api/openapi.yaml). Open it in
[editor.swagger.io](https://editor.swagger.io) or import it into Postman.

### Job fields

| Field | Required | Description |
|---|---|---|
| `name` | yes | Any name |
| `schedule` | one of these two | Cron: `minute hour day month weekday`, e.g. `0 2 * * *`. Also `@hourly`, `@daily`, `@every 10m` |
| `runAt` | one of these two | One-time run, RFC 3339, e.g. `2026-10-04T09:00:00Z` |
| `timezone` | no | Timezone for `schedule`, e.g. `Asia/Dhaka`. Default `UTC` |
| `type` | yes | `http` or `shell` |
| `http` | for `http` | `url`, `method` (default `GET`), `headers`, `body`. Any 2xx response is a success |
| `shell` | for `shell` | `command`. Runs with `sh -c` (Linux/macOS) or `cmd /C` (Windows). Exit code 0 is a success |
| `timeoutSeconds` | no | Max duration of one run. Default 30, max 3600 |

### Rules

- **No overlap:** if a job is still running when its next run is due, that run is skipped.
- **Missed runs** (paused, machine asleep) turn into one catch-up run, then the normal schedule continues.
- **Shutdown** (`Ctrl+C` / `docker stop`): no new runs start; running jobs get 30 seconds to finish.
- **Crash** (`kill -9`, power loss): on the next start, runs that were in progress are marked `failed`
  ("interrupted") and are not repeated. Their jobs continue with the next scheduled run.

Why these rules: [docs/decisions/](docs/decisions/).

## Configuration

| Environment variable | Default | Description |
|---|---|---|
| `PORT` | `8080` | HTTP port |
| `DB_PATH` | `data/scheduler.db` (`/app/data/scheduler.db` in Docker) | SQLite database file. Created if missing |
| `ALLOW_SHELL_JOBS` | `false` | Set to `true` to allow shell jobs. They run commands on this machine, so only enable this when the API is not reachable by others |

## Project structure

```text
cmd/local-scheduler/   main.go: reads config, wires everything, handles shutdown
internal/job/          what a job is: model, validation, next-run calculation
internal/store/        SQLite storage: jobs, run history, migrations, crash recovery
internal/scheduler/    the loop that starts due jobs
internal/runner/       executes one run: HTTP request or shell command
internal/api/          HTTP handlers and the OpenAPI spec
docs/decisions/        why things are built the way they are
```

## Development

```bash
go test ./...          # run all tests
go vet ./...           # static checks
gofmt -l .             # list badly formatted files (should print nothing)
./scripts/e2e/run.sh   # end-to-end test against the real binary (~40 s, needs curl and python3)
```

The end-to-end test starts the real server with a fake API next to it and checks
the behavior from the outside: scheduling, failures and timeouts, pause/resume,
restarts, graceful shutdown, recovery after `kill -9`, and memory use.
