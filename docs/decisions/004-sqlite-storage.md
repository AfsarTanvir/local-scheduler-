# 004 — Store jobs in SQLite

**Status:** accepted · 2026-10-03

## Problem
Jobs were kept in memory, so a restart or crash lost all of them.
Storage must survive restarts, but the project rule is "one binary, no
required dependencies": `docker run` alone must work.

## Options
| Option | Pros | Cons |
|---|---|---|
| JSON file | Very simple | Must rewrite the whole file on every change; no transactions; easy to corrupt on crash |
| SQLite | A real database in one file, transactions, SQL, crash-safe, no server | One writer at a time (fine for one instance) |
| PostgreSQL | Many instances can share it | Users must run a database server just to start |

## Decision
**SQLite** as the default, with the pure-Go driver `modernc.org/sqlite`
(no C compiler, so cross-compiling to every OS still works).
PostgreSQL stays an optional later addition for multiple instances.

Details:
- **One connection** (`SetMaxOpenConns(1)`): SQLite has a single writer anyway,
  and it makes every call run one after another, like the old mutex.
- **`Update` runs in a transaction**: read, change and write are atomic.
- **Spec stored as JSON**, because SQL never looks inside it. Fields the
  scheduler filters on (`enabled`, `running`, `next_run_at`) are real columns.
- **Times as fixed-width UTC text** (`2026-10-03T20:00:00.000000000Z`), so
  string comparison in SQL equals time comparison, and values are readable
  in the `sqlite3` CLI.
- **Migrations**: a list of SQL scripts; `PRAGMA user_version` stores how many
  have run. New schema change = append a script, never edit an old one.
- **WAL mode** and `busy_timeout` so tools like the `sqlite3` CLI can read
  the file while the scheduler is running.

## Consequences
- Jobs survive restarts. File location: `DB_PATH` (default `data/scheduler.db`).
- Only one scheduler process should use a database file at a time.
- Store methods can now fail, so they all return errors.
