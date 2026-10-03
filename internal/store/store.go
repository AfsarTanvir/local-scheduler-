// Package store saves jobs in a SQLite database file, so they survive
// restarts. It is safe for concurrent use.
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite: no C compiler needed, builds for every OS

	"github.com/AfsarTanvir/local-scheduler-/internal/job"
)

var ErrNotFound = errors.New("job not found")

// keepRuns is how many runs of each job are kept in the history.
const keepRuns = 100

type Store struct {
	db *sql.DB
}

// Open opens the database file at path, creating it if needed, and brings
// its schema up to date.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	// busy_timeout: wait up to 5 s instead of failing when another process
	//               (for example the sqlite3 CLI) is holding a lock.
	// journal_mode=WAL: readers do not block the writer, and the reverse.
	// foreign_keys: SQLite only enforces REFERENCES (and ON DELETE CASCADE) when this is on.
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	// SQLite allows one writer at a time. With a single connection every
	// call runs one after another, like the mutex in the old in-memory store,
	// and a transaction makes Update's "check then change" atomic.
	db.SetMaxOpenConns(1)

	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

// Add saves a new job.
func (s *Store) Add(j job.Job) error {
	vals, err := jobValues(j)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO jobs (`+jobColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?)`, vals...)
	return err
}

// Get returns the job with the given id.
func (s *Store) Get(id string) (job.Job, error) {
	return getJob(s.db, id)
}

// List returns all jobs, oldest first. It never returns nil,
// so an empty list is encoded as [] in JSON.
func (s *Store) List() ([]job.Job, error) {
	return s.queryJobs(`SELECT ` + jobColumns + ` FROM jobs ORDER BY created_at, id`)
}

// Due returns the enabled jobs whose next run time is at or before now.
// The index on next_run_at keeps this fast with many jobs.
func (s *Store) Due(now time.Time) ([]job.Job, error) {
	return s.queryJobs(`SELECT `+jobColumns+` FROM jobs
		WHERE enabled = 1 AND next_run_at <= ?
		ORDER BY next_run_at`, formatTime(now))
}

// queryJobs runs a query that selects jobColumns and reads all rows.
// Rows are read completely before returning, which matters with a
// single connection: an open result would block every other call.
func (s *Store) queryJobs(query string, args ...any) ([]job.Job, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []job.Job{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, j)
	}
	return list, rows.Err()
}

// Delete removes the job with the given id.
func (s *Store) Delete(id string) error {
	res, err := s.db.Exec(`DELETE FROM jobs WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Update changes a job atomically. fn gets the current job; the change is
// saved only if fn returns nil. Read, fn and write happen in one
// transaction, so no other change can happen in between.
func (s *Store) Update(id string, fn func(*job.Job) error) (job.Job, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return job.Job{}, err
	}
	defer tx.Rollback() // does nothing after Commit

	j, err := getJob(tx, id)
	if err != nil {
		return job.Job{}, err
	}
	if err := fn(&j); err != nil {
		return job.Job{}, err
	}

	vals, err := jobValues(j)
	if err != nil {
		return job.Job{}, err
	}
	_, err = tx.Exec(`UPDATE jobs SET spec = ?, enabled = ?, running = ?, next_run_at = ?, last_run = ?, created_at = ?
		WHERE id = ?`, append(vals[1:], id)...)
	if err != nil {
		return job.Job{}, err
	}
	return j, tx.Commit()
}

// StartRun records that a run of the job has started and returns the run's id.
func (s *Store) StartRun(jobID string, startedAt time.Time) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO runs (job_id, started_at, status) VALUES (?, ?, ?)`,
		jobID, formatTime(startedAt), job.StatusRunning)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// FinishRun saves the result of run r (r.ID from StartRun): it updates the
// history row, sets the job's lastRun and running=false, and deletes old
// history. All in one transaction, so they never disagree.
func (s *Store) FinishRun(jobID string, r job.Run) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := finishRun(tx, jobID, r); err != nil {
		return err
	}
	return tx.Commit()
}

