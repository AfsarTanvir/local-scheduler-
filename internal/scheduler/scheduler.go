// Package scheduler decides when jobs run.
//
// Every second it looks for jobs whose NextRunAt has passed, marks each
// one as running, moves its NextRunAt forward and starts it in a
// goroutine. When the run finishes, the result is saved on the job.
package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/AfsarTanvir/local-scheduler-/internal/job"
	"github.com/AfsarTanvir/local-scheduler-/internal/store"
)

var (
	ErrAlreadyRunning = errors.New("job is already running")
	ErrStopped        = errors.New("scheduler is shutting down")

	errNotDue = errors.New("job is not due")
)

// tickInterval is how often the scheduler checks for due jobs.
// Jobs therefore start at most about one second late.
const tickInterval = time.Second

// RunFunc executes a job once. In production it is runner.Run;
// tests pass a fake.
type RunFunc func(ctx context.Context, j job.Job) job.Run

type Scheduler struct {
	store *store.Store
	run   RunFunc

	ctx    context.Context    // passed to runs; canceled to force-stop them
	cancel context.CancelFunc //
	quit   chan struct{}      // closed to stop the loop
	done   chan struct{}      // closed when the loop has stopped

	mu      sync.Mutex     // guards stopped, and makes "check stopped + start run" atomic
	stopped bool           //
	running sync.WaitGroup // counts runs in progress
}

func New(st *store.Store, run RunFunc) *Scheduler {
	ctx, cancel := context.WithCancel(context.Background())
	return &Scheduler{
		store:  st,
		run:    run,
		ctx:    ctx,
		cancel: cancel,
		quit:   make(chan struct{}),
	}
}

// Start begins the scheduling loop in the background.
func (s *Scheduler) Start() {
	s.done = make(chan struct{})
	go func() {
		defer close(s.done)
		ticker := time.NewTicker(tickInterval)
		defer ticker.Stop()
		for {
			select {
			case <-s.quit:
				return
			case now := <-ticker.C:
				s.tick(now)
			}
		}
	}()
}

// tick starts every job that is due at now.
func (s *Scheduler) tick(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return
	}

	for _, j := range s.store.List() {
		if !isDue(j, now) {
			continue
		}
		start := false
		claimed, err := s.store.Update(j.ID, func(j *job.Job) error {
			// Check again under the store's lock: the job may have been
			// paused, deleted or started since List.
			if !isDue(*j, now) {
				return errNotDue
			}
			j.NextRunAt = j.NextRun(now)
			if j.Running {
				// The previous run is still going. Skip this run
				// instead of running the same job twice at once.
				return nil
			}
			j.Running = true
			start = true
			return nil
		})
		if err != nil {
			continue
		}
		if !start {
			slog.Warn("skipped run: previous run still in progress", "job", j.ID, "name", j.Name)
			continue
		}
		s.start(claimed)
	}
}

// isDue reports whether j should start at now.
func isDue(j job.Job, now time.Time) bool {
	return j.Enabled && j.NextRunAt != nil && !j.NextRunAt.After(now)
}

// RunNow starts a job immediately, outside its schedule.
// Its NextRunAt does not change.
func (s *Scheduler) RunNow(id string) (job.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return job.Job{}, ErrStopped
	}

	claimed, err := s.store.Update(id, func(j *job.Job) error {
		if j.Running {
			return ErrAlreadyRunning
		}
		j.Running = true
		return nil
	})
	if err != nil {
		return job.Job{}, err
	}
	s.start(claimed)
	return claimed, nil
}

// start runs j in a goroutine and saves the result when it finishes.
// The caller must hold s.mu and must have set j.Running.
func (s *Scheduler) start(j job.Job) {
	s.running.Add(1)
	go func() {
		defer s.running.Done()
		slog.Info("job started", "job", j.ID, "name", j.Name)

		result := s.run(s.ctx, j)

		// Fails with ErrNotFound if the job was deleted while running;
		// then there is nothing to save.
		s.store.Update(j.ID, func(j *job.Job) error {
			j.Running = false
			j.LastRun = &result
			return nil
		})
		slog.Info("job finished", "job", j.ID, "name", j.Name, "status", result.Status, "error", result.Error)
	}()
}

// Pause stops future runs of a job. A run already in progress finishes normally.
func (s *Scheduler) Pause(id string) (job.Job, error) {
	return s.store.Update(id, func(j *job.Job) error {
		j.Enabled = false
		return nil
	})
}

// Resume enables a paused job again. A repeating job continues from now,
// so runs missed while it was paused are skipped. A one-time job that has
// not run yet keeps its RunAt, and runs right away if that time has passed.
func (s *Scheduler) Resume(id string) (job.Job, error) {
	now := time.Now()
	return s.store.Update(id, func(j *job.Job) error {
		if !j.Enabled && !j.IsOneTime() {
			j.NextRunAt = j.NextRun(now)
		}
		j.Enabled = true
		return nil
	})
}

// Stop shuts down gracefully: no new runs start, and runs in progress
// get until ctx ends to finish. After that they are canceled.
func (s *Scheduler) Stop(ctx context.Context) error {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return nil
	}
	s.stopped = true
	s.mu.Unlock()
	defer s.cancel()

	close(s.quit)
	if s.done != nil {
		<-s.done
	}

	finished := make(chan struct{})
	go func() {
		s.running.Wait()
		close(finished)
	}()

	select {
	case <-finished:
		return nil
	case <-ctx.Done():
		slog.Warn("shutdown timeout reached, canceling running jobs")
		s.cancel()
		<-finished
		return ctx.Err()
	}
}
