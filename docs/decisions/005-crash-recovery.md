# 005 — What happens to runs interrupted by a crash

**Status:** accepted · 2026-10-03

## Problem
With a database, state survives restarts, including "this job is running".
If the process is killed during a run (`kill -9`, out of memory, power
loss), the job stays `running = true` forever. The scheduler skips running
jobs, so **it would never run again**.

And the real question: should an interrupted run be run again?
```text
02:00  job starts
02:01  scheduler crashes
02:02  scheduler restarts → run the 02:00 job again?
```

## Options
| Option | Meaning | Risk |
|---|---|---|
| At-most-once | Mark the run failed, don't repeat it | The work may be half done and never finished |
| At-least-once | Run it again on startup | The work may happen twice (two backups, two emails) |

Neither is right for every job: it depends on whether the job is
**idempotent** (safe to run twice).

## Decision
**At-most-once by default.** On startup, before the scheduler starts:
1. Every run still marked `running` is marked `failed` with the error
   `interrupted: the scheduler stopped while this job was running`.
2. Every job still marked `running` is freed.

Repeating jobs continue with their next scheduled run. A one-time job
that was interrupted does not run again; it can be started with
`POST /jobs/{id}/run`.

This is only safe because a run's history row is written **before**
the run starts, so we can tell which runs were in progress.

## Consequences
- A crash never leaves a job stuck.
- The user sees exactly which run was interrupted.
- Roadmap Step 3: let each job choose (`retries`, or re-run interrupted runs
  for idempotent jobs).
