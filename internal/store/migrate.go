package store

import (
	"database/sql"
	"fmt"
)

// migrations change the database schema step by step. Each one runs once,
// in order. The database remembers how many have run in PRAGMA user_version.
//
// To change the schema, add a new entry at the end. Never edit an old one:
// existing databases have already run it.
var migrations = []string{
	// 1: jobs
	`CREATE TABLE jobs (
		id          TEXT PRIMARY KEY,
		spec        TEXT NOT NULL,    -- job.Spec as JSON
		enabled     INTEGER NOT NULL, -- 0 or 1
		running     INTEGER NOT NULL, -- 0 or 1
		next_run_at TEXT,             -- NULL when nothing is left to run
		last_run    TEXT,             -- job.Run as JSON
		created_at  TEXT NOT NULL
	);
	CREATE INDEX jobs_next_run_at ON jobs (next_run_at);`,

	// 2: run history. AUTOINCREMENT so ids are never reused after old runs
	// are deleted. ON DELETE CASCADE removes a job's runs with the job.
	`CREATE TABLE runs (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		job_id      TEXT NOT NULL REFERENCES jobs (id) ON DELETE CASCADE,
		started_at  TEXT NOT NULL,
		finished_at TEXT,          -- NULL while running
		status      TEXT NOT NULL, -- running, success or failed
		output      TEXT NOT NULL DEFAULT '',
		error       TEXT NOT NULL DEFAULT ''
	);
	CREATE INDEX runs_job_id ON runs (job_id, id);`,
}

func migrate(db *sql.DB) error {
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version > len(migrations) {
		return fmt.Errorf("database schema version %d is newer than this program (%d)", version, len(migrations))
	}

	for v := version; v < len(migrations); v++ {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[v]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", v+1, err)
		}
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, v+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