func finishRun(tx *sql.Tx, jobID string, r job.Run) error {
	_, err := tx.Exec(`UPDATE runs SET started_at = ?, finished_at = ?, status = ?, output = ?, error = ?
		WHERE id = ?`, formatTime(r.StartedAt), formatTime(r.FinishedAt), r.Status, r.Output, r.Error, r.ID)
	if err != nil {
		return err
	}

	lastRun, err := json.Marshal(r)
	if err != nil {
		return err
	}
	// If the job was deleted while running, this changes nothing.
	if _, err := tx.Exec(`UPDATE jobs SET running = 0, last_run = ? WHERE id = ?`, string(lastRun), jobID); err != nil {
		return err
	}

	// Keep only the newest runs, so the table does not grow forever.
	_, err = tx.Exec(`DELETE FROM runs WHERE job_id = ? AND id NOT IN (
		SELECT id FROM runs WHERE job_id = ? ORDER BY id DESC LIMIT ?)`, jobID, jobID, keepRuns)
	return err
}

// Runs returns up to limit runs of a job, newest first.
func (s *Store) Runs(jobID string, limit int) ([]job.Run, error) {
	if _, err := s.Get(jobID); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT id, started_at, finished_at, status, output, error FROM runs
		WHERE job_id = ? ORDER BY id DESC LIMIT ?`, jobID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	runs := []job.Run{}
	for rows.Next() {
		var (
			r          job.Run
			startedAt  string
			finishedAt sql.NullString
		)
		if err := rows.Scan(&r.ID, &startedAt, &finishedAt, &r.Status, &r.Output, &r.Error); err != nil {
			return nil, err
		}
		if r.StartedAt, err = parseTime(startedAt); err != nil {
			return nil, err
		}
		if finishedAt.Valid {
			if r.FinishedAt, err = parseTime(finishedAt.String); err != nil {
				return nil, err
			}
		}
		runs = append(runs, r)
	}
	return runs, rows.Err()
}

// jobColumns is the column order used by jobValues and scanJob.
const jobColumns = `id, spec, enabled, running, next_run_at, last_run, created_at`

// querier is what *sql.DB and *sql.Tx have in common.
type querier interface {
	QueryRow(query string, args ...any) *sql.Row
}

func getJob(q querier, id string) (job.Job, error) {
	j, err := scanJob(q.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return job.Job{}, ErrNotFound
	}
	return j, err
}

// jobValues returns the column values of j, in jobColumns order.
// The Spec is stored as JSON because SQL never needs to look inside it;
// fields the scheduler filters on are real columns.
func jobValues(j job.Job) ([]any, error) {
	spec, err := json.Marshal(j.Spec)
	if err != nil {
		return nil, err
	}
	var nextRunAt, lastRun any // nil is stored as NULL
	if j.NextRunAt != nil {
		nextRunAt = formatTime(*j.NextRunAt)
	}
	if j.LastRun != nil {
		b, err := json.Marshal(j.LastRun)
		if err != nil {
			return nil, err
		}
		lastRun = string(b)
	}
	return []any{j.ID, string(spec), j.Enabled, j.Running, nextRunAt, lastRun, formatTime(j.CreatedAt)}, nil
}

// scanJob reads one row of jobColumns.
func scanJob(row interface{ Scan(...any) error }) (job.Job, error) {
	var (
		j                  job.Job
		spec, createdAt    string
		nextRunAt, lastRun sql.NullString
	)
	if err := row.Scan(&j.ID, &spec, &j.Enabled, &j.Running, &nextRunAt, &lastRun, &createdAt); err != nil {
		return job.Job{}, err
	}
	if err := json.Unmarshal([]byte(spec), &j.Spec); err != nil {
		return job.Job{}, fmt.Errorf("job %s: read spec: %w", j.ID, err)
	}
	if nextRunAt.Valid {
		t, err := parseTime(nextRunAt.String)
		if err != nil {
			return job.Job{}, err
		}
		j.NextRunAt = &t
	}
	if lastRun.Valid {
		j.LastRun = &job.Run{}
		if err := json.Unmarshal([]byte(lastRun.String), j.LastRun); err != nil {
			return job.Job{}, fmt.Errorf("job %s: read last run: %w", j.ID, err)
		}
	}
	var err error
	j.CreatedAt, err = parseTime(createdAt)
	return j, err
}

// timeFormat stores times as fixed-width UTC text. Because every value has
// the same length and order of fields, comparing the strings in SQL
// (next_run_at <= ?) gives the same result as comparing the times.
const timeFormat = "2006-01-02T15:04:05.000000000Z"

func formatTime(t time.Time) string {
	return t.UTC().Format(timeFormat)
}

func parseTime(s string) (time.Time, error) {
	return time.Parse(timeFormat, s)
}
