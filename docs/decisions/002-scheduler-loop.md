# 002 — How the scheduler loop works

**Status:** accepted · 2026-10-03

## Problem
Jobs must start at their scheduled time. We also need clear answers to:
- What if a job is still running when its next run is due?
- What if runs were missed (paused job, laptop asleep, slow tick)?
- What happens to running jobs on shutdown?

## Options for the loop
| Option | How | Trade-off |
|---|---|---|
| Tick every second | Every second, start all jobs with `nextRunAt <= now` | Simple, easy to reason about; up to ~1 s late |
| Sleep until the next due job | Compute the earliest `nextRunAt`, sleep until then, wake early when jobs change | Exact timing, but more code (wake-up channel, recalculation) |
| One timer per job | `time.AfterFunc` per job | Many timers to cancel/replace on every change |

## Decision
**Tick every second.** Cron has minute precision, so ~1 s delay does not matter,
and the code stays small enough to explain in a few sentences.

Rules:
1. **Claiming:** a job is marked `running` and its `nextRunAt` is moved forward
   inside one `store.Update` call (under a lock), before the run starts.
2. **No overlap:** if the previous run is still going when the next one is due,
   that run is **skipped** (logged as a warning) and `nextRunAt` moves on.
3. **Missed runs:** the next run is always calculated from *now*, so many
   missed runs turn into **one** catch-up run, then the normal schedule.
4. **Pause/resume:** resuming a repeating job continues from now (runs missed
   while paused are skipped). A one-time job that has not run keeps its time.
5. **Shutdown:** stop starting new runs, wait up to 30 s for running ones,
   then cancel them through their context.

## Consequences
- Up to ~1 s start delay.
- Overlap and missed-run behavior is fixed for now; roadmap Step 3 makes it
  configurable per job (`allowConcurrent`, `misfirePolicy`).
- State is in memory only; roadmap Step 2 adds persistence.
